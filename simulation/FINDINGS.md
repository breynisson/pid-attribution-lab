# Findings: trustworthy attribution across PID reuse

The faulty backend answers who has a PID when an event arrives. Attribution
requires identifying the lifetime that generated the event. Capturing that
identity at event creation and retaining its metadata fixes the demonstrated
failures under this simulation's assumptions.

## Counterexamples

The original ordinary regression follows this causal sequence:

| Step | World and delivery |
|---|---|
| 1 | A starts as `(lab, 7, 1)`; deliver A's metadata. |
| 2 | A creates E; capture A's key but hold the activity in a queue. |
| 3 | A exits; deliver its exit notification. |
| 4 | B starts as `(lab, 7, 2)`; deliver B's metadata. |
| 5 | Deliver E, still carrying A's key. |

`faulty.Attributor` returns B and `workload-B`; the correct answer is A and
`workload-A`. A's metadata was delivered, so the expected answer is unambiguous.
This is fully sequential: locking the current-PID table cannot make its answer
describe a previous lifetime.

The constrained property retains this five-step skeleton. Seed 1 shrank its
values to PID 1, incarnations 1 and 2, and workloads `a-A` and `a-B`, without
changing the wrong-origin result.

Stateful generation found a reverse safety failure at seed 11. Its recorded
eight-action trace starts three processes, exits PID 1, reuses PID 1 with
incarnation 4, captures E1 from that new process, then delivers only the old
incarnation-1 metadata followed by E1. The faulty backend assigns E1 to the old
process. The correct result is **unresolved**, since incarnation 4's metadata
has not arrived. `TestNewEventWithOnlyPreviousLifetimeMetadata` preserves this
trace, including two unrelated starts retained by shrinking.

The **smallest recorded generated action trace** has four actions, at seed 3:

1. Start a process and queue its metadata.
2. Create an event from the live process and queue the activity.
3. Deliver the activity: unresolved is correct.
4. Deliver matching metadata: the event must now resolve.

The faulty implementation exposes no updated attribution at step 4.
`TestActivityBeforeMetadata` preserves this useful-attribution failure without
relying on generated inputs or a persisted failure file. “Smallest recorded” describes the
observed shrink result, not a proof of global minimality. The full traces are in
[COUNTEREXAMPLE.md](COUNTEREXAMPLE.md).

To replay the two generated stateful failures from `simulation/`:

```sh
PID_ATTRIBUTION_FAULTY=1 go test . -run '^TestStatefulAttribution$/Safety$' -count=1 -v -rapid.seed=11 -rapid.nofailfile
PID_ATTRIBUTION_FAULTY=1 go test . -run '^TestStatefulAttribution$/UsefulAttribution$' -count=1 -v -rapid.seed=3 -rapid.nofailfile
```

Both should exit 1. Unset `PID_ATTRIBUTION_FAULTY` to run the corrected version.

## Information and contracts

The event carries `ProcessKey(scope, PID, incarnation)` captured at creation.
The synthetic incarnation never repeats within a scope. Scope must distinguish
identity boundaries, including boots when counters restart; the reboot fixture
uses different opaque scope strings. This is not evidence that a real kernel
provides a universal incarnation counter or that a sensor can capture it safely.
Metadata supplies the immutable executable and workload for that lifetime.

If activities exposed only PID, event ID, and kind, an event created by A before
exit and one created by B after reuse could produce identical delivered messages.
Those histories have different correct origins. An interface that cannot
distinguish them cannot guarantee the correct resolved answer for both.

| Property | Required behavior |
|---|---|
| No false attribution | Every resolved event matches its capture-time process key and workload. |
| Useful attribution | Once both the event and matching metadata have arrived, its result is resolved by the end of that delivery call. |
| Honest uncertainty | Missing matching metadata leaves the event unresolved; another lifetime or scope cannot substitute. |
| Historical identity | Exits and PID reuse cannot change a previously resolved origin. |
| Replay stability | An event ID retains the first delivered origin and returns its latest result on replay. |

Useful attribution prevents “always unresolved” from passing the contract just
by avoiding false matches. It is conditional on delivery, not an unconditional
promise that every event eventually resolves.

## Why the oracle is independent

The test world tracks live processes and records `event ID -> true key/workload`
when an event is created. Starts select free PIDs; capture and exit select live
PIDs. Physical exit frees a PID independently of notification delivery. A queue
allows generated delivery choices for metadata, activities, and exits.

The backend receives only selected message payloads. It never receives the world
or truth ledger. The oracle does not read backend tables or call its attribution
logic. A separate delivered-metadata set determines when resolution is required;
it does not determine the true origin. Stable PID candidate ordering and Rapid
draws make replay independent of Go map iteration order.

