package main

// Workflows and activities run by the worker in every client container.
// They are deterministic, and every result is a pure function of the inputs
// the driver chose and the signals Cadence delivered, so the driver can check
// each result against its model.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/cadence"
	"go.uber.org/cadence/activity"
	"go.uber.org/cadence/workflow"
)

const TaskList = "tl-main"

// ---------- shared options ----------

func activityCtx(ctx workflow.Context, heartbeat bool) workflow.Context {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Hour,
		StartToCloseTimeout:    15 * time.Second,
		RetryPolicy: &cadence.RetryPolicy{
			InitialInterval:          200 * time.Millisecond,
			BackoffCoefficient:       2,
			MaximumInterval:          3 * time.Second,
			ExpirationInterval:       2 * time.Hour,
			NonRetriableErrorReasons: []string{"nonretryable"},
		},
	}
	if heartbeat {
		ao.HeartbeatTimeout = 3 * time.Second
	}
	return workflow.WithActivityOptions(ctx, ao)
}

// ---------- counter: signals, queries, continue-as-new, cancellation ----------

type AddSignal struct {
	ID string `json:"id"`
	V  int64  `json:"v"`
}

type CounterState struct {
	Applied []string `json:"applied"`
	Sum     int64    `json:"sum"`
	Runs    int      `json:"runs"`
	// Acts counts activity results folded into the state (one per "act" signal).
	Acts int64 `json:"acts"`
	// ActErrs records activity errors (never expected: activities retry for 2 hours).
	ActErrs []string `json:"act_errs,omitempty"`
}

type CounterInput struct {
	State    CounterState `json:"state"`
	CanEvery int          `json:"can_every"`
}

// CounterWorkflow applies every "add" signal exactly once, in the order Cadence
// delivers them, until it gets "finish". Every CanEvery applied signals it
// continues as new, carrying its state. A signal whose ID starts with "act-"
// also runs an activity whose result is folded into Acts.
func CounterWorkflow(ctx workflow.Context, in CounterInput) (CounterState, error) {
	st := in.State
	st.Runs++
	if err := workflow.SetQueryHandler(ctx, "state", func() (CounterState, error) { return st, nil }); err != nil {
		return st, err
	}
	addCh := workflow.GetSignalChannel(ctx, "add")
	finCh := workflow.GetSignalChannel(ctx, "finish")
	applied := 0
	actCtx := activityCtx(ctx, false)
	apply := func(s AddSignal) {
		st.Applied = append(st.Applied, s.ID)
		st.Sum += s.V
		applied++
		if len(s.ID) > 4 && s.ID[:4] == "act-" {
			var r int64
			if err := workflow.ExecuteActivity(actCtx, EchoActivity, s.V).Get(ctx, &r); err == nil {
				st.Acts += r
			} else {
				st.Acts -= 1_000_000 // visible in the result, never expected
				st.ActErrs = append(st.ActErrs, s.ID+": "+err.Error())
			}
		}
	}
	drain := func() {
		for {
			var s AddSignal
			if !addCh.ReceiveAsync(&s) {
				return
			}
			apply(s)
		}
	}
	finished, canceled := false, false
	for !finished && !canceled {
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(addCh, func(c workflow.Channel, more bool) {
			var s AddSignal
			c.Receive(ctx, &s)
			apply(s)
		})
		sel.AddReceive(finCh, func(c workflow.Channel, more bool) {
			c.Receive(ctx, nil)
			finished = true
		})
		sel.AddReceive(ctx.Done(), func(c workflow.Channel, more bool) { canceled = true })
		sel.Select(ctx)
		if !finished && !canceled && in.CanEvery > 0 && applied >= in.CanEvery {
			drain()
			if finCh.ReceiveAsync(nil) {
				finished = true
				break
			}
			return CounterState{}, workflow.NewContinueAsNewError(ctx, CounterWorkflow, CounterInput{State: st, CanEvery: in.CanEvery})
		}
	}
	drain()
	if canceled {
		return st, ctx.Err()
	}
	return st, nil
}

func EchoActivity(ctx context.Context, v int64) (int64, error) {
	return v*3 + 1, nil
}

// ---------- chain: activities with retries, heartbeats, local activities ----------

type ChainInput struct {
	N            int   `json:"n"`
	Parallel     bool  `json:"parallel"`
	Seed         int64 `json:"seed"`
	FailAttempts int   `json:"fail_attempts"` // each step fails this many attempts first
	SleepMs      int   `json:"sleep_ms"`      // each step sleeps (heartbeating) this long
	Local        bool  `json:"local"`
}

func StepValue(seed int64, i int) int64 { return seed*1000 + int64(i*i) }

func ChainExpected(in ChainInput) int64 {
	var sum int64
	for i := 0; i < in.N; i++ {
		sum += StepValue(in.Seed, i)
	}
	return sum
}

func ChainWorkflow(ctx workflow.Context, in ChainInput) (int64, error) {
	var sum int64
	if in.Local {
		lctx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
			ScheduleToCloseTimeout: 10 * time.Second,
			RetryPolicy: &cadence.RetryPolicy{InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 2,
				MaximumInterval: time.Second, ExpirationInterval: 10 * time.Second},
		})
		for i := 0; i < in.N; i++ {
			var r int64
			for workflow.ExecuteLocalActivity(lctx, LocalStepActivity, in.Seed, i).Get(ctx, &r) != nil {
				if err := workflow.Sleep(ctx, time.Second); err != nil {
					return 0, err
				}
			}
			sum += r
		}
		return sum, nil
	}
	actx := activityCtx(ctx, in.SleepMs > 0)
	if in.Parallel {
		fs := make([]workflow.Future, in.N)
		for i := 0; i < in.N; i++ {
			fs[i] = workflow.ExecuteActivity(actx, StepActivity, in.Seed, i, in.FailAttempts, in.SleepMs)
		}
		for _, f := range fs {
			var r int64
			if err := f.Get(ctx, &r); err != nil {
				return 0, err
			}
			sum += r
		}
		return sum, nil
	}
	for i := 0; i < in.N; i++ {
		var r int64
		if err := workflow.ExecuteActivity(actx, StepActivity, in.Seed, i, in.FailAttempts, in.SleepMs).Get(ctx, &r); err != nil {
			return 0, err
		}
		sum += r
	}
	return sum, nil
}

