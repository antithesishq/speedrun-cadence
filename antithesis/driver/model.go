package main

// The model: what the driver sent to Cadence, what Cadence acknowledged, and
// the checks of every result, history and description against it. All
// guarantee checks run when an outcome arrives (in completion callbacks), so
// outcomes that faults delay are still checked.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

const (
	StPending = iota
	StOK
	StUnknown
	StFailed
)

type Sig struct {
	ID    string
	V     int64
	Sent  time.Time
	Acked time.Time
	State int
	Kind  string
}

type WF struct {
	WID      string
	Type     string
	Input    any
	Expected int64 // chain / parent
	TimerMs  int
	Rounds   int // race

	StartState int
	StartKind  string
	RunIDs     map[string]bool // every run ID Cadence reported for this workflow ID
	Sigs       []*Sig
	SigByID    map[string]*Sig

	FinishSent    bool
	CancelSent    bool
	TerminateSent bool
	Resets        int
	ResetsPending int

	Closed         bool
	CloseStatus    string
	ClosedRunID    string
	Applied        map[string]int // counter result: signal ID -> position
	HistChecked    bool
	ClosedAt       time.Time
	ResultInFlight bool
	Visible        bool
	Reused         bool
	PrevRunID      string
	ResetBases     map[string]bool
	Foreign        map[string]bool
	DupPending     int
	FirstRunID     string
}

type Model struct {
	mu      sync.Mutex
	wfs     map[string]*WF
	closed  []*WF // recently closed, for describe/list checks
	nextWF  int
	nextSig int
}

func NewModel() *Model { return &Model{wfs: map[string]*WF{}} }

func (m *Model) newWF(typ string) *WF {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextWF++
	w := &WF{WID: fmt.Sprintf("%s-%d", typ, m.nextWF), Type: typ, RunIDs: map[string]bool{}, SigByID: map[string]*Sig{}}
	m.wfs[w.WID] = w
	return w
}

func (m *Model) newSig(w *WF, act bool, v int64) *Sig {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSig++
	id := fmt.Sprintf("s%d", m.nextSig)
	if act {
		id = "act-" + id
	}
	s := &Sig{ID: id, V: v, Sent: time.Now()}
	w.Sigs = append(w.Sigs, s)
	w.SigByID[id] = s
	return s
}

// pick returns the workflows matching f, sorted by ID.
func (m *Model) pick(f func(*WF) bool) []*WF {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*WF
	for _, w := range m.wfs {
		if f(w) {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WID < out[j].WID })
	return out
}

func (m *Model) openCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.wfs)
}

// ---------- start ----------

func (m *Model) startDone(w *WF, o Outcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch o.Outcome {
	case "ok":
		var v struct {
			RunID string `json:"run_id"`
		}
		_ = json.Unmarshal(o.Value, &v)
		w.StartState = StOK
		w.RunIDs[v.RunID] = true
		if w.FirstRunID == "" {
			w.FirstRunID = v.RunID
		}
	case "failed":
		w.StartKind = o.ErrKind
		if w.StartState != StOK {
			w.StartState = StFailed
		}
	default:
		w.StartKind = o.ErrKind
		if w.StartState != StOK {
			w.StartState = StUnknown
		}
	}
}

// ---------- signals ----------

func (m *Model) signalDone(w *WF, s *Sig, o Outcome, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.Kind = o.ErrKind
	if o.Outcome == "ok" {
		s.State = StOK
		s.Acked = at
		assert.Sometimes(true, "a signal is acknowledged", nil)
		if w.Applied != nil {
			// The result was read before this ack arrived: it must still include the signal.
			_, in := w.Applied[s.ID]
			assert.Always(in, "an acknowledged signal is applied exactly once",
				map[string]any{"wid": w.WID, "signal": s.ID, "late_ack": true, "close_status": w.CloseStatus})
		}
		return
	}
	if o.Outcome == "failed" {
		s.State = StFailed
	} else {
		s.State = StUnknown
	}
}

