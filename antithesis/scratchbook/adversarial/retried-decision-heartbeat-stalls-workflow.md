Verdict: stands (as a Go client SDK bug in go.uber.org/cadence v1.4.0; NOT a Cadence server bug; bounded stall, not a permanent hang)

Finding: race-1 (run 6e868ff1a0ea33104c55aee1a6f6af93-67-6, v1.4.5) loops on empty decision heartbeats and never closes inside the driver's 5-min window.
Evidence used: scratchbook/rca/race-workflow-stuck.md, scratchbook/logs/race-stuck/history.json, client source in GOMODCACHE (checksum-verified, read-only; client/go.mod has no `replace`).

## Check 1: harness intervention between fault and symptom
- The driver does not touch race workflows after start: there are no signals, resets, or cancels for type "race" (driver/main.go:183-190 only builds input; driver/model.go:272-279 only checks the result).
- The history has no reset or signal events. Events 1-20 are pure DT/marker/timer events (history.json).
- The workflow code's LA-retry branch (client/workflows.go:315-327) never ran: there is no second marker for a round and no 1 s Sleep timer.
- Conclusion: there was no harness action between the fault (postgres DNS failure on cadence2, ~19:50:06-19:51:02) and the symptom. The defense fails.

## Check 2: configuration/tuning, and would it happen with Cadence defaults?
- The harness makes decision heartbeats likely. The decision timeout is 4 s (client/main.go:315, default 10 s at :630-633), and race LAs run up to 7 s (driver/main.go:183-190). An LA that outlives the decision timeout is a documented, supported use case: the client heartbeats with force-complete at 0.8×timeout (internal_task_handlers.go:834-871). So this is tuning toward a feature, not misuse.
- The trigger is any transient failure of a heartbeat RespondDecisionTaskCompleted that a later retry fixes. That happens in production too (DB blips, shard movement, frontend timeouts).
- dynamicconfig.yaml at the time did not shorten decisionHeartbeatTimeout. The current `history.decisionHeartbeatTimeout: 1m` (config-template/dynamicconfig.yaml, last entry) was added after this finding (speedrun-log.md:21). The other overrides (cache 20, 3 partitions, ack intervals) are unrelated to the client path.
- With defaults, the server ends the loop after `history.decisionHeartbeatTimeout` = 30m (src/cadence-v1.4.5/common/dynamicconfig/dynamicproperties/constants.go:3214-3219, 6021). The client then evicts and replays, and the LA runs again.
- Strongest partial defense: the checker's "never closes" is really "stalls up to 30 min with defaults". This holds, and it lowers severity. It does not remove the bug: the stall still occurs with defaults.

## Check 3: faults beyond what the guarantee promises
- The fault is a transient DNS/partition loss of postgres for about 1 min. That is within high_availability and within normal operating conditions.
- The symptom continues after the faults stop: about 93 heartbeats over 19:51:05-19:56:02, with no further RPC failures needed (the heartbeats succeed on the first try).
- Cadence's guarantee is that workflows make progress once the system is healthy. The fault only sets up the bad client state, and nothing beyond the guarantee was needed. The defense fails.

## Check 4: can the checker fire on documented behaviour?
- "every started workflow closes once faults stop" with a 5-min deadline (driver/main.go:601, 646) is stricter than anything Cadence documents. Cadence gives no latency bound, and decisionHeartbeatTimeout (30m) is the documented backstop.
- So the specific assertion firing is partly an artifact of the deadline (5 min < 30 min). The defense holds for the assertion as written.
- The underlying behaviour is still not documented or intended. A completed LA's result is silently dropped:
  - `processWorkflowTask` returns and closes doneCh (internal_task_pollers.go:355).
  - LA results are then discarded (internal_task_pollers.go:604-611).
  - The cached context is kept, because Unlock(nil) does not evict (internal_task_handlers.go:511-533).
  - CompleteDecisionTask never re-dispatches an LA that is already started (internal_task_handlers.go:1151-1173).
  - The workflow heartbeats empty decisions, a no-progress loop, until the server-side timeout.
- Root trigger: handleDecisionTaskCompletedRequest (internal_task_pollers.go:507-514) mutates the shared request on its first attempt (it sets StickyAttributes). Every backoff.Retry attempt (:453-462) therefore takes the `else` branch and sends ReturnNewDecisionTask=false.
  - This is a retry-path side effect. It is not a policy: that branch exists for disableStickyExecution.
  - The server honours the request (handler.go:500-535, 638), so it returns no DecisionTask.
  - The heartbeat path then treats a nil DecisionTask as "done" (internal_task_pollers.go:369-371; internal_task_handlers.go:857-859).
- History matches this exactly: event 9 has no inline DT, event 13 comes from a real sticky poll (requestId ea7642a0…), and events 15+ are empty heartbeats.

## Check 5: reproduction across timelines/runs/versions
- Observed once: one workflow in one v1.4.5 run. It was not reproduced on master, and no second instance is recorded in the analyses.
- The bug is in the client, so the server version is irrelevant. The same client v1.4.0 runs against both server versions.
- The code path is deterministic once the trigger happens: (heartbeat with an LA in flight) + (first respond attempt fails) + (a retry succeeds). I confirmed every step by reading the code, not only from the timeline.
- Weakness: there is no independent reproduction or a minimal unit test. A repro is cheap: a fake service that fails the first RespondDecisionTaskCompleted while an LA is running.
- I did not check whether a newer client release fixes it.

## Strongest defense and why it doesn't hold
"The workflow isn't stuck. Cadence's own decisionHeartbeatTimeout (30m) recovers it, and the driver's 5-min deadline is stricter than documented semantics."

This is true for the assertion's wording, and it caps severity. It does not explain away the defect, for three reasons:
- A transient error leads to a silently dropped LA result.
- The workflow then makes zero progress for up to 30 min after the system is healthy.
- The stall burns a decision every ~3.2 s, adding ~1,700 history events per 30 min.

All of this comes from a retry bug in the client: the shared request is mutated, so ReturnNewDecisionTask flips. The harness, the faults, and the server behaviour do not account for it.

## Severity to state to Uber engineers
Medium-low, against the Go client (go.uber.org/cadence v1.4.0), not the server:
- Bounded liveness stall, up to decisionHeartbeatTimeout (30m default), with no data loss. The LA re-executes after recovery, which fits at-least-once semantics.
- Reachable in production by any transient failure of a heartbeat RespondDecisionTaskCompleted while a local activity runs past the decision timeout.
- Fixes:
  - Compute the sticky/ReturnNewDecisionTask fields once, outside backoff.Retry, or clone the request per attempt.
  - Treat a heartbeat response with no DecisionTask as an error, or evict the cached context, so in-flight LAs are dispatched again.