func StepActivity(ctx context.Context, seed int64, i int, failAttempts int, sleepMs int) (int64, error) {
	info := activity.GetInfo(ctx)
	if int(info.Attempt) < failAttempts {
		return 0, errors.New("planned failure")
	}
	if sleepMs > 0 {
		deadline := time.Now().Add(time.Duration(sleepMs) * time.Millisecond)
		for time.Now().Before(deadline) {
			activity.RecordHeartbeat(ctx, i)
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return StepValue(seed, i), nil
}

func LocalStepActivity(ctx context.Context, seed int64, i int) (int64, error) {
	return StepValue(seed, i), nil
}

// ---------- timer ----------

type TimerInput struct {
	Ms int `json:"ms"`
}

type TimerResult struct {
	T0 int64 `json:"t0"` // workflow.Now before the sleep, unix ms
	T1 int64 `json:"t1"` // workflow.Now after the sleep, unix ms
}

func TimerWorkflow(ctx workflow.Context, in TimerInput) (TimerResult, error) {
	t0 := workflow.Now(ctx)
	if err := workflow.Sleep(ctx, time.Duration(in.Ms)*time.Millisecond); err != nil {
		return TimerResult{T0: t0.UnixMilli()}, err
	}
	t1 := workflow.Now(ctx)
	return TimerResult{T0: t0.UnixMilli(), T1: t1.UnixMilli()}, nil
}

// ---------- parent: child workflows ----------

type ParentInput struct {
	K    int   `json:"k"`
	Seed int64 `json:"seed"`
}

func ParentExpected(in ParentInput) int64 {
	var sum int64
	for i := 0; i < in.K; i++ {
		sum += ChainExpected(childInput(in, i))
	}
	return sum
}

func childInput(in ParentInput, i int) ChainInput {
	return ChainInput{N: 2, Seed: in.Seed + int64(i), FailAttempts: i % 2}
}

func ParentWorkflow(ctx workflow.Context, in ParentInput) (int64, error) {
	wid := workflow.GetInfo(ctx).WorkflowExecution.ID
	fs := make([]workflow.ChildWorkflowFuture, in.K)
	for i := 0; i < in.K; i++ {
		cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:                   fmt.Sprintf("%s/c%d", wid, i),
			TaskList:                     TaskList,
			ExecutionStartToCloseTimeout: 2 * time.Hour,
			TaskStartToCloseTimeout:      10 * time.Second,
		})
		fs[i] = workflow.ExecuteChildWorkflow(cctx, ChainWorkflow, childInput(in, i))
	}
	var sum int64
	for _, f := range fs {
		var r int64
		if err := f.Get(ctx, &r); err != nil {
			return 0, err
		}
		sum += r
	}
	return sum, nil
}

// ---------- race: timers racing long local activities (decision heartbeats) ----------

type RaceInput struct {
	Rounds []RaceRound `json:"rounds"`
}

type RaceRound struct {
	TimerMs int `json:"timer_ms"` // user timer
	LAMs    int `json:"la_ms"`    // local activity duration; > ~80% of the decision timeout forces a decision heartbeat
}

type RaceResult struct {
	Fired    int `json:"fired"`
	Canceled int `json:"canceled"`
	Rounds   int `json:"rounds"`
}

// RaceWorkflow, each round: start a timer and a local activity together, wait
// for the local activity, then cancel the timer if it hasn't fired yet. Which
// one wins is up to timing; the result counts how each round ended.
func RaceWorkflow(ctx workflow.Context, in RaceInput) (RaceResult, error) {
	var res RaceResult
	lctx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &cadence.RetryPolicy{InitialInterval: 200 * time.Millisecond, BackoffCoefficient: 2,
			MaximumInterval: 2 * time.Second, ExpirationInterval: 2 * time.Minute},
	})
	for _, r := range in.Rounds {
		tctx, cancelTimer := workflow.WithCancel(ctx)
		timer := workflow.NewTimer(tctx, time.Duration(r.TimerMs)*time.Millisecond)
		la := workflow.ExecuteLocalActivity(lctx, SleepLocalActivity, r.LAMs)
		for {
			if err := la.Get(ctx, nil); err == nil {
				break
			}
			if ctx.Err() != nil {
				return res, ctx.Err() // canceled
			}
			// A local activity that timed out (e.g. paused worker) is retried by the workflow.
			if err := workflow.Sleep(ctx, time.Second); err != nil {
				return res, err
			}
			la = workflow.ExecuteLocalActivity(lctx, SleepLocalActivity, r.LAMs)
		}
		if timer.IsReady() {
			res.Fired++
		} else {
			cancelTimer()
			if err := timer.Get(ctx, nil); err == nil {
				res.Fired++ // fired in the same decision as the cancel
			} else {
				res.Canceled++
			}
		}
		res.Rounds++
	}
	return res, nil
}

func SleepLocalActivity(ctx context.Context, ms int) error {
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