// ---------- results ----------

type CounterState struct {
	Applied []string `json:"applied"`
	Sum     int64    `json:"sum"`
	Runs    int      `json:"runs"`
	Acts    int64    `json:"acts"`
	ActErrs []string `json:"act_errs"`
}

type resultVal struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	RunID  string          `json:"run_id"`
	Error  string          `json:"error"`
}

// resultDone checks a final result. It returns true if the workflow closed.
func (m *Model) resultDone(w *WF, o Outcome) bool {
	if o.Outcome != "ok" {
		return false
	}
	var r resultVal
	if err := json.Unmarshal(o.Value, &r); err != nil {
		assert.Unreachable("a workflow result can't be decoded", map[string]any{"wid": w.WID, "err": err.Error()})
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if w.PrevRunID != "" && (r.RunID == w.PrevRunID || w.FirstRunID == "") {
		// The read resolved to the previous run of a reused ID: this start never took effect (or isn't visible yet).
		if r.RunID == w.PrevRunID && w.StartState != StOK {
			w.StartState = StFailed
		}
		return false
	}
	if w.Foreign[r.RunID] {
		return false // a run started by a duplicate start after this workflow closed
	}
	// A committed reset closes its base run as terminated; a base run that closed any other way
	// was never reset (the reset failed or timed out without committing), so its result is final.
	if w.ResetsPending > 0 || (w.Resets > 0 && (r.RunID == "" || (w.ResetBases[r.RunID] && r.Status == "terminated"))) {
		return false // a reset's base run (or an unnamed run): not the final outcome; read again
	}
	if w.Closed {
		// A later read of the same closed workflow must agree (closed runs never reopen).
		if r.Status != "terminated" || w.Resets == 0 {
			assert.Always(r.Status == w.CloseStatus, "a closed workflow keeps its outcome",
				map[string]any{"wid": w.WID, "first": w.CloseStatus, "now": r.Status})
		}
		return true
	}
	// A reset terminates the base run; a result read that resolved to that run
	// is not this workflow's final outcome. Read again later.
	if r.Status == "terminated" && (w.Resets > 0 || w.ResetsPending > 0) && !w.TerminateSent {
		return false
	}
	details := map[string]any{"wid": w.WID, "type": w.Type, "status": r.Status, "error": r.Error,
		"cancel_sent": w.CancelSent, "terminate_sent": w.TerminateSent, "resets": w.Resets}
	allowed := r.Status == "completed" ||
		(r.Status == "canceled" && w.CancelSent) ||
		(r.Status == "terminated" && w.TerminateSent)
	assert.Always(allowed, "a workflow ends only as completed, or as canceled or terminated on request", details)
	w.Closed, w.CloseStatus, w.ClosedRunID, w.ClosedAt = true, r.Status, r.RunID, time.Now()
	if r.RunID != "" {
		w.RunIDs[r.RunID] = true
	}
	switch r.Status {
	case "canceled":
		assert.Sometimes(true, "a cancel request closes a workflow as canceled", map[string]any{"type": w.Type})
	case "terminated":
		assert.Sometimes(true, "a terminate request closes a workflow", map[string]any{"type": w.Type})
	}
	if r.Status != "completed" {
		return true
	}
	switch w.Type {
	case "counter":
		m.checkCounter(w, r.Result)
	case "chain", "parent":
		var got int64
		_ = json.Unmarshal(r.Result, &got)
		details["got"], details["want"] = got, w.Expected
		assert.Always(got == w.Expected, "a completed workflow returns the result its inputs determine", details)
		if w.Type == "parent" {
			assert.Sometimes(true, "a parent workflow completes with all its children", nil)
		} else {
			assert.Sometimes(true, "an activity workflow completes", nil)
		}
	case "timer":
		var tr struct{ T0, T1 int64 }
		_ = json.Unmarshal(r.Result, &tr)
		assert.AlwaysGreaterThanOrEqualTo(tr.T1-tr.T0, int64(w.TimerMs), "a timer never fires before its duration",
			map[string]any{"wid": w.WID, "t0": tr.T0, "t1": tr.T1, "ms": w.TimerMs})
	case "race":
		var rr struct{ Fired, Canceled, Rounds int }
		_ = json.Unmarshal(r.Result, &rr)
		details["result"] = rr
		assert.Always(rr.Rounds == w.Rounds && rr.Fired+rr.Canceled == w.Rounds,
			"a completed workflow returns the result its inputs determine", details)
		assert.Sometimes(rr.Canceled > 0, "a race workflow cancels a timer", nil)
		assert.Sometimes(w.Rounds > 1 && rr.Canceled > 1, "a race workflow cancels timers in consecutive rounds", nil)
	}
	return true
}

func (m *Model) checkCounter(w *WF, raw json.RawMessage) {
	var st CounterState
	if err := json.Unmarshal(raw, &st); err != nil {
		assert.Unreachable("a workflow result can't be decoded", map[string]any{"wid": w.WID, "err": err.Error()})
		return
	}
	w.Applied = map[string]int{}
	dups := []string{}
	unknown := []string{}
	var sum, acts int64
	for i, id := range st.Applied {
		if _, seen := w.Applied[id]; seen {
			dups = append(dups, id)
		}
		w.Applied[id] = i
		s := w.SigByID[id]
		if s == nil {
			unknown = append(unknown, id)
			continue
		}
		sum += s.V
		if len(id) > 4 && id[:4] == "act-" {
			acts += s.V*3 + 1
		}
	}
	base := map[string]any{"wid": w.WID, "runs": st.Runs, "resets": w.Resets, "applied_n": len(st.Applied), "sent_n": len(w.Sigs)}
	with := func(k string, v any) map[string]any {
		d := map[string]any{k: v}
		for a, b := range base {
			d[a] = b
		}
		return d
	}
	if w.Resets > 0 || w.ResetsPending > 0 {
		// Known finding: retried-signal-duplicated-after-reset. Reported apart so other duplicates stay visible.
		assert.Always(len(dups) == 0, "a signal is applied at most once in a workflow that was reset", with("duplicates", dups))
	} else {
		assert.Always(len(dups) == 0, "an acknowledged signal is applied exactly once", with("duplicates", dups))
	}
	assert.Always(len(unknown) == 0, "a workflow only applies signals that clients sent", with("unknown_ids", unknown))
	missing := []string{}
	unknownApplied := 0
	for _, s := range w.Sigs {
		_, in := w.Applied[s.ID]
		if s.State == StOK && !in {
			missing = append(missing, s.ID)
		}
		if s.State != StOK && in {
			unknownApplied++
		}
	}
	if w.Resets > 0 || w.ResetsPending > 0 {
		// Known finding acked-signal-lost-on-reset: reported apart so other losses stay visible.
		assert.Always(len(missing) == 0, "an acknowledged signal survives a reset of its workflow", with("missing", missing))
	} else {
		assert.Always(len(missing) == 0, "an acknowledged signal is applied exactly once", with("missing", missing))
	}
	assert.Sometimes(unknownApplied > 0, "a signal with an unknown outcome is applied", nil)
	// Order: a signal acknowledged before another was sent is applied first.
	var bad []string
	for _, a := range w.Sigs {
		if a.State != StOK {
			continue
		}
		for _, b := range w.Sigs {
			if b.State != StOK || !a.Acked.Before(b.Sent) {
				continue
			}
			ia, oka := w.Applied[a.ID]
			ib, okb := w.Applied[b.ID]
			if oka && okb && ia > ib {
				bad = append(bad, a.ID+"<"+b.ID)
			}
		}
	}
	assert.Always(len(bad) == 0, "signals are applied in the order they were acknowledged", with("inversions", bad))
	// After a cancel request, an in-flight activity is canceled while the workflow may still complete on "finish".
	actsOK := st.Acts == acts || (w.CancelSent && len(st.ActErrs) > 0)
	assert.Always(st.Sum == sum && actsOK, "a completed workflow returns the result its inputs determine",
		with("sum", fmt.Sprintf("got %d/%d want %d/%d act_errs=%v", st.Sum, st.Acts, sum, acts, st.ActErrs)))
	assert.Sometimes(st.Runs > 1, "a signal workflow continues as new and completes", nil)
	assert.Sometimes(w.Resets > 0, "a reset workflow completes", nil)
}

// ---------- history ----------

type Ev struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
	Ts   int64  `json:"ts"`
	Sig  string `json:"sig"`
	Ref  int64  `json:"ref"`
	Name string `json:"name"`
}

