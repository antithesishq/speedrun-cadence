Verdict: stands

# Adversarial review: retried-reset-applied-twice

Finding: one client `ResetWorkflowExecution` call (driver -> client2 -> Go client v1.4.0) was committed twice by the
server. Reset A (d112 -> 939f, 20:41:06.084) and reset B (d112 -> 1129, 20:41:15.621) carry the identical request ID
`counter-182-d112c482-3e92-425f-b021-6ba69f9c8da9-15` (DecisionTaskFailed cause=RESET_WORKFLOW, 939f ev15 and 1129 ev15
in `scratchbook/logs/fixverify-reset-dup/out1.txt`). Reset B threw away run 939f, which lost the acknowledged signals
s617, s620 and s621 and made the s609 duplicate possible. Evidence: `scratchbook/rca/fixverify-reset-dup.md`, run
`41bf06723b005a4e1c4cc37b560fb195-67-6`, moment `4272893864207768579 / 474.5656898911111`.

## Check 1: harness intervention between the fault and the symptom

- Could two separate driver calls explain the two resets? The request ID is deterministic (`<wid>-<baseRun>-<point>`,
  `antithesis/client/main.go:613-621`). Two separate calls that picked the same point on the same base run would collide.
  That cannot happen here:
  - `RuleReset` only picks a workflow with `ResetsPending == 0` (`antithesis/driver/main.go:336-337`).
  - `ResetsPending` is decremented only in the completion callback (`driver/main.go:347-349`) or on NotSent (`:364-368`).
  - `Clients.Call` runs `complete` exactly once, when the reply arrives or when the client is lost (`antithesis/driver/rpc.go`
    Call and connection-close handler). A driver-side `RuleWait` timeout (2 s, `driver/main.go:29`) does not decrement it.
  - So at most one reset of counter-182 is in flight at a time. The timeline has exactly two counter-182 reset replies: 20:39:11.105
    (base 5f57) and 20:41:15.623 (base d112). It has no "connection closed / ops lost" line after the first reset
    (`timeline.log`, grep). A later call would read the current run with Describe (`client/main.go:592-596`), so it
    could not reuse base d112.
- The only retry layer between the call and the server is the Go client's `backoff.Retry(..., isServiceTransientError)`,
  which sends the *same* request (`go.uber.org/cadence@v1.4.0/internal/internal_workflow_client.go:788-805`). The harness
  takes no action between reset A committing and reset B arriving.
- Conclusion: one API call, two committed resets. The harness did not cause it.

## Check 2: configuration and tuning (would it happen with defaults?)

- `antithesis/config-template/dynamicconfig.yaml` changes the cache size (20 / 30s TTL), task list partitions, ack intervals,
  drain durations and decision heartbeat timeout. None of these touches reset or request-ID handling.
- The dedup that should catch the retry, `reset_workflow_execution.go:124-133` (v1.4.5 and master), compares the current
  run's `ExecutionInfo.CreateRequestID` with the reset request ID. Rebuild always overwrites that field with the start
  event's request ID (`execution/mutable_state_builder_methods_started.go:192-198`, the "prefer requestID from history
  event" branch, same on master). This is deterministic. It does not depend on the cache: a cached or a reloaded mutable
  state gives the same value. With default config the check can never match after a reset.
- The only other dedup is the strong-idempotency `DuplicateRequestError` path (`:177-185`). It needs
  `history.enableStrongIdempotency`, which defaults to false (`common/dynamicconfig/dynamicproperties/constants.go:5429-5433`,
  master). Our config leaves it at the default.
- Conclusion: this happens with Cadence defaults.

## Check 3: faults beyond what the guarantee promises

- What triggers it: the reply to a reset that committed is lost (network partition or postgres timeouts. The d112 lock was
  held 4.55 s, released 20:41:10.634, `timeline.log:12776`), and the Go client retries. Lost responses and client retries
  are the fault model of Guarantee 2 (`scratchbook/guarantees.md:10`, "lost responses + client retries"). They are also
  the reason a request ID exists. There is no clock skew beyond jitter, no data loss and no disk corruption.
