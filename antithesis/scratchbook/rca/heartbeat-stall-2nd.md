# Second occurrence of retried-decision-heartbeat-stalls-workflow? (hunts)

**Verdict: not found in the hunts. The decision heartbeat timeouts seen in the hunts come from a different mechanism, although it shares the first step of the finding.**
Hunts searched: v1.4.5 f04f3907fc63a679646c90a9dc10c042-67-6, master-b c26baba5062af99fb01cbb7300de3bc6-67-6, v1.4.5-b bba4d2f05a517054ee304b6265bbc541-67-6
(history.decisionHeartbeatTimeout = 1m in all three). Raw material: `logs/heartbeat-stall-2nd/`.

## 1. What the emptyDecisionRun detector measured

Property "a workflow does not heartbeat decisions without progress" (driver/model.go:414-415, threshold 20).
I counted its evaluations with `snouty runs search ... --count` on `details.empty_cycles`:

| run | evaluations | empty_cycles >= 2 | empty_cycles >= 3 | max |
|---|---|---|---|---|
| f04f3907 (v1.4.5) | 9150 | 12 (all race-*) | 0 | **2** |
| c26baba5 (master-b) | n/a | 20 | 1 (race-1, n=20) | **3** |
| bba4d2f0 (v1.4.5-b) | n/a | 21 | 2 (race-8, race-2; n=20) | **3** |

A race workflow's normal local activity heartbeats give 2 to 3 empty cycles (a 7 s LA with a 4 s
decision timeout gives about 2 heartbeats). If a stall had been cut off by the 1m heartbeat timeout,
its history would show about 18 consecutive empty cycles (60 s / 3.2 s) and then a
DecisionTaskTimedOut. No checked history comes close. Caveat: the detector only sees histories that
the driver read after the workflow closed. In the timeline examined below, race-31 and race-38 were
never checked.

## 2. Decision heartbeat timeouts do occur in the hunts

The Go worker logs `error sending decision heartbeat EntityNotExistsError{Message: decision heartbeat timeout}`
(internal_worker_base.go:402). `--count` over the whole run tree: f04 3460, c26 2916, bba 2191.
These counts include shared prefixes and branches, so they are not distinct occurrences. The server
side is service/history/decision/handler.go:381-390 and 616-620: an empty forced heartbeat that
arrives more than decisionHeartbeatTimeout after the decision's OriginalScheduledTimestamp is
answered with DecisionTaskTimedOut plus EntityNotExists.

### Examined occurrence: f04f3907, moment -1758056358065479918 @ 181.0577 (client3, 20:52:53.199Z)

At that moment I fetched every race-1..40 history and described race-38 (debugging session
2ed301e3d0f12a8465e8028133dfe8a4-67-6, since cancelled). Only race-5, -12, -31 and -38 exist.
race-5, -12 and -31 completed normally (max empty cycles 1, 2 and 1). **race-38** is the workflow
that timed out:

- History (15 events, `logs/heartbeat-stall-2nd/race-38.json`): DT timed out at ev 4 (20:50:52),
  attempt 2 completed at ev 7 (20:50:59, startToClose 49 s), sticky DT 9/10 started inline, then
  START_TO_CLOSE timeouts at ev 11 (20:51:09) and ev 15 (20:51:16). After that, decision attempts
  are transient and are not written to history. **There are no empty DecisionTaskCompleted cycles
  at all.**
- Server logs: frontend `RespondDecisionTaskCompleted` for race-38 fails or times out at 20:52:00,
  :10, :20, :31 and :41. That is the Go client retrying the same heartbeat request every 10 s while
  shards move, cadence1 stops and postgres is unreachable (`f04-181.log`).
- At 20:52:53 a retry gets through. It arrives more than 1m after the transient decision was
  scheduled (attempt >= 2 decisions get a ~1m start-to-close, mutable_state_task_generator.go:701),
  so the server returns "decision heartbeat timeout". `wf desc` right afterwards shows
  `PendingDecision{STARTED, Attempt: 3, ScheduleID: 16, Scheduled/Started 20:52:53}`.
  The client evicts the cached context and replays, so the workflow is not stuck.

**Mechanism:** a heartbeat RPC is retried for more than 1m under faults, and its eventual success
lands after the heartbeat deadline. This is the same first step as the finding (the client retries
a decision-heartbeat RespondDecisionTaskCompleted), but the retry did not succeed in time with
ReturnNewDecisionTask=false. It hit the heartbeat timeout instead, which is exactly the recovery
path. It did not leave a cached context heartbeating empty decisions. This is correct
fault-tolerance behaviour, not a bug. (With the 30 min production default, the same retry would
have succeeded and could have entered the stall. The 1m setting cuts this path off.)

## 3. Conclusion

- No second occurrence of the stall (a long run of empty decision cycles ending in a heartbeat
  timeout) in any checked history of the three hunts. The detector's maximum is 3, against about
  18 expected for a stall bounded at 1m.
- The many "decision heartbeat timeout" client log lines are a different, benign mechanism:
  a heartbeat retry delayed past the 1m deadline by faults. One case was examined in full (f04
  race-38). Others in c26 and bba were not opened one by one.
- The finding stays single-occurrence: v1.4.5 iter-3 6e868ff1 race-1 (rca/race-workflow-stuck.md).
  To see it again, run with the default decisionHeartbeatTimeout (30m), or have the driver also run
  the empty-cycle check on stragglers (open workflows) at the end of the run.
