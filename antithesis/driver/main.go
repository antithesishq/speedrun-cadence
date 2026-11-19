package main

// Cadence workload driver. A hegel concurrent state machine whose rules start,
// signal, query, cancel, terminate and reset workflows through the client
// containers (stateful-clients pattern), and check every outcome against the
// model. An eventually stops faults, finishes every workflow and checks
// recovery, completion and visibility, then ends the timeline.

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"hegel.dev/go/hegel"
)

const (
	MaxOpen    = 24
	RuleWait   = 2 * time.Second
	ResultWait = 4 * time.Second
	MaxSignals = 40
)

type Driver struct {
	cs        *Clients
	m         *Model
	names     []string
	active    atomic.Bool
	stopping  atomic.Bool
	evMu      sync.Mutex
	lastReset atomic.Int64
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	names := strings.Split(envOr("CLIENTS", "client1,client2,client3"), ",")
	d := &Driver{m: NewModel(), names: names}
	d.cs = NewClients(names, &d.active)
	d.startup()
	lifecycle.SetupComplete(map[string]any{"clients": names})
	log.Printf("setup complete; waiting for /workload-active")
	for {
		if _, err := os.Stat("/workload-active"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.active.Store(true)
	assert.Reachable("the workload starts", nil)
	if s := os.Getenv("EVENTUALLY_AFTER_S"); s != "" {
		n, _ := strconv.Atoi(s)
		go func() { time.Sleep(time.Duration(n) * time.Second); d.eventually() }()
	}
	hegel.Workload(func(tc hegel.TestCase) {
		hegel.RunStateful(tc, d,
			hegel.WithBoundedConcurrency(6),
			hegel.WithStatefulStepCount(math.MaxInt),
		)
	}, hegel.WithPhases(hegel.PhaseGenerate), hegel.SuppressHealthCheck(hegel.AllHealthChecks()...))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// call retries an admin step until it gives one of the accepted outcomes.
func (d *Driver) mustCall(client, method string, params any, accept func(Outcome) bool) Outcome {
	last := ""
	for {
		st, o := d.cs.Call(client, method, params, 30*time.Second, nil)
		if st == Done && accept(o) {
			return o
		}
		msg := fmt.Sprintf("%s on %s: status=%d outcome=%s kind=%s err=%.200s", method, client, st, o.Outcome, o.ErrKind, o.Error)
		if msg != last {
			log.Printf("waiting: %s", msg)
			last = msg
		}
		time.Sleep(time.Second)
	}
}

func isOK(o Outcome) bool { return o.Outcome == "ok" }

func (d *Driver) startup() {
	for _, c := range d.names {
		d.mustCall(c, "health", nil, isOK)
	}
	log.Printf("all clients healthy")
	d.mustCall(d.names[0], "register_domain", nil, func(o Outcome) bool { return isOK(o) || o.ErrKind == "domain_exists" })
	for _, c := range d.names {
		d.mustCall(c, "describe_domain", nil, isOK)
		d.mustCall(c, "start_worker", nil, isOK)
	}
	log.Printf("domain registered, workers started")
	// One workflow end to end, so every service is serving before the snapshot.
	in := map[string]any{"n": 1, "seed": 1}
	d.mustCall(d.names[0], "start", map[string]any{"wid": "setup-probe", "type": "chain", "input": in, "reuse": 1},
		func(o Outcome) bool { return isOK(o) || o.ErrKind == "already_started" })
	d.mustCall(d.names[0], "result", map[string]any{"wid": "setup-probe", "timeout_ms": 20000},
		func(o Outcome) bool { return isOK(o) && strings.Contains(string(o.Value), "completed") })
	log.Printf("setup probe workflow completed")
}

// ---------- helpers ----------

func (d *Driver) client(tc hegel.TestCase) string {
	return d.names[hegel.Draw(tc, hegel.Integers(0, len(d.names)-1))]
}

func pickOne[T any](tc hegel.TestCase, xs []T) (T, bool) {
	var zero T
	if len(xs) == 0 {
		return zero, false
	}
	return xs[hegel.Draw(tc, hegel.Integers(0, len(xs)-1))], true
}

func chance(tc hegel.TestCase, p float64) bool { return hegel.Draw(tc, hegel.WeightedBooleans(p)) }

func (d *Driver) busy() bool { return d.stopping.Load() }

// ---------- rules ----------

// RuleStart starts a new workflow of a drawn type.
func (d *Driver) RuleStart(tc hegel.TestCase) {
	if d.busy() || d.m.openCount() >= MaxOpen {
		return
	}
	typ := hegel.Draw(tc, hegel.SampledFrom([]string{"counter", "counter", "chain", "timer", "parent", "race"}))
	w := d.m.newWF(typ)
	params := map[string]any{"wid": w.WID, "type": typ, "reuse": hegel.Draw(tc, hegel.Integers(0, 2))}
	method := "start"
	switch typ {
	case "counter":
		in := map[string]any{"can_every": hegel.Draw(tc, hegel.SampledFrom([]int{0, 3, 10})), "state": map[string]any{}}
		w.Input = in
		params["input"] = in
		params["exec_timeout_s"] = 7 * 24 * 3600
		if chance(tc, 0.3) {
			method = "signal_with_start"
			s := d.m.newSig(w, false, hegel.Draw(tc, hegel.Integers(int64(1), 100)))
			params["sig"] = map[string]any{"id": s.ID, "v": s.V}
			params["_sig"] = s.ID
		}
	case "chain":
		in := map[string]any{
			"n": hegel.Draw(tc, hegel.Integers(1, 5)), "parallel": chance(tc, 0.4), "seed": hegel.Draw(tc, hegel.Integers(int64(1), 1000)),
			"fail_attempts": hegel.Draw(tc, hegel.Integers(0, 2)), "sleep_ms": hegel.Draw(tc, hegel.SampledFrom([]int{0, 0, 1500, 5000})),
			"local": chance(tc, 0.25),
		}
		w.Input = in
		n, seed := in["n"].(int), in["seed"].(int64)
		for i := 0; i < n; i++ {
			w.Expected += seed*1000 + int64(i*i)
		}
		params["input"] = in
	case "timer":
		ms := hegel.Draw(tc, hegel.SampledFrom([]int{100, 1000, 3000, 10000, 30000}))
		w.TimerMs = ms
		params["input"] = map[string]any{"ms": ms}
	case "parent":
		k := hegel.Draw(tc, hegel.Integers(1, 3))
		seed := hegel.Draw(tc, hegel.Integers(int64(1), 1000))
		for i := 0; i < k; i++ {
			s := seed + int64(i)
			w.Expected += s*1000 + (s*1000 + 1) // children: N=2, steps 0 and 1
		}
		params["input"] = map[string]any{"k": k, "seed": seed}
	case "race":
		n := hegel.Draw(tc, hegel.Integers(1, 4))
		rounds := make([]map[string]int, n)
		for i := range rounds {
			rounds[i] = map[string]int{
				"timer_ms": hegel.Draw(tc, hegel.SampledFrom([]int{200, 1000, 2500, 4000})),
				"la_ms":    hegel.Draw(tc, hegel.SampledFrom([]int{100, 1500, 3500, 4500, 7000})),
			}
		}
		w.Rounds = n
		params["input"] = map[string]any{"rounds": rounds}
		params["decision_timeout_s"] = 4 // local activities past ~80% of this force decision heartbeats
	}
	sigID, _ := params["_sig"].(string)
	delete(params, "_sig")
	at := d.client(tc)
	d.cs.Call(at, method, params, RuleWait, func(o Outcome, t time.Time) {
		d.m.startDone(w, o)
		if sigID != "" {
			d.m.signalDone(w, w.SigByID[sigID], o, t)
		}
		if o.Outcome == "unknown" {
			assert.Sometimes(true, "a start returns an unknown outcome", map[string]any{"kind": o.ErrKind})
		}
	})
}

func (d *Driver) openCounters(extra func(*WF) bool) []*WF {
	return d.m.pick(func(w *WF) bool {
		return w.Type == "counter" && !w.Closed && w.StartState != StFailed && (extra == nil || extra(w))
	})
}

// RuleSignal sends an "add" signal to a counter workflow.
func (d *Driver) RuleSignal(tc hegel.TestCase) {
	if d.busy() {
		return
	}
	w, ok := pickOne(tc, d.openCounters(func(w *WF) bool { return len(w.Sigs) < MaxSignals }))
	if !ok {
		return
	}
	act, v := chance(tc, 0.15), hegel.Draw(tc, hegel.Integers(int64(1), 100))
	d.m.mu.Lock()
	current := d.m.wfs[w.WID] == w
	d.m.mu.Unlock()
	if !current {
		return // the ID was reused since this workflow was picked
	}
	s := d.m.newSig(w, act, v)
	d.cs.Call(d.client(tc), "signal", map[string]any{"wid": w.WID, "sig": map[string]any{"id": s.ID, "v": s.V}}, RuleWait,
		func(o Outcome, t time.Time) { d.m.signalDone(w, s, o, t) })
}

// RuleFinish asks a counter workflow to complete.
func (d *Driver) RuleFinish(tc hegel.TestCase) {
	if d.busy() {
		return
	}
	w, ok := pickOne(tc, d.openCounters(func(w *WF) bool { return len(w.Sigs) >= 3 || len(w.Sigs) >= MaxSignals }))
	if !ok || !chance(tc, 0.3) {
		return
	}
	d.m.mu.Lock()
	w.FinishSent = true
	d.m.mu.Unlock()
	d.cs.Call(d.client(tc), "signal", map[string]any{"wid": w.WID, "signal": "finish"}, RuleWait, nil)
}

// RuleQuery queries a counter's state: never a duplicate, never an unsent signal.
func (d *Driver) RuleQuery(tc hegel.TestCase) {
	if d.busy() {
		return
	}
	w, ok := pickOne(tc, d.openCounters(nil))
	if !ok {
		return
	}
	d.m.mu.Lock()
	run, reused := w.FirstRunID, w.PrevRunID != ""
	d.m.mu.Unlock()
	if reused && run == "" {
		return // can't tell this run from the previous one yet
	}
	d.cs.Call(d.client(tc), "query", map[string]any{"wid": w.WID, "run_id": run}, RuleWait, func(o Outcome, _ time.Time) {
		if o.Outcome != "ok" {
			return
		}
		var st CounterState
		if json.Unmarshal(o.Value, &st) != nil {
			return
		}
		d.m.mu.Lock()
		seen := map[string]bool{}
		var dups, unsent []string
		for _, id := range st.Applied {
			if seen[id] {
				dups = append(dups, id)
			}
			seen[id] = true
			if w.SigByID[id] == nil {
				unsent = append(unsent, id)
			}
		}
		d.m.mu.Unlock()
		det := map[string]any{"wid": w.WID, "dups": dups, "unsent": unsent, "n": len(st.Applied)}
		d.m.mu.Lock()
		wasReset := w.Resets > 0 || w.ResetsPending > 0
		d.m.mu.Unlock()
		if wasReset && len(unsent) == 0 {
			assert.Always(len(dups) == 0, "a signal is applied at most once in a workflow that was reset", det)
		} else {
			assert.Always(len(dups) == 0 && len(unsent) == 0, "a query shows each delivered signal once", det)
		}
		assert.Sometimes(len(st.Applied) > 0, "a query sees applied signals", nil)
	})
}

// RuleCancel requests cancellation of an open workflow.
func (d *Driver) RuleCancel(tc hegel.TestCase) {
	if d.busy() || !chance(tc, 0.15) {
		return
	}
	w, ok := pickOne(tc, d.m.pick(func(w *WF) bool { return !w.Closed && w.StartState != StFailed }))
	if !ok {
		return
	}
	d.m.mu.Lock()
	w.CancelSent = true
	d.m.mu.Unlock()
	d.cs.Call(d.client(tc), "cancel", map[string]any{"wid": w.WID}, RuleWait, nil)
}

// RuleTerminate terminates an open workflow.
func (d *Driver) RuleTerminate(tc hegel.TestCase) {
	if d.busy() || !chance(tc, 0.07) {
		return
	}
	w, ok := pickOne(tc, d.m.pick(func(w *WF) bool { return !w.Closed && w.StartState != StFailed }))
	if !ok {
		return
	}
	d.m.mu.Lock()
	w.TerminateSent = true
	d.m.mu.Unlock()
	d.cs.Call(d.client(tc), "terminate", map[string]any{"wid": w.WID}, RuleWait, nil)
}

// RuleReset resets a counter workflow to one of its completed decisions.
func (d *Driver) RuleReset(tc hegel.TestCase) {
	if d.busy() || !chance(tc, 0.2) {
		return
	}
	w, ok := pickOne(tc, d.openCounters(func(w *WF) bool {
		return w.StartState == StOK && !w.FinishSent && !w.CancelSent && !w.TerminateSent && w.ResetsPending == 0
	}))
	if !ok {
		return
	}
	d.m.mu.Lock()
	w.ResetsPending++
	d.m.mu.Unlock()
	pick := float64(hegel.Draw(tc, hegel.Integers(0, 999))) / 1000
	st, _ := d.cs.Call(d.client(tc), "reset", map[string]any{"wid": w.WID, "pick": pick}, RuleWait, func(o Outcome, _ time.Time) {
		d.m.mu.Lock()
		defer d.m.mu.Unlock()
		w.ResetsPending--
		w.Resets++ // any outcome: a "failed" reply can follow an internal retry of a reset that took effect
		var rv struct {
			Base string `json:"base_run_id"`
		}
		_ = json.Unmarshal(o.Value, &rv)
		if rv.Base != "" {
			if w.ResetBases == nil {
				w.ResetBases = map[string]bool{}
			}
			w.ResetBases[rv.Base] = true
		}
		if o.Outcome == "ok" {
			assert.Sometimes(true, "a workflow reset succeeds", nil)
		}
	})
	if st == NotSent {
		d.m.mu.Lock()
		w.ResetsPending--
		d.m.mu.Unlock()
	}
}

// RuleDuplicateStart starts an open workflow's ID again: Cadence must reject it.
func (d *Driver) RuleDuplicateStart(tc hegel.TestCase) {
	if d.busy() || !chance(tc, 0.3) {
		return
	}
	w, ok := pickOne(tc, d.openCounters(func(w *WF) bool {
		return w.StartState == StOK && !w.FinishSent && !w.CancelSent && !w.TerminateSent && w.Resets == 0 && w.ResetsPending == 0
	}))
	if !ok {
		return
	}
	reuse := hegel.Draw(tc, hegel.Integers(0, 2))
	params := map[string]any{"wid": w.WID, "type": "counter", "reuse": reuse, "exec_timeout_s": 7 * 24 * 3600,
		"input": map[string]any{"can_every": 0, "state": map[string]any{}}}
	d.m.mu.Lock()
	w.DupPending++
	d.m.mu.Unlock()
	st, _ := d.cs.Call(d.client(tc), "start", params, RuleWait, func(o Outcome, _ time.Time) {
		d.m.mu.Lock()
		w.DupPending--
		closing := w.FinishSent || w.CancelSent || w.TerminateSent || w.Closed
		d.m.mu.Unlock()
		if o.Outcome == "ok" {
			var v struct {
				RunID string `json:"run_id"`
			}
			_ = json.Unmarshal(o.Value, &v)
			d.m.mu.Lock()
			if w.Foreign == nil {
				w.Foreign = map[string]bool{}
			}
			w.Foreign[v.RunID] = true // a run this workflow's model doesn't own
			d.m.mu.Unlock()
		}
		if closing || d.stopping.Load() {
			return // it may have closed meanwhile; the check no longer applies
		}
		assert.Always(o.Outcome != "ok", "a second start of an open workflow ID is rejected",
			map[string]any{"wid": w.WID, "reuse": reuse, "value": string(o.Value)})
		assert.Sometimes(o.ErrKind == "already_started", "a duplicate start is rejected as already started", nil)
	})
	if st == NotSent {
		d.m.mu.Lock()
		w.DupPending--
		d.m.mu.Unlock()
	}
}

// RuleResult reads the final result of a workflow that should be finishing.
func (d *Driver) RuleResult(tc hegel.TestCase) {
	cands := d.m.pick(func(w *WF) bool {
		return !w.ResultInFlight && (w.Closed && !w.HistChecked || !w.Closed && w.StartState != StFailed &&
			(w.Type != "counter" || w.FinishSent || w.CancelSent || w.TerminateSent))
	})
	w, ok := pickOne(tc, cands)
	if !ok {
		return
	}
	d.readResult(d.client(tc), w, ResultWait)
}

func (d *Driver) readResult(client string, w *WF, wait time.Duration) Status {
	d.m.mu.Lock()
	if (w.PrevRunID != "" && w.FirstRunID == "") || w.ResetsPending > 0 {
		d.m.mu.Unlock()
		return NotSent
	}
	if w.ResultInFlight {
		d.m.mu.Unlock()
		return Pending
	}
	w.ResultInFlight = true
	d.m.mu.Unlock()
	d.m.mu.Lock()
	run := w.FirstRunID
	if w.Resets > 0 || w.ResetsPending > 0 {
		run = "" // a reset closes the first run as terminated; read the current run instead
	}
	params := map[string]any{"wid": w.WID, "timeout_ms": 3000, "run_id": run}
	d.m.mu.Unlock()
	st, _ := d.cs.Call(client, "result", params, wait, func(o Outcome, _ time.Time) {
		closed := d.m.resultDone(w, o)
		d.m.mu.Lock()
		w.ResultInFlight = false
		run, checked := w.ClosedRunID, w.HistChecked
		d.m.mu.Unlock()
		if closed && !checked && run == "" {
			// No run ID to read (e.g. canceled/terminated result): reading the current run
			// could reach a later run of a reused ID, so skip the history check.
			d.m.mu.Lock()
			w.HistChecked = true
			d.m.mu.Unlock()
			if !d.stopping.Load() {
				d.m.retire(w)
			}
		} else if closed && !checked {
			go d.cs.Call(client, "history", map[string]any{"wid": w.WID, "run_id": run}, 30*time.Second, func(h Outcome, _ time.Time) {
				d.m.historyDone(w, run, h)
				d.m.mu.Lock()
				done := w.HistChecked
				d.m.mu.Unlock()
				if done && !d.stopping.Load() {
					d.m.retire(w)
				}
			})
		}
	})
	if st == NotSent {
		d.m.mu.Lock()
		w.ResultInFlight = false
		d.m.mu.Unlock()
	}
	return st
}

// RuleDescribeClosed re-reads a closed run: it must still be closed with the same status.
func (d *Driver) RuleDescribeClosed(tc hegel.TestCase) {
	if d.busy() {
		return
	}
	w, ok := pickOne(tc, d.m.recentClosed())
	if !ok || w.ClosedRunID == "" {
		return
	}
	d.cs.Call(d.client(tc), "describe", map[string]any{"wid": w.WID, "run_id": w.ClosedRunID}, RuleWait, func(o Outcome, _ time.Time) {
		if o.Outcome != "ok" {
			return
		}
		var v struct {
			Closed bool   `json:"closed"`
			Status string `json:"close_status"`
		}
		_ = json.Unmarshal(o.Value, &v)
		want := map[string]string{"completed": "completed", "canceled": "canceled", "terminated": "terminated"}[w.CloseStatus]
		// A continue-as-new chain's first run is closed as continued_as_new.
		assert.Always(v.Closed && (want == "" || v.Status == want || v.Status == "continued_as_new"), "a closed workflow keeps its outcome",
			map[string]any{"wid": w.WID, "run": w.ClosedRunID, "closed": v.Closed, "status": v.Status, "want": w.CloseStatus})
	})
}

// RuleEventually, rarely: stop faults and check recovery, completion and visibility.
func (d *Driver) RuleEventually(tc hegel.TestCase) {
	if os.Getenv("EVENTUALLY_OFF") != "" || !chance(tc, 0.003) {
		return
	}
	d.eventually()
}

// ---------- eventually ----------

func (d *Driver) eventually() {
	if !d.evMu.TryLock() {
		return
	}
	d.stopping.Store(true)
	log.Printf("eventually: stopping faults")
	if sf := os.Getenv("ANTITHESIS_STOP_FAULTS"); sf != "" {
		if out, err := exec.Command(sf, "3600").CombinedOutput(); err != nil {
			log.Printf("stop faults: %v %s", err, out)
		}
	}
	start := time.Now()
	// 1. Every client serves real requests again (Guarantee 5).
	deadline := start.Add(3 * time.Minute)
	down := map[string]string{}
	for _, c := range d.names {
		for {
			st, o := d.cs.Call(c, "start", map[string]any{"wid": fmt.Sprintf("served-%s-%d", c, time.Now().UnixNano()), "type": "timer",
				"reuse": 1, "input": map[string]any{"ms": 10}}, 20*time.Second, nil)
			if st == Done && o.Outcome == "ok" {
				delete(down, c)
				break
			}
			down[c] = fmt.Sprintf("%d %s %s", st, o.ErrKind, o.Error)
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	log.Printf("eventually: assertions start (recovered after %v)", time.Since(start))
	assert.Always(len(down) == 0, "every client is served again once faults stop",
		map[string]any{"down": down, "outstanding": d.cs.Outstanding()})
	if len(down) > 0 {
		os.Exit(0)
	}
	// 2. A fresh workflow completes within 120 s (Guarantee 5).
	probe := fmt.Sprintf("probe-%d", time.Now().UnixNano())
	pstart := time.Now()
	probeOK := false
	started := false
	for time.Since(pstart) < 120*time.Second {
		if !started {
			st, o := d.cs.Call(d.names[0], "start", map[string]any{"wid": probe, "type": "chain", "reuse": 1, "input": map[string]any{"n": 2, "seed": 7}}, 30*time.Second, nil)
			started = st == Done && (o.Outcome == "ok" || o.ErrKind == "already_started")
			if !started {
				time.Sleep(time.Second)
				continue
			}
		}
		st, o := d.cs.Call(d.names[0], "result", map[string]any{"wid": probe, "timeout_ms": 10000}, 15*time.Second, nil)
		if st == Done && o.Outcome == "ok" && strings.Contains(string(o.Value), "completed") {
			probeOK = true
			break
		}
		time.Sleep(time.Second)
	}
	assert.Always(probeOK, "a new workflow completes within 2 minutes once faults stop",
		map[string]any{"took_s": time.Since(pstart).Seconds()})
	// 3. Every started workflow closes (Guarantee 4).
	wfs := d.m.pick(func(w *WF) bool { return true })
	for _, w := range wfs {
		d.m.mu.Lock()
		need := w.Type == "counter" && !w.Closed && w.StartState != StFailed
		d.m.mu.Unlock()
		if need {
			d.m.mu.Lock()
			w.FinishSent = true
			d.m.mu.Unlock()
			// finish is idempotent for our purposes; repeat until it lands or the workflow is gone
			for i := 0; i < 10; i++ {
				st, o := d.cs.Call(d.names[i%len(d.names)], "signal", map[string]any{"wid": w.WID, "signal": "finish"}, 20*time.Second, nil)
				if st == Done && (o.Outcome == "ok" || o.ErrKind == "not_exists" || o.ErrKind == "already_completed") {
					break
				}
				time.Sleep(time.Second)
			}
		}
	}
	deadline = time.Now().Add(5 * time.Minute)
	var stragglers []map[string]any
	for _, w := range wfs {
		tried := 0
		for {
			d.m.mu.Lock()
			closed, state := w.Closed, w.StartState
			d.m.mu.Unlock()
			if closed || state == StFailed {
				break
			}
			if state != StOK && w.PrevRunID != "" {
				break // a reused ID whose start never confirmed a run: can't tell its run from the previous one
			}
			if state != StOK {
				// Start outcome unknown: the workflow may not exist.
				st, o := d.cs.Call(d.names[0], "describe", map[string]any{"wid": w.WID}, 20*time.Second, nil)
				if st == Done && o.ErrKind == "not_exists" {
					break
				}
				var dv struct {
					RunID string `json:"run_id"`
				}
				_ = json.Unmarshal(o.Value, &dv)
				if st == Done && o.Outcome == "ok" && w.PrevRunID != "" && dv.RunID == w.PrevRunID {
					break // the reuse start never took effect
				}
			}
			if time.Now().After(deadline) && tried >= 3 {
				_, o := d.cs.Call(d.names[0], "describe", map[string]any{"wid": w.WID}, 20*time.Second, nil)
				d.m.mu.Lock()
				stragglers = append(stragglers, map[string]any{"wid": w.WID, "type": w.Type, "start": state, "describe": string(o.Value), "err": o.Error,
					"resets": w.Resets, "resets_pending": w.ResetsPending, "reset_bases": len(w.ResetBases), "foreign": len(w.Foreign),
					"prev_run": w.PrevRunID, "first_run": w.FirstRunID, "in_flight": w.ResultInFlight, "cancel": w.CancelSent, "terminate": w.TerminateSent})
				d.m.mu.Unlock()
				break
			}
			tried++
			d.readResult(d.names[tried%len(d.names)], w, 15*time.Second)
			time.Sleep(500 * time.Millisecond)
		}
	}
	if len(stragglers) > 10 {
		stragglers = stragglers[:10]
	}
	assert.Always(len(stragglers) == 0, "every started workflow closes once faults stop", map[string]any{"stragglers": stragglers})
	// 4. Visibility converges: every closed run is listed as closed with its status (Guarantee 6).
	closedWFs := append(d.m.recentClosed(), d.m.pick(func(w *WF) bool { return w.Closed })...)
	vdeadline := time.Now().Add(2 * time.Minute)
	var invisible []string
	for _, w := range closedWFs {
		if w.ClosedRunID == "" {
			continue
		}
		for {
			st, o := d.cs.Call(d.names[1%len(d.names)], "list", map[string]any{"wid": w.WID}, 20*time.Second, nil)
			found := false
			if st == Done && o.Outcome == "ok" {
				var v struct {
					Closed []map[string]string `json:"closed"`
				}
				_ = json.Unmarshal(o.Value, &v)
				for _, r := range v.Closed {
					if r["run_id"] == w.ClosedRunID {
						found = true
					}
				}
			}
			if found {
				break
			}
			if time.Now().After(vdeadline) {
				invisible = append(invisible, w.WID+"/"+w.ClosedRunID)
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	assert.Always(len(invisible) == 0, "a closed workflow appears in visibility once faults stop", map[string]any{"missing": invisible})
	log.Printf("eventually: ending the timeline")
	os.Exit(0)
}

// RuleReuseClosed starts a new run on the ID of a closed workflow with a drawn
// reuse policy: AllowDuplicateFailedOnly (0) allows it only after a run that did
// not complete successfully, AllowDuplicate (1) always, RejectDuplicate (2) never.
func (d *Driver) RuleReuseClosed(tc hegel.TestCase) {
	if d.busy() || d.m.openCount() >= MaxOpen || !chance(tc, 0.3) {
		return
	}
	cands := []*WF{}
	for _, w := range d.m.recentClosed() {
		d.m.mu.Lock()
		_, active := d.m.wfs[w.WID]
		ok := !active && !w.Reused && w.Type == "counter" && w.Resets == 0 && w.ResetsPending == 0 && w.ClosedRunID != "" &&
			w.DupPending == 0 && len(w.Foreign) == 0
		d.m.mu.Unlock()
		if ok {
			cands = append(cands, w)
		}
	}
	old, ok := pickOne(tc, cands)
	if !ok {
		return
	}
	policy := hegel.Draw(tc, hegel.Integers(0, 2))
	allowed := policy == 1 || (policy == 0 && old.CloseStatus != "completed")
	d.m.mu.Lock()
	old.Reused = true
	w := &WF{WID: old.WID, Type: "counter", RunIDs: map[string]bool{}, SigByID: map[string]*Sig{}, PrevRunID: old.ClosedRunID}
	d.m.wfs[w.WID] = w
	d.m.mu.Unlock()
	params := map[string]any{"wid": w.WID, "type": "counter", "reuse": policy, "exec_timeout_s": 7 * 24 * 3600,
		"input": map[string]any{"can_every": 0, "state": map[string]any{}}}
	d.cs.Call(d.client(tc), "start", params, RuleWait, func(o Outcome, _ time.Time) {
		d.m.startDone(w, o)
		det := map[string]any{"wid": w.WID, "policy": policy, "prev_status": old.CloseStatus, "outcome": o.Outcome, "kind": o.ErrKind}
		if o.Outcome == "ok" {
			assert.Always(allowed, "a start on a closed workflow ID follows its reuse policy", det)
			assert.Sometimes(true, "a closed workflow ID is reused", nil)
		}
		if o.ErrKind == "already_started" {
			assert.Always(!allowed, "a start on a closed workflow ID follows its reuse policy", det)
			assert.Sometimes(true, "a reuse policy rejects a start on a closed workflow ID", nil)
		}
		if o.Outcome == "failed" {
			d.m.mu.Lock()
			if w.StartState == StFailed && d.m.wfs[w.WID] == w {
				delete(d.m.wfs, w.WID)
			}
			d.m.mu.Unlock()
		}
	})
}