var closeEvents = map[string]bool{
	"WorkflowExecutionCompleted": true, "WorkflowExecutionFailed": true, "WorkflowExecutionTimedOut": true,
	"WorkflowExecutionCanceled": true, "WorkflowExecutionTerminated": true, "WorkflowExecutionContinuedAsNew": true,
}

var resolveOf = map[string]string{
	"ActivityTaskCompleted": "activity", "ActivityTaskFailed": "activity", "ActivityTaskTimedOut": "activity", "ActivityTaskCanceled": "activity",
	"TimerFired": "timer", "TimerCanceled": "timer",
	"ChildWorkflowExecutionCompleted": "child", "ChildWorkflowExecutionFailed": "child", "ChildWorkflowExecutionCanceled": "child",
	"ChildWorkflowExecutionTimedOut": "child", "ChildWorkflowExecutionTerminated": "child",
}

// checkHistory checks one closed run's history (Guarantee 3).
func checkHistory(wid, runID string, evs []Ev, wantClosed bool, wasReset bool) {
	d := map[string]any{"wid": wid, "run_id": runID, "n": len(evs)}
	if len(evs) == 0 {
		assert.Always(false, "a workflow history is well formed", d)
		return
	}
	all, resetDups := historyChecks(evs, wantClosed, wasReset)
	// Known finding stale-history-after-shard-steal: a read after close that lacks the close event
	// is reported apart, so other history problems stay visible.
	var problems, stale []string
	for _, p := range all {
		if strings.HasSuffix(p, "close events") {
			stale = append(stale, p)
		} else {
			problems = append(problems, p)
		}
	}
	if wantClosed {
		assert.Always(len(stale) == 0, "a history read after a workflow closed includes its close event", map[string]any{"wid": wid, "run_id": runID, "n": len(evs), "problems": stale})
	}
	// A local activity forces a heartbeat at most every ~80% of the decision timeout; the race
	// workflow's longest local activity (7 s, 4 s timeout) needs ~3 heartbeats per round.
	run := emptyDecisionRun(evs)
	assert.AlwaysLessThan(run, 20, "a workflow does not heartbeat decisions without progress", map[string]any{"wid": wid, "run_id": runID, "empty_cycles": run, "n": len(evs)})
	d["problems"] = problems
	assert.Always(len(problems) == 0, "a workflow history is well formed", d)
	if wasReset {
		assert.Always(len(resetDups) == 0, "a signal is applied at most once in a workflow that was reset", map[string]any{"wid": wid, "run_id": runID, "problems": resetDups})
	}
	for _, e := range evs {
		if e.Type == "TimerCanceled" {
			assert.Sometimes(true, "a history contains a canceled timer", nil)
			break
		}
	}
}

