# Logical time and retention

## Contract (defined before implementation)

`OpenBackend` retains its existing unlimited-duration behavior. The opt-in
`OpenBackendWithRetention` accepts separate metadata and pending-event TTLs.
Zero disables that expiry. `AdvanceTo` explicitly advances a persisted logical
clock; backwards movement is rejected. Nothing expires merely because wall time
passes, including during downtime. The caller chooses the units and advances
the clock. Every operation, including advancement, is serialized by the backend
mutex; no timer or goroutine is introduced.

A record expires when `now - firstAcceptedAt >= TTL`, before another delivery
at that clock value can use it. Identical metadata deliveries while cached and
activity replays never refresh deadlines. Metadata expiry frees its cache slot.
A subsequent metadata delivery may acquire a new lease: there is no permanent
metadata tombstone. Conflict rejection therefore covers only cached metadata;
truth and lifetime-wide immutability of inputs remain source obligations.

Pending-event expiry is terminal: the retained event becomes explicitly expired
and unresolved. Later metadata or replays cannot revive it. Resolved results and
all accepted event IDs remain retained, so result stability and first-origin
replay protection survive expiration. `Status` distinguishes an expired event
from one still waiting; `Result` keeps its existing shape. Pending expiry frees
waiting-index entries, **not event-ID capacity**. A full event ledger still
rejects new events. Unlimited throughput would require a separate replay horizon
and acknowledgment/compaction contract.

Safety requires authentic capture-time keys, unique process lifetimes and event
IDs, and truthful immutable metadata. Under those assumptions insufficient
history yields unresolved, never a lookup of the current PID holder. Published
results survive metadata expiry and recovery. Snapshots persist results, expiry
state, admission times and clock, rather than deriving results from surviving
metadata. Reopening requires the same limits and retention policy. Version-1
snapshots migrate only with expiration disabled because they lack timestamps.

Useful attribution is conditional: an accepted event and its matching accepted
metadata must overlap while the event is still pending. For metadata-first
delivery the gap must be strictly less than the metadata TTL; for event-first
delivery it must be strictly less than the pending TTL (or that TTL is zero).
Order independence applies only to schedules satisfying the relevant overlap
and admission conditions. Capacity rejections, deadline crossings, missing
delivery and storage failures can change coverage. These are admission-relative
windows, not a maximum delay measured from physical capture. No clock fairness
or message-delivery fairness is assumed for safety. Progress needs both.

`Stats` counts cached metadata, accepted events, active pending events, resolved
events and terminal expired events. Limits bound entries, not bytes, temporary
snapshot copies, disk space or upstream queues. Longer metadata retention helps
late activity but occupies cache slots; longer pending retention helps late
metadata but keeps more events waiting. Neither can compensate for missing
messages, and neither reclaims the permanent event ledger.

## Implementation and evidence

[retention.go](retention.go) implements the clock, status, counts and snapshot
restoration. [backend.go](backend.go) reuses the existing resolver on a candidate
index, saves the resulting state, then publishes it. Failed saves still poison
the instance until recovery determines which complete snapshot survived. There
are no new production dependencies and the original `Attributor` is unchanged.
The backend uses its existing mutex; only the deadline concurrency test starts
goroutines, racing metadata delivery against clock advancement. Either whole
operation can win, but a terminal expired event cannot subsequently resolve.

[Deterministic regressions](retention_test.go) cover both sides of the exact
deadline, replay without lease refresh, expired history followed by PID reuse,
metadata re-admission, event capacity, durable answers and terminal status,
legacy migration, clock overflow/backwards movement, commit ambiguity, and the
deadline race. [Snapshot tests](retention_snapshot_test.go) reject contradictory
persisted state. [Generated tests](retention_property_test.go) check both arrival
orders inside the window and arbitrary deliveries, time advances and recovery
after a physically valid three-lifetime capture prefix. Capture truth is frozen
before delivery. The usefulness oracle independently intersects admission-time
intervals; it does not call the resolver or reproduce its pending-index logic.
These generated schedules have ample capacity and one metadata lease per key;
the deterministic tests cover re-admission and resource rejection separately.

[Five Go source-overlay mutants](retention_mutation_test.go) are caught by named
tests: PID fallback, recomputing answers on restart, reviving expired events,
using `>` instead of `>=` at the deadline, and always returning unresolved.
Each named baseline must pass and each mutant must finish with an assertion
failure; build errors and missing tests do not count as detection. There are no
production fault switches.

