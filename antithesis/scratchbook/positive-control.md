# Positive control: a canceled timer is also recorded as fired after decision heartbeating

Status: research only. The candidate is chosen on paper; nothing has been built yet.

- **Public reference:** [PR #8595, "fix(history): Fix duplicate timer resolution in decision heartbeating"][pr8595]
  (merged 2026-09-30). Commit message: "If that was the only buffered event then we no longer call
  FlushBufferedEvents. This leaves the timer fired event in the DB and when we later reload the
  mutable state we end up resurrecting the event. Then a subsequent transaction will end up
  flushing it, and we get duplicate resolution of the timer (both canceled and fired)."
- **Violates:** [Guarantee 3](guarantees.md#guarantee-3) (history is well-formed): "every started
  timer / scheduled activity / initiated child has at most one resolution event".
- **Trigger:** a user timer fires (timer queue) while a decision task is in flight, so
  `TimerFired` is written to the DB as a buffered event. The same decision then cancels that
  timer and completes as a **decision heartbeat** (`ForceCreateNewDecisionTask`, which the Go SDK
  sends when local activities run past about 80% of the decision timeout). The heartbeat removes the
  fired event only in memory. Later the mutable state is **reloaded from the DB** (history host
  kill or pause, shard movement, or cache eviction), and the stale buffered `TimerFired` is
  flushed. Needs concurrency (timer firing during a decision) plus a crash or ownership change. No
  scripted sequence is needed: a generic workload that races timers against local activities and
  cancels the loser hits it.
- **Expected symptom:** in `GetWorkflowExecutionHistory`, one `TimerStarted` event (ID S) is
  followed by both `TimerCanceled{StartedEventId=S}` and `TimerFired{StartedEventId=S}`, with
  `TimerFired` appearing after `TimerCanceled`. The worker usually also reports non-determinism
  on the next decision, so the run may stall, which also shows up under
  [Guarantee 4](guarantees.md#guarantee-4).
- **Verified from the diff (`git show 976d08c25`):** in
  `service/history/execution/mutable_state_decision_task_manager.go`,
  `AddDecisionTaskScheduledEventAsHeartbeat` changes `if m.msb.HasBufferedEvents() {` to
  `if m.msb.HasBufferedEvents() || m.msb.hasBufferedEventsInDB {`. At the pre-fix ref,
  `mutable_state_builder.go` sets `hasBufferedEventsInDB = len(e.bufferedEvents) > 0` after each
  transaction (line 1684), so once the in-memory copy is cleared the DB copy is forgotten until a
  reload. The guard dates to 2020 (`830974dd9`).
- **Refs:** `prefix_ref` = `c889f8319b9dffa8c8638c6679e8961070e0536d` (fix^, 2026-09-30),
  `fix_ref` = `976d08c25bbbb08b6c18023139454bc5eed2476b` ([fix PR][pr8595]). Alternative
  `prefix_ref`: the last affected release, `v1.4.2` (2026-09-09).
- **Affected releases:** every release through `v1.4.2`. First fixed: `v1.4.3-prerelease03`, so
  `v1.4.5` and master contain the fix.
- **Same harness?** Yes. `prefix_ref` is 51 commits before `v1.4.5`. Between them, `schema/postgres`,
  `config/` and the docker config templates are identical; only `go.mod` (Go 1.25.14 rather
  than 1.26) and the Dockerfile base image differ. Option 2 also works: revert the one-line fix on
  `v1.4.5`, which applies cleanly because line 418 is unchanged there.
- **Build:** not yet attempted. Toolchain `golang:1.25.14-alpine3.23` (from the ref's Dockerfile).
- **Fault needs:** process kill or pause of history (or partition causing shard movement); all
  default Antithesis faults.

## Criteria

| | Criterion | Evidence |
|---|---|---|
| a | Violates a guarantee as written | Duplicate resolution of one timer breaks [Guarantee 3](guarantees.md#guarantee-3) word for word |
| b | Needs faults/concurrency | It needs a timer firing during an in-flight decision (a race) and a mutable state reload (crash, shard move or eviction) |
| c | Public and recognizable | Upstream PR with a full write-up. It belongs to a family of "duplicate buffered events cause non-determinism" fixes ([#6966][pr6966], [#7400][pr7400], [#8163][pr8163]) whose root cause was "still unknown" |
| d | Cheap to build | Pre-fix is 3 weeks before `v1.4.5`, with the same Postgres schema and config (see "Same harness?" above) |

## Other candidates considered (ranked)

1. **Parent never learns that its child completed.** [PR #6871][pr6871]; fix
   `6076ce66856a013c63e838eaf4b274bdda3dc7ed`, pre-fix `b1e514f8f35b9d7d1b4f4d28ca3d9b6474d46804`
   (v1.3.0 → v1.3.1). Diff: `RecordChildExecutionCompleted` returned non-retryable
   `EntityNotExistsError("Pending child execution not started")` when the parent's (possibly
   stale) mutable state had not yet recorded the child as started, so the child's close transfer
   task was dropped. The fix returns retryable `ErrStaleState`. Trigger: shard movement of the
   parent, or a child that finishes before the parent records `ChildWorkflowExecutionStarted`
   (history pause or latency). Symptom: the parent waits forever although the child is closed,
   which breaks [Guarantee 4](guarantees.md#guarantee-4). It is a strong second choice, but the
   pre-fix ref is about 1.5 years older: the Postgres schema is v0.6 rather than v0.8 and
   visibility v0.8 rather than v0.9, so it needs that ref's own schema setup. The one-line revert
   on `v1.4.5` is also viable (the fixed branch is unchanged).
2. **A retried signal-with-start delivers the signal twice.** [PR #5671][pr5671]; fix
   `2965d8188da224dfb9b3e1019764e8f163a5470a`, pre-fix `9a1c2edb9c13ae4d58b51457e32fee9335e4bf85`
   (v1.2.7 → v1.2.8). Diff: the signal-with-start path on a running workflow checked
   `IsSignalRequested(requestID)` but never called `AddSignalRequested`, so the same request ID
   retried after a lost response appended a second `WorkflowExecutionSignaled`. Breaks
   [Guarantee 2](guarantees.md#guarantee-2). The trigger is simple (a partition or latency that
   causes a client retry) and very recognizable. It was not chosen because the release is from
   2024-02, with an older schema and Go toolchain. A revert on `v1.4.5` is possible
   (`start_workflow_execution.go:591`).
3. **Cleanup of a failed start deletes the history of a run that did start.** [PR #7617][pr7617]
   (follow-up to [#7590][pr7590]); fix `949af72789041e6bcafa28dfb2e7e02114dc33cb`, pre-fix
   `df2180b614ea4f92d3f3cffa180a0f11dafcd522` (v1.3.6 → v1.4.0). Diff: before the fix,
   `handleCreateWorkflowExecutionFailureCleanup` called `DeleteHistoryBranch` on any error,
   including timeouts and unknown DB errors where the execution row may have committed. A client
   retry with the same request ID then gets the run ID of a run with no history, which breaks
   Guarantees [3](guarantees.md#guarantee-3) and [4](guarantees.md#guarantee-4). Not chosen
   because the pre-fix code path is off by default
   (`history.enableCleanupOrphanedHistoryBranchOnWorkflowCreation=false` until
   [#7661][pr7661]) and must be enabled by dynamic config. The cleanup reuses the request's
   `ctx`, so a pure context-deadline timeout often cannot delete anything either, which narrows
   the window to "commit succeeded, connection error reported".
4. **Matching task reader deadlocks on an ack-level condition failure.** [PR #7855][pr7855] /
   [#7930][pr7930]; fix `75cbf7559df9c8f46650aa81521b5ae2943e441d`, pre-fix
   `e4c7e792a3d2baa1c272fb83eccc56595f70c263` (v1.4.0 → v1.4.1). Diff: `getTasksPump` →
   `handleErr` → `Stop()` → `stopWg.Wait()` waits on itself. The trigger is task list ownership
   moving (pause or partition of a matching host). Not chosen: with ringpop routing, the manager
   unregisters before it blocks, so clients mostly see pollers stranded for one long-poll interval
   and a goroutine leak. The impact is severe only with the shard distributor. A weak fit for
   [Guarantee 5](guarantees.md#guarantee-5).
5. **Signals bypass DelayStart.** [Issue #7595][i7595] / [PR #7702][pr7702]; fix
   `a50e09aa0`. A signal to a workflow still in DelayStart scheduled the first decision early,
   which breaks [Guarantee 4](guarantees.md#guarantee-4) (no early timer). Rejected on criterion
   (b): it is deterministic and needs no fault or race.

## Considered and not used

- Shrinking `history.cacheMaxSize` only to force reloads for this bug. It may be proposed as
  class-level tuning ("mutable state reloaded from DB more often") in `tuning.md` once that file
  exists, and judged there.
- Workload commands that specifically schedule a timer to expire mid-heartbeat. The general
  workload of timers, local activities and cancellation must reach it on its own.

[pr8595]: https://github.com/cadence-workflow/cadence/pull/8595
[pr6966]: https://github.com/cadence-workflow/cadence/pull/6966
[pr7400]: https://github.com/cadence-workflow/cadence/pull/7400
[pr8163]: https://github.com/cadence-workflow/cadence/pull/8163
[pr6871]: https://github.com/cadence-workflow/cadence/pull/6871
[pr5671]: https://github.com/cadence-workflow/cadence/pull/5671
[pr7617]: https://github.com/cadence-workflow/cadence/pull/7617
[pr7590]: https://github.com/cadence-workflow/cadence/pull/7590
[pr7661]: https://github.com/cadence-workflow/cadence/pull/7661
[pr7855]: https://github.com/cadence-workflow/cadence/pull/7855
[pr7930]: https://github.com/cadence-workflow/cadence/pull/7930
[pr7702]: https://github.com/cadence-workflow/cadence/pull/7702
[i7595]: https://github.com/cadence-workflow/cadence/issues/7595