- Conclusion: the faults are within the promise.

## Check 4: can the checker fire on documented behaviour?

- The assertion that fired is "a signal is applied at most once in a workflow that was reset" (`driver/model.go:319`,
  `:419`; `driver/main.go:293`). The proximate duplicate of s609 is the signal dedup set not crossing continue-as-new
  (Gap B in the RCA: `signal_workflow_execution.go:66-75`, with `AddContinueAsNewEvent` not copying `SignalRequestedIDs`).
  **This is the strongest defense.** Cadence never documents that the signal dedup set survives continue-as-new, so a
  CAN-crossing duplicate could be called a known limitation.
- Why that defense does not save *this* finding: the finding is about the reset retry, not the CAN duplicate. Its harm is
  independent of Gap B:
  - Two resets were committed for one request ID. The server's own code intends to deduplicate this
    (`reset_workflow_execution.go:124`, comment "dedup by requestID"). It only stopped working when #5821 (baac62184) added
    the prefer-event override. That is a regression, not a stricter-than-documented check.
  - The second reset dropped run 939f together with three acknowledged signals (s617, s620, s621). The reset-B reapply only follows
    ContinuedAsNew links from the base (`reset/resetter.go:487-572`, `:614-619`). Losing acknowledged signals violates
    Guarantee 2 even when no duplicate exists. The RCA says "an acknowledged signal survives a reset of its workflow" fires right after.
  - Without Gap A, the 20:41:15.623 retry of s609 would have reached 939f, which already had its request ID, and been a no-op
    (RCA, "Why the v1 patch did not…").
- Also considered: does reset document "may apply again on retry"? I found no such documentation. The request-ID field and the
  server-side dedup branch show the intent is idempotency.
- Conclusion: for the reset part, the checker does not fire on documented behaviour. Gap B is a contributing limitation and
  should be reported separately (as the RCA's driver note proposes).

## Check 5: reproduction across timelines, runs and versions

- Observed once: counter-182 in the fix-verify run (v1.4.5 + signal patch, `src/cadence-fixverify-signal` @ffbc65c2f). It has
  not been seen independently on stock v1.4.5 or master in another run. This is the weakest point of the finding.
- Source check: the dead dedup is identical in v1.4.5 and master @572e7e2c (`reset_workflow_execution.go:124-133`,
  `mutable_state_builder_methods_started.go:192-198`). The fix-verify patch does not touch the reset path. So the mechanism
  is the same on upstream release and master, from code inspection.
- The RCA's v2 patch (set `CreateRequestID = resetRequestID` after Rebuild) passes `go test ./service/history/reset/`. It has
  not yet been verified in an Antithesis run.

## Strongest defense and why it fails

"The duplicate is just signal dedup not crossing CAN, which is a known design limitation, and the reset has nothing to do
with it." This fails because the request IDs in the history show two committed resets for one request. That alone loses
acknowledged signals. The server's own dedup branch for exactly this case is dead code since #5821, with default config,
under the fault model the request ID was designed for. A second possible defense, "two driver calls collided on a
deterministic request ID", is ruled out by the `ResetsPending` guard and the call log.

## Severity to state to Uber engineers

Medium-high. `ResetWorkflowExecution` is not idempotent under client retry (a regression from #5821, also on master, with
default config, without strong idempotency, and always on SQL). A timeout on a reset that committed, followed by the standard
Go-client retry, resets the base again. That silently discards the first reset run and any signals acknowledged to it, and
can lead to duplicate signals. It needs a lost reply on a reset, which is common for operator and batch-reset tooling under load.
It does not corrupt state otherwise. It is seen once, but the code path is deterministic. The fix is a one-line change.