func historyProblems(evs []Ev, wantClosed, wasReset bool) []string {
	p, _ := historyChecks(evs, wantClosed, wasReset)
	return p
}

func historyChecks(evs []Ev, wantClosed bool, wasReset bool) ([]string, []string) {
	var problems, resetDups []string
	for i, e := range evs {
		if e.ID != int64(i+1) {
			problems = append(problems, fmt.Sprintf("event %d has id %d", i+1, e.ID))
			break
		}
	}
	if evs[0].Type != "WorkflowExecutionStarted" {
		problems = append(problems, "first event is "+evs[0].Type)
	}
	nClose := 0
	for i, e := range evs {
		if closeEvents[e.Type] {
			nClose++
			if i != len(evs)-1 {
				problems = append(problems, fmt.Sprintf("close event %s at %d is not last", e.Type, e.ID))
			}
		}
	}
	if wantClosed && nClose != 1 {
		problems = append(problems, fmt.Sprintf("%d close events", nClose))
	}
	// Each timer / activity / child resolves at most once, after it started.
	resolved := map[int64]string{}
	started := map[int64]string{}
	sigs := map[string]int64{}
	timerID := map[int64]string{}
	for _, e := range evs {
		switch e.Type {
		case "TimerStarted":
			started[e.ID] = "timer"
			timerID[e.ID] = e.Name
		case "ActivityTaskScheduled":
			started[e.ID] = "activity"
		case "StartChildWorkflowExecutionInitiated":
			started[e.ID] = "child"
		case "WorkflowExecutionSignaled":
			if e.Sig != "" {
				if prev, dup := sigs[e.Sig]; dup {
					if wasReset {
						resetDups = append(resetDups, fmt.Sprintf("signal %s recorded twice (events %d and %d)", e.Sig, prev, e.ID))
					} else {
						problems = append(problems, fmt.Sprintf("signal %s recorded twice (events %d and %d)", e.Sig, prev, e.ID))
					}
				}
				sigs[e.Sig] = e.ID
			}
		}
		if kind, ok := resolveOf[e.Type]; ok && e.Ref != 0 {
			if prev, dup := resolved[e.Ref]; dup {
				problems = append(problems, fmt.Sprintf("%s %d resolved twice: %s then %s (event %d)", kind, e.Ref, prev, e.Type, e.ID))
			}
			if started[e.Ref] != kind {
				problems = append(problems, fmt.Sprintf("%s (event %d) refers to %d, which is not a started %s", e.Type, e.ID, e.Ref, kind))
			}
			resolved[e.Ref] = e.Type
		}
	}
	if len(problems) > 8 {
		problems = problems[:8]
	}
	return problems, resetDups
}

