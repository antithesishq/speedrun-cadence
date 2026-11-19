package main

// Client program for the stateful-clients pattern: one long-lived Cadence Go
// client + worker per container. The driver sends JSON-RPC requests over
// /clients/<hostname>.sock; each request runs concurrently and is answered
// when it completes. Every library result becomes an outcome: ok, failed, or
// unknown. Checks and randomness live in the driver.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	adminv1 "github.com/uber/cadence-idl/go/proto/admin/v1"
	apiv1 "github.com/uber/cadence-idl/go/proto/api/v1"
	"go.uber.org/cadence"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/activity"
	"go.uber.org/cadence/client"
	"go.uber.org/cadence/compatibility"
	"go.uber.org/cadence/worker"
	"go.uber.org/cadence/workflow"
	"go.uber.org/yarpc"
	apipeer "go.uber.org/yarpc/api/peer"
	"go.uber.org/yarpc/peer"
	"go.uber.org/yarpc/peer/hostport"
	"go.uber.org/yarpc/peer/roundrobin"
	"go.uber.org/yarpc/transport/grpc"
	"go.uber.org/zap"
)

const Domain = "antithesis"

type Client struct {
	mu       sync.Mutex
	service  workflowserviceclient.Interface
	cc       client.Client
	dc       client.DomainClient
	worker   worker.Worker
	identity string
	admin    adminv1.AdminAPIYARPCClient // this cluster's admin API: reads served from this cluster's own store
}

func (c *Client) get() (workflowserviceclient.Interface, client.Client, client.DomainClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.service != nil {
		return c.service, c.cc, c.dc, nil
	}
	addrs := strings.Split(os.Getenv("CADENCE_FRONTENDS"), ",")
	t := grpc.NewTransport()
	list := roundrobin.New(t)
	ids := make([]apipeer.Identifier, len(addrs))
	for i, a := range addrs {
		ids[i] = hostport.PeerIdentifier(a)
	}
	out := t.NewOutbound(peer.Bind(list, peer.BindPeers(ids)))
	d := yarpc.NewDispatcher(yarpc.Config{
		Name:      "antithesis-client",
		Outbounds: yarpc.Outbounds{"cadence-frontend": {Unary: out}},
	})
	if err := d.Start(); err != nil {
		return nil, nil, nil, err
	}
	cfg := d.ClientConfig("cadence-frontend")
	c.admin = adminv1.NewAdminAPIYARPCClient(cfg)
	c.service = compatibility.NewThrift2ProtoAdapter(compatibility.AdapterClients{
		Domain:     apiv1.NewDomainAPIYARPCClient(cfg),
		Workflow:   apiv1.NewWorkflowAPIYARPCClient(cfg),
		Worker:     apiv1.NewWorkerAPIYARPCClient(cfg),
		Visibility: apiv1.NewVisibilityAPIYARPCClient(cfg),
	})
	c.cc = client.NewClient(c.service, Domain, &client.Options{Identity: c.identity})
	c.dc = client.NewDomainClient(c.service, &client.Options{Identity: c.identity})
	return c.service, c.cc, c.dc, nil
}

func (c *Client) startWorker() error {
	svc, _, _, err := c.get()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.worker != nil {
		return nil
	}
	logger, _ := zap.NewProduction()
	w := worker.New(svc, Domain, TaskList, worker.Options{Identity: c.identity, Logger: logger})
	w.RegisterWorkflowWithOptions(CounterWorkflow, workflow.RegisterOptions{Name: "counter"})
	w.RegisterWorkflowWithOptions(ChainWorkflow, workflow.RegisterOptions{Name: "chain"})
	w.RegisterWorkflowWithOptions(TimerWorkflow, workflow.RegisterOptions{Name: "timer"})
	w.RegisterWorkflowWithOptions(ParentWorkflow, workflow.RegisterOptions{Name: "parent"})
	w.RegisterWorkflowWithOptions(RaceWorkflow, workflow.RegisterOptions{Name: "race"})
	w.RegisterActivityWithOptions(SleepLocalActivity, activity.RegisterOptions{Name: "sleepla"})
	w.RegisterActivityWithOptions(StepActivity, activity.RegisterOptions{Name: "step"})
	w.RegisterActivityWithOptions(EchoActivity, activity.RegisterOptions{Name: "echo"})
	w.RegisterActivityWithOptions(LocalStepActivity, activity.RegisterOptions{Name: "localstep"})
	if err := w.Start(); err != nil {
		return err
	}
	c.worker = w
	return nil
}

