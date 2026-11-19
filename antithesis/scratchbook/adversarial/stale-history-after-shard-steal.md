Verdict: stands

Finding: stale-history-after-shard-steal. After a shard steal, a history host that lost the shard keeps serving
`PollMutableState` / `GetWorkflowExecutionHistory` / `DescribeWorkflowExecution` from its cached mutable state.
A client that has just been told "completed" then reads the same run as running, with a truncated history and no close event.
Sources: rca/history-missing-close-event.md (prefix build, parent-10 and parent-7) and rca/stale-history-v145.md
(v1.4.5 + fix-verify patches, chain-17). The evidence reviewed was already on disk. No new runs were started.

## Check 1: harness intervention between the fault and the symptom

- The driver path is a result read followed by a history read: `driver/main.go:438-445` and `:461` (v145 run, run ID pinned),
  and `:400-403` (prefix run, runID ""). The client round-robins frontends (`client/main.go:59-66`). These are ordinary
  client calls. The harness does not restart, reconfigure or touch the DB between the commit and the stale read.
- The history read goes through the standard Go client. No custom read path is involved, and nothing is injected into Cadence.
- The SUT is upstream source plus coverage instrumentation only (triage-context.md).
- The one harness-adjacent factor is the node-exits issue (rca/node-exits.md): `startup-dependency-failfast` causes nodes to
  fail at startup and come back later. In the v145 run, the trigger was cadence1 rejoining at 94.6 after a startup failure
  (rca/stale-history-v145.md, timeline 90.29 and 94.61). A node restarting and rejoining the ring is a routine operational
  event (deploys, OOM restarts), so this changes how often the trigger happens, not the mechanism.
  In the prefix run, cadence2 being dead left a two-node ring. That made the split easier to reach but did not cause it.
- **Result: no harness intervention explains the symptom.**

## Check 2: configuration and tuning (antithesis/config-template/dynamicconfig.yaml)

Values that touch this path, compared with the v1.4.5 defaults (`common/dynamicconfig/dynamicproperties/constants.go`):

| key | harness | default | effect on the stale window |
|---|---|---|---|
| history.cacheMaxSize | 20 (`dynamicconfig.yaml:5-6`) | 512 (`constants.go:974`) | A smaller cache evicts sooner, so a stale entry is *more* likely to be dropped and reloaded from the DB. The harness value **shrinks** the window. |
| history.cacheTTL | 30s (`:7-8`) | 1h (`constants.go:2969`) | Same reasoning: the harness value **shrinks** the window. |
| history.shardUpdateMinInterval | 1s (`:18-19`) | 5m (`constants.go:3199`) | The periodic `updateShardInfoLocked` write is what fences and heals the stale host (98.89 in the v145 run). With the default, that write happens less often, so healing is slower or no faster. The harness value **shrinks** the window. |
| shutdownDrainDuration 0s, ack intervals 5s | | | These do not affect read routing or cache validity. |

The ring/membership code (`controller.go`) uses no harness-specific tuning, and ringpop runs with its defaults.
**Result: every relevant tuning makes the bug harder to see. With Cadence defaults the stale read would last at least as long and probably longer.**

## Check 3: faults beyond what the guarantee promises

- Guarantee 3 (guarantees.md) lists "shard movement (mutable state reload)" under "Breaks under" and states "any later read
  returns the earlier read as a prefix".
- Prefix run: partitions and clogs (high_availability). These are within the declared fault model.
- v145 run: **no faults at all**. The injector stopped at 92.34. The split happened at 94.6-94.98 during ordinary ring convergence
  after a node restart. This is the strongest evidence that the faults are not excessive. A rolling restart is enough.
- **Result: holds.**

## Check 4: can the checker fire on documented behaviour?

- Checker: `driver/model.go:399-410`. It fires only when `wantClosed` is true (the server has just reported the run closed)
  and the history has no close event. In the v145 run the run ID was pinned, so there is no ambiguity about which run was read.
