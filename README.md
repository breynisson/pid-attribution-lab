# PID attribution lab

A small Go simulation of a delayed event being assigned to the wrong process
after PID reuse. The corrected implementation matches the full capture-time
identity, retains historical metadata, and leaves missing information explicitly
unresolved. The original faulty implementation remains available for comparison.

This is a learning project motivated by runtime-security telemetry. Core milestones
1–5 are complete. An optional [bounded TLA+ model](formal/README.md) now checks
safety and conditional liveness separately; Linux integration is not built.

## Run the passing suite

Use Go 1.23 or newer with `go` on `PATH`, a writable build cache, and network
access for the initial dependency download unless it is already cached.
[Rapid v1.3.0](simulation/go.mod) is pinned and used only in tests. There are no
production dependencies, services, databases, or Linux privilege requirements.

From the repository root:

```sh
cd simulation
unset PID_ATTRIBUTION_FAULTY
go test . -run '^(TestDelayedEventAfterPIDReuse|TestNewEventWithOnlyPreviousLifetimeMetadata|TestActivityBeforeMetadata)$' -count=1 -v
go test ./... -count=1 -rapid.seed=11 -rapid.nofailfile
go vet ./...
```

Each command should exit with status **0**. The full suite includes deterministic
regressions, generated properties, and automated mutation checks. Mutation checks
launch child Go test processes with temporary source overlays; expected failures
inside those child processes make the enclosing mutation checks pass.

For fresh generated inputs, run `go test ./... -count=1`. The fixed seed above
makes the run reproducible; `-rapid.nofailfile` prevents failure-artifact writes.
If the default build cache is not writable, prefix a command with
`GOCACHE=/tmp/pid-attribution-go-build`.

## Reproduce the intentionally failing demonstration

From `simulation/`:

```sh
PID_ATTRIBUTION_FAULTY=1 go test . -run '^TestDelayedEventAfterPIDReuse$' -count=1 -v
```

This command should exit with status **1**, reporting A as expected and B as
actual:

```text
expected: {EventID:E Resolved:true Key:{Scope:lab PID:7 Incarnation:1} Workload:workload-A}
actual:   {EventID:E Resolved:true Key:{Scope:lab PID:7 Incarnation:2} Workload:workload-B}
```

The environment assignment applies only to that command. To compare the same
full contract suite against the faulty implementation:

```sh
PID_ATTRIBUTION_FAULTY=1 go test ./... -count=1 -rapid.seed=11 -rapid.nofailfile
```

That also exits with status **1** on attribution assertions. Build or dependency
errors are setup failures, not successful reproductions. Mutation checks are
skipped in this mode because they require the corrected baseline.

## Read the code and evidence

| File | Purpose |
|---|---|
| [records.go](simulation/records.go) | Process identity, metadata, activity, and attribution records. |
| [attributor.go](simulation/attributor.go) | Corrected synchronous attributor; `Result(eventID)` exposes later resolution. |
| [faulty/attributor.go](simulation/faulty/attributor.go) | Original current-PID lookup. |
| [attributor_test.go](simulation/attributor_test.go), [complications_test.go](simulation/complications_test.go) | Regression traces, replay behavior, scope/boot isolation, missing metadata, and repeated reuse. |
| [property_test.go](simulation/property_test.go), [contract_test.go](simulation/contract_test.go) | Generated schedules, independent oracle, and shared implementation selection. |
| [mutation_test.go](simulation/mutation_test.go) | Five targeted faults checked against named tests. |
| [formal/](formal/README.md) | TLA+ model, TLC configurations, counterexample, and bounded-check results. |

[Findings](simulation/FINDINGS.md) explains the counterexamples, guarantees,
mutation evidence, and practical limits. [The counterexample log](simulation/COUNTEREXAMPLE.md)
preserves the milestone history and additional replay commands.
[The project plan](PROJECT_PLAN.md) describes the original scope and optional work.

Passing generated tests provide evidence for the sampled schedules and explicit
assumptions. They are not exhaustive verification. Identity capture is synthetic,
history is unbounded, and callers must serialize access.