// ---------- JSON-RPC ----------

type Req struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Result struct {
	Outcome string      `json:"outcome"`
	Value   interface{} `json:"value,omitempty"`
	Error   string      `json:"error,omitempty"`
	ErrKind string      `json:"err_kind,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func ok(v interface{}) Result { return Result{Outcome: "ok", Value: v} }

// classify maps a Cadence error to an outcome. Only errors that by contract mean
// "did not take effect and never will" are "failed"; the driver still gets the kind.
func classify(err error) Result {
	kind := "other"
	var (
		already  *s.WorkflowExecutionAlreadyStartedError
		notExist *s.EntityNotExistsError
		badReq   *s.BadRequestError
		done     *s.WorkflowExecutionAlreadyCompletedError
		qf       *s.QueryFailedError
		busy     *s.ServiceBusyError
		internal *s.InternalServiceError
		cancelRq *s.CancellationAlreadyRequestedError
		limit    *s.LimitExceededError
		domainEx *s.DomainAlreadyExistsError
	)
	outcome := "unknown"
	switch {
	case errors.As(err, &already):
		kind, outcome = "already_started", "failed"
	case errors.As(err, &notExist):
		kind, outcome = "not_exists", "failed"
	case errors.As(err, &done):
		kind, outcome = "already_completed", "failed"
	case errors.As(err, &badReq):
		kind, outcome = "bad_request", "failed"
	case errors.As(err, &qf):
		kind, outcome = "query_failed", "failed"
	case errors.As(err, &cancelRq):
		kind, outcome = "cancel_already_requested", "failed"
	case errors.As(err, &domainEx):
		kind, outcome = "domain_exists", "failed"
	case errors.As(err, &busy):
		kind = "busy"
	case errors.As(err, &limit):
		kind = "limit"
	case errors.As(err, &internal):
		kind = "internal"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline exceeded"):
		kind = "timeout"
	case strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "unavailable"):
		kind = "unavailable"
	}
	return Result{Outcome: outcome, Error: err.Error(), ErrKind: kind}
}

func main() {
	host, _ := os.Hostname()
	sock := "/clients/" + host + ".sock"
	// Handle SIGTERM before anything else, so a stop right after start exits cleanly.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigs; _ = os.Remove(sock); os.Exit(0) }()
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("listen %s: %v", sock, err)
	}
	log.Printf("client %s listening on %s", host, sock)
	c := &Client{identity: host}
	// Start the worker as soon as the domain exists, and after every restart of this process.
	go func() {
		for c.startWorker() != nil {
			time.Sleep(2 * time.Second)
		}
		log.Printf("worker started")
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go serve(c, conn)
	}
}

func serve(c *Client, conn net.Conn) {
	defer conn.Close()
	var wmu sync.Mutex
	write := func(v interface{}) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		conn.Write(append(b, '\n'))
		wmu.Unlock()
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r Req
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			write(map[string]interface{}{"jsonrpc": "2.0", "id": nil, "error": rpcErr{-32700, err.Error()}})
			continue
		}
		go func(r Req) {
			res, perr := dispatch(c, r.Method, r.Params)
			if perr != nil {
				write(map[string]interface{}{"jsonrpc": "2.0", "id": r.ID, "error": perr})
				return
			}
			if _, err := json.Marshal(res); err != nil {
				write(map[string]interface{}{"jsonrpc": "2.0", "id": r.ID, "error": rpcErr{-32603, err.Error()}})
				return
			}
			write(map[string]interface{}{"jsonrpc": "2.0", "id": r.ID, "result": res})
		}(r)
	}
}

// ---------- methods ----------

type P struct {
	WID            string          `json:"wid"`
	RunID          string          `json:"run_id"`
	Type           string          `json:"type"`
	Input          json.RawMessage `json:"input"`
	Reuse          int             `json:"reuse"`
	Sig            *AddSignal      `json:"sig"`
	Name           string          `json:"name"`
	TimeoutMs      int             `json:"timeout_ms"`
	Pick           float64         `json:"pick"`
	Reason         string          `json:"reason"`
	ExecTOSecs     int             `json:"exec_timeout_s"`
	Signal         string          `json:"signal"`
	DecisionTOSecs int             `json:"decision_timeout_s"`
	Cluster        string          `json:"cluster"`
	GracefulS      int             `json:"graceful_s"`
}

func dispatch(c *Client, method string, raw json.RawMessage) (res Result, perr *rpcErr) {
	var p P
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return Result{}, &rpcErr{-32602, err.Error()}
		}
	}
	if method == "health" {
		return ok(map[string]string{"cluster": os.Getenv("CADENCE_CLUSTER")}), nil
	}
	_, cc, dc, err := c.get()
	if err != nil {
		return classify(err), nil
	}
	timeout := 20 * time.Second
	if p.TimeoutMs > 0 {
		timeout = time.Duration(p.TimeoutMs) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	switch method {
	case "register_domain":
		days := int32(1)
		err := dc.Register(ctx, &s.RegisterDomainRequest{Name: strPtr(Domain), WorkflowExecutionRetentionPeriodInDays: &days})
		if err != nil {
			return classify(err), nil
		}
		return ok("registered"), nil
	case "register_global_domain":
		// A global domain replicated to both clusters, active in cluster0 (the primary cluster,
		// the only one that accepts a global domain registration).
		days := int32(1)
		global := true
		err := dc.Register(ctx, &s.RegisterDomainRequest{
			Name: strPtr(Domain), WorkflowExecutionRetentionPeriodInDays: &days,
			IsGlobalDomain:    &global,
			Clusters:          []*s.ClusterReplicationConfiguration{{ClusterName: strPtr("cluster0")}, {ClusterName: strPtr("cluster1")}},
			ActiveClusterName: strPtr("cluster0"),
		})
		if err != nil {
			return classify(err), nil
		}
		return ok("registered"), nil
	case "describe_domain":
		d, err := dc.Describe(ctx, Domain)
		if err != nil {
			return classify(err), nil
		}
		return ok(map[string]interface{}{
			"name":             d.DomainInfo.GetName(),
			"is_global":        d.GetIsGlobalDomain(),
			"active_cluster":   d.ReplicationConfiguration.GetActiveClusterName(),
			"failover_version": d.GetFailoverVersion(),
			"cluster":          os.Getenv("CADENCE_CLUSTER"),
		}), nil
	case "failover":
		// UpdateDomain with a new active cluster. graceful_s > 0 asks for a graceful failover
		// (Cadence requires it to be sent to the cluster becoming active); 0 is a force failover.
		if p.Cluster == "" {
			return Result{}, &rpcErr{-32602, "cluster required"}
		}
		req := &s.UpdateDomainRequest{Name: strPtr(Domain),
			ReplicationConfiguration: &s.DomainReplicationConfiguration{ActiveClusterName: strPtr(p.Cluster)}}
		if p.GracefulS > 0 {
			g := int32(p.GracefulS)
			req.FailoverTimeoutInSeconds = &g
		}
		if err := dc.Update(ctx, req); err != nil {
			return classify(err), nil
		}
		return ok(map[string]interface{}{"active_cluster": p.Cluster, "graceful_s": p.GracefulS}), nil
	case "raw_history":
		return rawHistory(ctx, c.admin, p), nil
	case "start_worker":
		if err := c.startWorker(); err != nil {
			return classify(err), nil
		}
		return ok("started"), nil
	case "start", "signal_with_start":
		fn, arg, perr := workflowArg(p.Type, p.Input)
		if perr != nil {
			return Result{}, perr
		}
		execTO := 2 * time.Hour
		if p.ExecTOSecs > 0 {
			execTO = time.Duration(p.ExecTOSecs) * time.Second
		}
		opts := client.StartWorkflowOptions{
			ID: p.WID, TaskList: TaskList,
			ExecutionStartToCloseTimeout:    execTO,
			DecisionTaskStartToCloseTimeout: decisionTimeout(p.DecisionTOSecs),
			WorkflowIDReusePolicy:           client.WorkflowIDReusePolicy(p.Reuse),
		}
		var we *workflow.Execution
		if method == "start" {
			we, err = cc.StartWorkflow(ctx, opts, fn, arg)
		} else {
			if p.Sig == nil {
				return Result{}, &rpcErr{-32602, "sig required"}
			}
			we, err = cc.SignalWithStartWorkflow(ctx, p.WID, "add", *p.Sig, opts, fn, arg)
		}
		if err != nil {
			return classify(err), nil
		}
		return ok(map[string]string{"run_id": we.RunID}), nil
	case "signal":
		name := p.Signal
		if name == "" {
			name = "add"
		}
		var arg interface{}
		if p.Sig != nil {
			arg = *p.Sig
		}
		if err := cc.SignalWorkflow(ctx, p.WID, p.RunID, name, arg); err != nil {
			return classify(err), nil
		}
		return ok("signaled"), nil
	case "cancel":
		if err := cc.CancelWorkflow(ctx, p.WID, p.RunID); err != nil {
			return classify(err), nil
		}
		return ok("cancel_requested"), nil
	case "terminate":
		if err := cc.TerminateWorkflow(ctx, p.WID, p.RunID, "antithesis", nil); err != nil {
			return classify(err), nil
		}
		return ok("terminated"), nil
	case "query":
		v, err := cc.QueryWorkflow(ctx, p.WID, p.RunID, "state")
		if err != nil {
			return classify(err), nil
		}
		var st CounterState
		if err := v.Get(&st); err != nil {
			return Result{Outcome: "unknown", Error: err.Error(), ErrKind: "decode"}, nil
		}
		return ok(st), nil
	case "result":
		return getResult(ctx, cc, p), nil
	case "describe":
		d, err := cc.DescribeWorkflowExecution(ctx, p.WID, p.RunID)
		if err != nil {
			return classify(err), nil
		}
		info := d.WorkflowExecutionInfo
		out := map[string]interface{}{
			"run_id":           info.Execution.GetRunId(),
			"closed":           info.CloseStatus != nil,
			"close_status":     closeStatus(info.CloseStatus),
			"history_length":   info.GetHistoryLength(),
			"pending_acts":     len(d.PendingActivities),
			"pending_children": len(d.PendingChildren),
		}
		return ok(out), nil
	case "history":
		return getHistory(ctx, cc, p), nil
	case "list":
		return listRuns(ctx, cc, p), nil
	case "reset":
		return reset(ctx, cc, p), nil
	}
	return Result{}, &rpcErr{-32601, "unknown method " + method}
}

func strPtr(v string) *string { return &v }

func closeStatus(cs *s.WorkflowExecutionCloseStatus) string {
	if cs == nil {
		return ""
	}
	return strings.ToLower(cs.String())
}

func workflowArg(t string, in json.RawMessage) (string, interface{}, *rpcErr) {
	var err error
	switch t {
	case "counter":
		var v CounterInput
		err = json.Unmarshal(in, &v)
		return t, v, perr(err)
	case "chain":
		var v ChainInput
		err = json.Unmarshal(in, &v)
		return t, v, perr(err)
	case "timer":
		var v TimerInput
		err = json.Unmarshal(in, &v)
		return t, v, perr(err)
	case "race":
		var v RaceInput
		err = json.Unmarshal(in, &v)
		return t, v, perr(err)
	case "parent":
		var v ParentInput
		err = json.Unmarshal(in, &v)
		return t, v, perr(err)
	}
	return "", nil, &rpcErr{-32602, "unknown workflow type " + t}
}

func perr(err error) *rpcErr {
	if err != nil {
		return &rpcErr{-32602, err.Error()}
	}
	return nil
}

// getResult waits (up to the request timeout) for the workflow's final result,
// following continue-as-new. Status is completed, canceled, terminated, failed,
// timed_out, or pending (deadline hit; unknown).
func getResult(ctx context.Context, cc client.Client, p P) Result {
	if p.RunID == "" {
		// Pin the read to the current run, so the reply always names the run it describes.
		d, err := cc.DescribeWorkflowExecution(ctx, p.WID, "")
		if err != nil {
			return classify(err)
		}
		p.RunID = d.WorkflowExecutionInfo.Execution.GetRunId()
	}
	res := getResultOfRun(ctx, cc, p)
	if m, ok := res.Value.(map[string]interface{}); ok && m["run_id"] == nil {
		m["run_id"] = p.RunID
	}
	return res
}

func getResultOfRun(ctx context.Context, cc client.Client, p P) Result {
	run := cc.GetWorkflow(ctx, p.WID, p.RunID)
	var raw json.RawMessage
	err := run.Get(ctx, &raw)
	if err == nil {
		rid := run.GetRunID()
		if rid == "" {
			if d, derr := cc.DescribeWorkflowExecution(ctx, p.WID, ""); derr == nil && d.WorkflowExecutionInfo.CloseStatus != nil {
				rid = d.WorkflowExecutionInfo.Execution.GetRunId()
			}
		}
		return ok(map[string]interface{}{"status": "completed", "result": raw, "run_id": rid})
	}
	var canceled *cadence.CanceledError
	var term *workflow.TerminatedError
	var tout *workflow.TimeoutError
	var gerr *workflow.GenericError
	var cerr *cadence.CustomError
	switch {
	case errors.As(err, &canceled):
		var st json.RawMessage
		_ = canceled.Details(&st)
		return ok(map[string]interface{}{"status": "canceled", "result": st})
	case errors.As(err, &term):
		return ok(map[string]interface{}{"status": "terminated"})
	case errors.As(err, &tout):
		return ok(map[string]interface{}{"status": "timed_out", "error": err.Error()})
	case errors.As(err, &cerr):
		return ok(map[string]interface{}{"status": "failed", "error": err.Error()})
	case errors.As(err, &gerr):
		return ok(map[string]interface{}{"status": "failed", "error": err.Error()})
	}
	r := classify(err)
	if ctx.Err() != nil {
		r.ErrKind = "pending"
	}
	return r
}

type Ev struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
	Ts   int64  `json:"ts"`
	Sig  string `json:"sig,omitempty"`  // signal payload ID for WorkflowExecutionSignaled
	Ref  int64  `json:"ref,omitempty"`  // scheduled/started event the event refers to
	Name string `json:"name,omitempty"` // signal name, timer ID, activity ID
}

func getHistory(ctx context.Context, cc client.Client, p P) Result {
	it := cc.GetWorkflowHistory(ctx, p.WID, p.RunID, false, s.HistoryEventFilterTypeAllEvent)
	var evs []Ev
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			return classify(err)
		}
		ev := Ev{ID: e.GetEventId(), Type: e.GetEventType().String(), Ts: e.GetTimestamp() / 1e6}
		switch e.GetEventType() {
		case s.EventTypeWorkflowExecutionSignaled:
			a := e.WorkflowExecutionSignaledEventAttributes
			ev.Name = a.GetSignalName()
			var sig AddSignal
			if json.Unmarshal(a.Input, &sig) == nil {
				ev.Sig = sig.ID
			}
		case s.EventTypeActivityTaskScheduled:
			ev.Name = e.ActivityTaskScheduledEventAttributes.GetActivityId()
		case s.EventTypeActivityTaskStarted:
			ev.Ref = e.ActivityTaskStartedEventAttributes.GetScheduledEventId()
		case s.EventTypeActivityTaskCompleted:
			ev.Ref = e.ActivityTaskCompletedEventAttributes.GetScheduledEventId()
		case s.EventTypeActivityTaskFailed:
			ev.Ref = e.ActivityTaskFailedEventAttributes.GetScheduledEventId()
		case s.EventTypeActivityTaskTimedOut:
			ev.Ref = e.ActivityTaskTimedOutEventAttributes.GetScheduledEventId()
		case s.EventTypeActivityTaskCanceled:
			ev.Ref = e.ActivityTaskCanceledEventAttributes.GetScheduledEventId()
		case s.EventTypeTimerStarted:
			ev.Name = e.TimerStartedEventAttributes.GetTimerId()
		case s.EventTypeTimerCanceled:
			ev.Name = e.TimerCanceledEventAttributes.GetTimerId()
			ev.Ref = e.TimerCanceledEventAttributes.GetStartedEventId()
		case s.EventTypeChildWorkflowExecutionFailed:
			ev.Ref = e.ChildWorkflowExecutionFailedEventAttributes.GetInitiatedEventId()
		case s.EventTypeChildWorkflowExecutionCanceled:
			ev.Ref = e.ChildWorkflowExecutionCanceledEventAttributes.GetInitiatedEventId()
		case s.EventTypeChildWorkflowExecutionTerminated:
			ev.Ref = e.ChildWorkflowExecutionTerminatedEventAttributes.GetInitiatedEventId()
		case s.EventTypeChildWorkflowExecutionTimedOut:
			ev.Ref = e.ChildWorkflowExecutionTimedOutEventAttributes.GetInitiatedEventId()
		case s.EventTypeTimerFired:
			ev.Name = e.TimerFiredEventAttributes.GetTimerId()
			ev.Ref = e.TimerFiredEventAttributes.GetStartedEventId()
		case s.EventTypeDecisionTaskStarted:
			ev.Ref = e.DecisionTaskStartedEventAttributes.GetScheduledEventId()
		case s.EventTypeDecisionTaskCompleted:
			ev.Ref = e.DecisionTaskCompletedEventAttributes.GetScheduledEventId()
		case s.EventTypeDecisionTaskTimedOut:
			ev.Ref = e.DecisionTaskTimedOutEventAttributes.GetScheduledEventId()
		case s.EventTypeDecisionTaskFailed:
			ev.Ref = e.DecisionTaskFailedEventAttributes.GetScheduledEventId()
		case s.EventTypeChildWorkflowExecutionStarted:
			ev.Ref = e.ChildWorkflowExecutionStartedEventAttributes.GetInitiatedEventId()
		case s.EventTypeChildWorkflowExecutionCompleted:
			ev.Ref = e.ChildWorkflowExecutionCompletedEventAttributes.GetInitiatedEventId()
		case s.EventTypeStartChildWorkflowExecutionInitiated:
			ev.Name = e.StartChildWorkflowExecutionInitiatedEventAttributes.GetWorkflowId()
		}
		evs = append(evs, ev)
	}
	return ok(evs)
}

func listRuns(ctx context.Context, cc client.Client, p P) Result {
	earliest, latest := int64(0), time.Now().Add(24*time.Hour).UnixNano()
	tf := &s.StartTimeFilter{EarliestTime: &earliest, LatestTime: &latest}
	ef := &s.WorkflowExecutionFilter{WorkflowId: &p.WID}
	dom := Domain
	open, err := cc.ListOpenWorkflow(ctx, &s.ListOpenWorkflowExecutionsRequest{Domain: &dom, StartTimeFilter: tf, ExecutionFilter: ef})
	if err != nil {
		return classify(err)
	}
	closed, err := cc.ListClosedWorkflow(ctx, &s.ListClosedWorkflowExecutionsRequest{Domain: &dom, StartTimeFilter: tf, ExecutionFilter: ef})
	if err != nil {
		return classify(err)
	}
	var o, cl []map[string]string
	for _, e := range open.Executions {
		o = append(o, map[string]string{"run_id": e.Execution.GetRunId()})
	}
	for _, e := range closed.Executions {
		cl = append(cl, map[string]string{"run_id": e.Execution.GetRunId(), "status": closeStatus(e.CloseStatus)})
	}
	return ok(map[string]interface{}{"open": o, "closed": cl})
}

// reset resets the current run to one of its DecisionTaskCompleted events
// (chosen by p.Pick in [0,1)); signals after that point are reapplied.
func reset(ctx context.Context, cc client.Client, p P) Result {
	d, err := cc.DescribeWorkflowExecution(ctx, p.WID, "")
	if err != nil {
		return classify(err)
	}
	runID := d.WorkflowExecutionInfo.Execution.GetRunId()
	it := cc.GetWorkflowHistory(ctx, p.WID, runID, false, s.HistoryEventFilterTypeAllEvent)
	var points []int64
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			return classify(err)
		}
		if e.GetEventType() == s.EventTypeDecisionTaskCompleted {
			points = append(points, e.GetEventId())
		}
	}
	if len(points) == 0 {
		return Result{Outcome: "failed", ErrKind: "no_reset_point", Error: "no completed decision"}
	}
	pt := points[int(p.Pick*float64(len(points)))%len(points)]
	dom := Domain
	reqID := fmt.Sprintf("%s-%s-%d", p.WID, runID, pt)
	reason := "antithesis-reset"
	r, err := cc.ResetWorkflow(ctx, &s.ResetWorkflowExecutionRequest{
		Domain:                &dom,
		WorkflowExecution:     &s.WorkflowExecution{WorkflowId: &p.WID, RunId: &runID},
		Reason:                &reason,
		DecisionFinishEventId: &pt,
		RequestId:             &reqID,
	})
	if err != nil {
		res := classify(err)
		res.Value = map[string]interface{}{"base_run_id": runID, "point": pt}
		return res
	}
	return ok(map[string]interface{}{"run_id": r.GetRunId(), "base_run_id": runID, "point": pt})
}

func decisionTimeout(secs int) time.Duration {
	if secs <= 0 {
		return 10 * time.Second
	}
	return time.Duration(secs) * time.Second
}