The [TLA+ retention model](../formal/extensions/Retention.tla) checks safety with
no fairness. With two full identities sharing a PID, two captured event IDs,
metadata/event capacities of one, TTLs of one and time `0..2`, corrected TLC
search completed: **151 generated / 90 distinct states, queue 0, exit 0**.
Four faulty model variants fail the intended invariants; see the
[configuration, traces and limits](../formal/extensions/README.md#retention).

The shortest PID-fallback counterexample is easy to reproduce: A's metadata is
initially cached; advance to its deadline and remove it; admit B's metadata for
the reused PID; deliver A's old event. Looking up that PID returns B and falsely
reports confidence. Exact-key lookup instead returns unresolved. This trace is
preserved as `TestRetentionExpiredHistoryNeverUsesCurrentPID`. TLC found it by
breadth-first search, not by Rapid shrinking. A second counterexample resolves A,
expires its metadata, crashes and recomputes on restart: the published answer
disappears. Persisting the result directly prevents that loss.

## Coverage experiment

`TestRetentionCoverageExperiment` runs 24 valid successive lifetimes reusing PID
7, one capture each, two ticks apart. Half deliver metadata first and half
activity first. The other message is delayed by a repeating 1, 3, 6, 9 ticks.
Equal-time messages use stable insertion order. Each input is attempted once,
without retries. The experiment ends at tick 80, after all finite leases expire.
Every resolved answer is checked against independent capture truth after every
delivery. These are synthetic counts, not measurements of production traffic.

Observed results (`0` TTL means unlimited duration):

| Metadata / pending TTL | Metadata / event cap | Resolved / 24 | Expired | Still pending | Rejected metadata / events | Peak metadata / pending |
|---|---|---:|---:|---:|---|---|
| 0 / 0 | 24 / 24 | 24 | 0 | 0 | 0 / 0 | 24 / 2 |
| 2 / 0 | 24 / 24 | 15 | 0 | 9 | 0 / 0 | 2 / 10 |
| 0 / 2 | 24 / 24 | 15 | 9 | 0 | 0 / 0 | 24 / 1 |
| 5 / 5 | 24 / 24 | 12 | 12 | 0 | 0 / 0 | 4 / 3 |
| 10 / 10 | 24 / 24 | 24 | 0 | 0 | 0 / 0 | 7 / 2 |
| 0 / 0 | 3 / 24 | 3 | 0 | 21 | 21 / 0 | 3 / 21 |
| 5 / 5 | 3 / 24 | 10 | 14 | 0 | 2 / 0 | 3 / 3 |
| 10 / 10 | 3 / 24 | 14 | 10 | 0 | 10 / 0 | 3 / 5 |
| 5 / 5 | 3 / 8 | 5 | 3 | 0 | 2 / 16 | 3 / 3 |

Longer history supports more delayed activity when capacity is sufficient.
Under three metadata slots, keeping history forever admits only the first three
lifetimes; expiration makes room for later ones. Increasing TTL from five to ten
then helps some delayed pairs while causing more metadata rejections: coverage
depends on both timing and admission. Buffering cannot help when matching
metadata never arrives. In the last row all eight event slots remain occupied
by five results and three expired records, so sixteen events are rejected.
Pending expiry is a finality policy, not an event-ledger reclamation scheme.

## Reproduce

From `simulation/`, Go 1.23+ and a local macOS/Linux filesystem:

```sh
unset PID_ATTRIBUTION_FAULTY PID_ATTRIBUTION_UNTRUSTED_DEMO PID_ATTRIBUTION_CRASH_SNAPSHOT
export GOCACHE=/tmp/pid-attribution-go-build
# Regressions and targeted mutation checks before generated properties.
go test . -run '^TestRetention' -count=1 -v
go test . -run '^TestPropertyRetention' -count=1 -rapid.seed=11 -rapid.nofailfile
go test ./... -count=1 -rapid.seed=11 -rapid.nofailfile
go test -race . -run '^(TestBackend|TestSensor|TestBoot|TestRetentionConcurrentDeadline|TestPropertyRetention)' -count=1 -rapid.seed=11 -rapid.nofailfile
go vet ./...
# Print the experiment as CSV rows in test output.
go test . -run '^TestRetentionCoverageExperiment$' -count=1 -v
```

From the repository root with the [pinned TLC setup](../formal/README.md#reproduce):

```sh
export TLA_JAR=/tmp/pid-attribution-tla2tools-1.7.4.jar
sh formal/extensions/run.sh retention-pid-fallback # intentionally exits 12: CaptureTruth
sh formal/extensions/run.sh retention-recompute    # intentionally exits 12: StableResults
sh formal/extensions/run.sh retention-revive       # intentionally exits 12: TerminalExpiry
sh formal/extensions/run.sh retention-unresolved   # intentionally exits 12: Useful
sh formal/extensions/run.sh retention              # exits 0
sh formal/extensions/check.sh                      # all 18 expected outcomes; exits 0
```

## Liveness and limits

Safety does not require delivery or clock progress. With finite pending TTL,
every accepted event eventually becomes resolved **or expired** only if logical
time eventually reaches its deadline and storage/recovery permit those operations
to complete. Without explicit advancement, expiry does not happen. Wall-clock
downtime is excluded; applications must choose how to advance time after recovery.

Eventual *resolution* needs successful admission and matching-message delivery
inside the overlap window. Weak fairness alone permits delivery after any finite
deadline, so it is insufficient for finite-retention resolution. Use an explicit
logical-delay bound smaller than the appropriate TTL, or retain the necessary
information until matching delivery. No backend can guarantee resolution of
missing metadata merely by changing its lock or scheduler.

Key uniqueness and truthful inputs remain essential even with infinite history.
After metadata expiry, the backend cannot detect a conflict with the forgotten
record; it trusts re-admitted metadata. Retained answers stay stable even under
that conflict, but correctness of new answers needs immutable source metadata.
Event IDs and resolved results remain retained indefinitely within the entry cap.
Bounds do not cover bytes, queues, payload size, full-snapshot I/O, or temporary
copies. Status and statistics scan retained events. No wall-clock service,
automatic retry, live sensor, event-ID compaction or throughput benchmark is added.

Tests and race checks passed on macOS; generated tests cover sampled schedules,
not all executions. TLC completes only the stated finite abstraction, assumes
atomic durable whole-call operations, and does not verify filesystem writes,
Go locks, snapshot encoding, real capture or their combined implementation.
