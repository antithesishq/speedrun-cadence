# Triage context (for sub-agents)

Workspace: /home/exedev/cadence. SUT: Cadence server, unmodified upstream source plus Antithesis
coverage instrumentation only. Sources: src/cadence-v1.4.5 (release), src/cadence-master
(master @572e7e2c), src/cadence-prefix (c889f8319, before fix #8595). Do NOT modify src/.

Harness (antithesis/):
- config-template/docker-compose.yaml: postgres (17.4), schema (one-shot), cadence1..3 (all four
  services each; 8 history shards; ringpop), client1..3 (Cadence Go client v1.4.0 + worker,
  JSON-RPC over unix socket), driver (hegel state machine; fault-excluded).
- config-template/cadence.yaml, dynamicconfig.yaml (small history cache 20, 3 task list partitions, ...)
- client/workflows.go: workflows counter (signals "add"/"finish", query "state", continue-as-new every
  CanEvery signals, cancel), chain, timer, parent, race. client/main.go: RPC methods incl. reset
  (ResetWorkflowExecution to a chosen DecisionTaskCompleted event, signals reapplied by default).
- driver/model.go + driver/main.go: model and checks. Signal IDs are unique per send (s<N>, act-s<N>);
  the driver never re-sends a signal ID (the Go client may retry internally with the same request ID).
- Faults: Antithesis high_availability (kills, stops, partitions, throttles, clock jitter); driver excluded.

Run commands (read `--help` of each snouty command before using it):
- `snouty runs properties <run> --failing --detail`
- `snouty runs logs <run> <input_hash> <vtime> --detail` (input_hash may be negative; pass as-is)
- `snouty runs search`, `snouty runs events`, and `snouty debug` / `snouty runs exec` for commands at a moment
  (see /home/exedev/.claude/plugins/cache/antithesis/antithesis/0.2.0/skills/antithesis/references/debug/MODULE.md).
- Triage guidance: /home/exedev/.claude/plugins/cache/antithesis/antithesis/0.2.0/skills/antithesis/references/triage/MODULE.md,
  logs.md, root-cause.md in the same directory. Read logs.md before downloading logs.

False-positive review (mandatory before calling anything a Cadence bug): client API misuse, a
check stricter than Cadence's documented semantics, harness config/topology, checker bugs. Burden
of proof is on the violation. Classify: false-positive | suspect | real | harness-gap.

Keep raw logs under antithesis/scratchbook/logs/<your-topic>/, never paste big logs into your answer.