// emptyDecisionRun returns the longest run of consecutive decision cycles
// (DecisionTaskScheduled, Started, Completed) with no other event in between.
func emptyDecisionRun(evs []Ev) int {
	best, cur := 0, 0
	for i := 0; i+2 < len(evs); {
		if evs[i].Type == "DecisionTaskScheduled" && evs[i+1].Type == "DecisionTaskStarted" && evs[i+2].Type == "DecisionTaskCompleted" {
			cur++
			if cur > best {
				best = cur
			}
			i += 3
			continue
		}
		cur = 0
		i++
	}
	return best
}

func (m *Model) historyDone(w *WF, runID string, o Outcome) {
	if o.Outcome != "ok" {
		return
	}
	var evs []Ev
	if err := json.Unmarshal(o.Value, &evs); err != nil {
		return
	}
	m.mu.Lock()
	wasReset := w.Resets > 0 || w.ResetsPending > 0
	m.mu.Unlock()
	checkHistory(w.WID, runID, evs, true, wasReset)
	m.mu.Lock()
	w.HistChecked = true
	m.mu.Unlock()
}

// retire moves a closed, history-checked workflow out of the active set.
func (m *Model) retire(w *WF) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.wfs, w.WID)
	m.closed = append(m.closed, w)
	if len(m.closed) > 40 {
		m.closed = m.closed[len(m.closed)-40:]
	}
}

func (m *Model) recentClosed() []*WF {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*WF(nil), m.closed...)
}