After every generated action, the harness checks returned or queried results
against capture-time truth. `Result(eventID)` observes metadata-triggered updates;
the harness does not redeliver an event to manufacture resolution. The faulty
test adapter only stores actual return values and cannot resolve anything itself.

## Corrected design and assumptions

The corrected attributor keeps metadata by full process key, pending event IDs
by that same key, and results by event ID. Matching metadata resolves all pending
events for its exact key synchronously. Exits are accepted without evicting
history: this component does not track liveness. `DeliverActivity` returns a
snapshot; `Result` returns the latest stored value. Its boolean distinguishes an
unknown event from a delivered but unresolved one.

These guarantees depend on accurate, unique capture-time keys and consistent
metadata. Retaining metadata supports delayed activities after exit. Retaining
pending events supports metadata arriving later. Retaining results supports
queries and replay protection. All retention is currently unbounded. Expiration
would require a narrower resolution contract and an unresolved policy when
history is insufficient; it must never justify guessing the current PID holder.

Distinct logical events require globally unique event IDs across scopes and
boots. Conflicting uses of an ID are first-delivery-wins, which preserves the
first origin but does not authenticate it. Identical metadata and exits are
idempotent; conflicting metadata is outside the contract. Agreement across
delivery orders assumes nonconflicting messages and retention. Changing which
conflicting payload arrives first can change the result.

Calls are serial, with no added goroutines, channels, or locks. Explicit queues
model ordering, not concurrent execution. The maps are not safe for concurrent
access. Workload identity is fixed per lifetime; threads, `exec`, and workload
migration are not modeled.

## Evidence and limits

The passing commands in [README.md](../README.md) run the corrected implementation.
With seed 11 and default Rapid settings, the generated portion runs 100
constrained cases, 100 stateful safety cases, and 100 stateful combined-property
cases. A stateful case contains a generated sequence, not a single schedule step.
Separate deterministic tests cover duplicates, scopes, a simulated reboot,
withheld metadata, and multiple reuses. Those complications are not generated by
the original single-scope state machine.

The mutation harness checks the actual corrected source through temporary build
overlays. Each named test must pass without the fault and fail with it. A missing
test or build failure does not count. The five detected faults are:

| Fault | Named test | Property exposed |
|---|---|---|
| Ignore incarnation | `TestNewEventWithOnlyPreviousLifetimeMetadata` | No false attribution / honest uncertainty |
| Ignore scope | `TestScopeIsolation` | Scope isolation |
| Delete history on exit | `TestDelayedEventAfterPIDReuse` | Useful attribution with retained metadata |
| Always return unresolved | `TestActivityBeforeMetadata` | Useful attribution |
| Remove event-ID deduplication | `TestEventIDKeepsFirstOrigin` | Replay stability |

To inspect those failures while keeping the overall check passing:

```sh
go test . -run '^TestTargetedMutations$' -count=1 -v
```

Observed validation used Go 1.27.1 on macOS/arm64. Passing tests support the
implemented contracts for the tested examples and sampled schedules; mutation
results show that these particular defects are detected. Neither establishes
exhaustive verification, global shrink minimality, detection of every defect,
or correctness of a deployed telemetry system. The Go suite has no exhaustive
permutation check of complete message sets. A separate [bounded TLA+ model](../formal/README.md)
now reproduces a faulty counterexample and checks corrected safety and conditional
liveness in explicit finite configurations. It does not prove Go refinement.

Real identity capture, boot detection, concurrency, crash recovery, resource
bounds, and conflicting metadata remain unverified. Withheld-metadata fixtures
exercise finite traces, not infinite-time liveness. A future eventual-resolution
claim would also require eventual delivery of both messages and sufficient
retention. The Go module declares a 1.23 minimum; this validation was not a
cross-version or cross-platform compatibility matrix.

## Implications for runtime-security ingestion

These experiments suggest capturing a justified lifetime identity at the sensor,
carrying it through transport, and attaching workload metadata by that identity
at ingestion. A real identity scheme would need explicit tenant, host, boot,
namespace, capture-race, and collision assumptions. Synthetic uniqueness here
does not validate any particular Linux-derived scheme.

An ingestion system should expose unresolved attribution rather than confidently
attach another process's workload. Its retention budget and supported delivery
delay should be specified together, and replay IDs should preserve event origin.
An independent capture-time ledger and valid lifecycle generator can test those
contracts before connecting a live sensor. These are engineering lessons from
the model, not evidence about any vendor's implementation.