- **Strongest defense:** Cadence does not document linearizable or monotonic reads across frontends or across shard movement.
  Ringpop membership is eventually consistent, and range ID is designed to fence writes, not reads. On this view, a stale
  read during membership convergence is "by design", and the check asks for a guarantee nobody made.
- **Why the defense does not hold, though it does lower severity:**
  - The read is not just old. It **contradicts a response the same client got from the server 0.6 ms earlier**
    (completed, correct result). Returning a workflow to running is the "a closed run stays closed" property that Guarantee 4
    cites from Cadence's own fault-oblivious model. A stale view can resurrect a run.
  - Cadence's history service is designed as a single owner per shard with authoritative cached mutable state. Users and the
    SDK treat `GetWorkflowExecutionHistory` and `DescribeWorkflowExecution` as authoritative, for example the CLI, replayers
    and `GetWorkflow().Get` followed by describe. Nothing in the API docs flags these reads as possibly stale or as able to
    regress.
  - The stale host does not lose ownership when its ring view changes. It can keep a stolen shard **indefinitely** if it
    makes no writes: `controller.go:301-307` returns any valid item without a membership check, `acquireShards` only adds
    shards, and `context.go:359` returns cached state without a range check. The window ends only when the stale host's own
    write fails. With the default `shardUpdateMinInterval` (5m), an idle shard could serve stale reads for minutes.
  - The checker does not misread events: the DB and the owner's cache both have the close event (`exec1.txt`, `pg1.txt` in
    logs/stale-v145 and logs/hist-close).
- **Result: the checker is stricter than any written consistency promise. Even so, it fires on a state (completed, then running) that Cadence's model
  says should not be observable. This is a semantics gap rather than a checker bug.**

## Check 5: reproduction across timelines, runs and versions

- Prefix build (c889f8319), run `22fa5e0b...-67-6`: parent-10 (shard 6) and parent-7 (shard 0), on two different timelines
  (input hashes -1535614395710740674 and -8174707967280476638). parent-10 was confirmed by `snouty runs exec`: cadence1's cache has
  NextEventID 32 and is running, while the DB has NextEventID 34 and is completed.
- v1.4.5 (+ fix-verify patches that do not touch this path), run `41bf06723b...-67-6`: chain-17 (shard 3), confirmed by exec at
  96.5063. The trigger was different: no partition, only a node rejoining.
- master 572e7e2c: not observed. Code inspection (rca/stale-history-v145.md) says `poll_mutable_state.go`, `execution/context.go`,
  `execution/cache.go` and `shard/controller.go` are identical to v1.4.5. I re-checked `context.go:350-372` against master
  with no diff. It is therefore expected but not demonstrated on master.
- **Result: 3 workflows, 2 runs, 2 builds, 2 different triggers. It reproduces.**

## Strongest defense overall

"Reads are not fenced by design, Cadence never promised read-your-writes across shard movement, so the checker is too
strict." This is a fair reading of the *written* docs. It does not explain away the observation: the harness, the tuning and
the fault set are all ruled out, and the symptom also appears with no faults injected. It argues only for a lower severity,
not for a false positive.

## Severity to state to Uber engineers

**Medium: a real consistency defect, not data loss.** The DB is always correct, and the stale host heals when its next write to
the shard fails. During ring convergence, which happens on every rolling deploy or node restart, a frontend can route reads to a
host that has silently lost the shard. That host then reports an already-completed workflow as running, with a truncated history,
for seconds to minutes (longer with the default `shardUpdateMinInterval`=5m). Callers that poll describe or history to decide
next steps, such as orchestration, CLI or operator tooling, or replay and non-determinism checks, can act on a state that has
already been overwritten. Suggested fix direction: validate shard ownership on reads (for example re-check membership or the
range ID in `getOrCreateHistoryShardItem` and on cache hits, or release shards in `acquireShards` that the ring no longer assigns
to this host). Present it as "monotonic-read / closed-run-resurrection under shard steal", and say clearly that it is
not the #8595 timer bug.
