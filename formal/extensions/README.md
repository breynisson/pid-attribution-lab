# Attribution boundary models

These focused models extend the original [attribution model](../README.md).
They separate existing Go behavior from proposed environment and storage
contracts. Subsequent [Go boundary implementations](../../simulation/BOUNDARIES.md)
implement selected contracts. Neither these models nor those tests compose all
these complications into a single verified system.

## Contracts defined before modeling

1. **Conflicting activity replays:** the first delivered event ID fixes its key;
   subsequent conflicting payloads must not rebind it or change a resolved result.
   Agreement with capture truth additionally requires the first payload to be
   truthful. A forged first payload cannot be recognized from the ID alone.
2. **Conflicting metadata:** the original Go `Attributor` map overwrites metadata for a key;
   already-resolved results stay fixed. Capture-truth correctness requires
   immutable, truthful metadata. First-writer-wins alone would not authenticate
   the first value. Conflicting input is tested as a broken assumption, not
   silently excluded or presented as supported behavior.
3. **Resource limits:** count retained metadata entries and event records
   (including unresolved records). Compare the core's unbounded admission with a
   proposed policy that explicitly rejects new entries at capacity, accepts
   duplicates, and never silently evicts accepted work. Rejected inputs carry no
   eventual-resolution guarantee. Counts are logical entries, not byte limits.
4. **Crashes:** after an accepted delivery, retained event identity and results
   should survive if crash recovery is promised. Volatile storage cannot meet
   this contract. The alternative assumes each delivery atomically commits the
   complete backend state to durable storage; restart restores it. This is a
   storage assumption, not a verified disk protocol.
5. **Sensor capture:** an event must retain the physical lifetime at the instant
   of capture. Compare a coherent snapshot with looking up identity later, after
   PID reuse. The oracle observes physical state independently of the sensor.
   This checks the sensor contract, not real kernel instrumentation.
6. **Physical boots:** reboot terminates processes and resets incarnation
   allocation. Actual boot identities never repeat in the model. Detection may
   lag. A stale cached boot ID must not masquerade as the current boot; compare
   that failure with reading the actual boot identity in the capture snapshot.
7. **Concurrent calls:** split activity lookup, pending registration, and result
   publication from metadata insertion, pending resolution, and cleanup. At
   quiescence, both delivered inputs must produce a resolved result. Compare
   arbitrary interleaving with a lock covering each whole delivery operation.
   The core requires serial calls; the new Go `Backend` implements the modeled
   whole-call lock contract.
8. **Retention:** explicitly advance logical time, expire cached metadata and
   finalize overdue pending events as unresolved. Retain published results and
   event identities across expiry and restart. Useful attribution requires
   overlapping leases and successful admission. See [retention](#retention).

All checks here are safety checks without fairness. Crash recovery, capture
completion, lock acquisition, metadata delivery, and retry of rejected work have
no unconditional liveness guarantee. The original fair/unfair configurations
remain the separate liveness demonstration.

## Models and observation boundaries

| Model | Bounded world | Checks and abstraction |
|---|---|---|
| [Conflicts.tla](Conflicts.tla) | Two full process identities `A/B`, two event IDs, two workloads. | Identity fields are opaque here; the original model checks their tuple semantics. Events are already captured in a fixed private `Truth` ledger. Activities can repeat with a different key. Metadata can change workload after an initially truthful delivery. Backend decisions use delivered values; `Truth`, `World`, `firstKey`, and `published` are source/oracle/audit data, not backend lookup sources. |
| [Storage.tla](Storage.tla) | Two identities, two events, two workloads; capacity one metadata entry and one event record. | Truthful inputs only. Separate counters include all retained identities/events, whether resolved or pending. `everAccepted` and `published` are unbounded-in-time but finite-domain observer ledgers, not backend storage. `reply` exposes admission/rejection. Durable mode retains the same logical tables across crash/restart, with no partial transaction. |
| [Capture.tla](Capture.tla) | One PID, two physical boots, two incarnations per boot, one capture. | Start requires a free PID; capture requires a live process; reboot kills it and resets allocation. `DetectBoot` may lag. The oracle records physical identity at the event; later sensor reporting uses its own snapshot or a fresh lookup. This checks identity provenance before backend delivery. |
| [Concurrent.tla](Concurrent.tla) | One activity call and one matching metadata call, three steps each. | Checks useful attribution after both calls return. Independent atomic reads/writes are an idealization that already admits a lost-resolution bug. Whole-call locking removes those interleavings; it is not a proof of linearizability for the complete Go API. |
| [Retention.tla](Retention.tla) | Two complete identities sharing a PID, two events, time 0–2, both TTLs one, capacities one each. | A valid capture prefix precedes delivery. Checks exact-key truth, stable published answers, terminal expiry, conditional usefulness, capacity and deadlines across atomic delivery, clock, crash and restart operations. |

`Storage.Next` constructs truthful input from the fixture's `World` and `Truth`,
corresponding to the source of a delivery, not a backend query of hidden truth.
`Activity` and `Meta` use explicit message arguments and retained backend state.
No backend uses
the private event ledger to repair a missing or corrupt input. The capture model
assumes a coherent physical snapshot in its passing variant; it checks that later
exit/reuse/reboot cannot change the saved identity, not that a real sensor can
obtain such a snapshot.

The concurrency counterexample is meaningful even under idealized atomic memory
accesses. Actual concurrent calls to the original Go `Attributor` are unsupported
and also involve unsynchronized maps. The [Go memory model](https://go.dev/ref/mem)
requires synchronization for shared concurrent access. The new Go `Backend`
serializes all methods, including `Result`; this small model checks only the
two delivery calls. See its [implementation limits](../../simulation/BOUNDARIES.md).

## Reproduce

Use the same pinned TLC jar and Java setup as the [original model](../README.md#reproduce).
From the repository root:

```sh
export TLA_JAR=/tmp/pid-attribution-tla2tools-1.7.4.jar
sh formal/extensions/check.sh
```

The checker exits **0** only if all eighteen configurations have their expected
outcome. It checks the named invariant for intentional failures, so a parser,
setup, type-invariant, or unrelated property error cannot substitute for the
expected counterexample. Each configuration is also independently runnable:

```sh
sh formal/extensions/run.sh replay                 # 0
sh formal/extensions/run.sh limits-reject          # 0
sh formal/extensions/run.sh crash-durable          # 0
sh formal/extensions/run.sh capture-snapshot       # 0
sh formal/extensions/run.sh concurrent-serialized  # 0

# Intentional failures: each exits 12 and prints its counterexample.
sh formal/extensions/run.sh replay-first-corrupt
sh formal/extensions/run.sh metadata-conflict
sh formal/extensions/run.sh limits-unbounded
sh formal/extensions/run.sh crash-volatile
sh formal/extensions/run.sh capture-late
sh formal/extensions/run.sh boot-stale
sh formal/extensions/run.sh concurrent-unlocked
```

`conflict-stability` is the sixth passing configuration. It deliberately checks
only type, first-origin, stable-result, and useful-attribution invariants with both
conflict switches enabled. It makes **no capture-truth claim**; the negative
configurations demonstrate why that stronger claim fails.

All runs use TLC 2.19 (v1.7.4 jar, revision 5a47802), one worker, breadth-first
search, `-fp 0 -seed 1 -Xmx1g`. Configurations disable deadlock reporting to permit
terminal finite fixtures; they add no fairness, state constraints, symmetry
reductions, or search-depth cutoff. Search files are temporary and removed by the
runner. For retained traces, redirect an individual run's output to a log file.

## Observed results and counterexamples

Run on 2026-10-24 with Temurin/OpenJDK 21.0.12.1 on macOS. Negative runs stop at
their first invariant violation; only passing runs complete their finite graphs.

| Configuration | Generated | Distinct | Queue left | Exit | Outcome |
|---|---:|---:|---:|---:|---|
| `replay` | 97 | 16 | 0 | 0 | Later conflicting replay preserves origin and capture truth. |
| `replay-first-corrupt` | 28 | 13 | 9 | 12 | `CaptureTruth` fails. |
| `metadata-conflict` | 70 | 22 | 10 | 12 | `CaptureTruth` fails. |
| `conflict-stability` | 2,449 | 173 | 0 | 0 | First origin and resolved results stay stable; truth is not checked. |
| `limits-unbounded` | 7 | 6 | 3 | 12 | `Capacity` fails. |
| `limits-reject` | 101 | 25 | 0 | 0 | Bounded admission, explicit responses, retained accepted work, and attribution pass. |
| `crash-volatile` | 11 | 10 | 7 | 12 | `AcceptedRetained` fails. |
| `crash-durable` | 175 | 42 | 0 | 0 | Storage invariants pass with atomic durability assumed. |
| `capture-late` | 40 | 31 | 13 | 12 | `CaptureIdentity` fails. |
| `boot-stale` | 36 | 27 | 12 | 12 | `CaptureIdentity` fails. |
| `capture-snapshot` | 184 | 91 | 0 | 0 | Snapshot keeps the original physical identity across lifecycle changes. |
| `concurrent-unlocked` | 45 | 29 | 6 | 12 | `QuiescentUseful` fails. |
| `concurrent-serialized` | 13 | 13 | 0 | 0 | Both call orders resolve the event after both calls complete. |
| `retention-pid-fallback` | 39 | 33 | 19 | 12 | `CaptureTruth` fails after expiry and PID reuse. |
| `retention-recompute` | 56 | 44 | 24 | 12 | `StableResults` fails on restart after history expires. |
| `retention-revive` | 69 | 52 | 25 | 12 | `TerminalExpiry` fails when late metadata revives an expired event. |
| `retention-unresolved` | 2 | 2 | 0 | 12 | `Useful` catches an implementation that always returns unresolved. |
| `retention` | 151 | 90 | 0 | 0 | All retention safety invariants pass. |

The counterexamples in plain language (excluding Init):

1. **Corrupt first replay, two transitions:** deliver E2 claiming A, then A's
   metadata. E2 resolves to A even though it was captured from B. First-arrival
   stability is not authentication.
2. **Conflicting metadata, three transitions:** deliver A/WA, overwrite it with
   A/WB, then deliver E1 from A. The key matches, but the workload is wrong.
3. **Capacity, two transitions:** accept unresolved E1, then unresolved E2. A
   capacity of one is exceeded even without any metadata arriving.
4. **Volatile crash, two transitions:** accept E1, then crash. The observer still
   knows E1 was accepted, but the backend has lost its record. Restart cannot
   reconstruct it without an additional replay or persistence contract.
5. **Late identity lookup, five transitions:** start incarnation 1, capture the
   event, exit, start incarnation 2, then finish the identity lookup. The reported
   key describes incarnation 2, while the event came from incarnation 1.
6. **Stale boot detection, four transitions:** reboot to boot 2, start a process,
   capture while the cached boot is still 1, then report. The event has the wrong
   scope even though its PID and incarnation look valid.
7. **Concurrent overwrite, six transitions:** activity sees no metadata and
   registers pending work; metadata inserts, scans, resolves, and clears that
   work; activity finally stores its earlier unresolved answer over the resolved
   result. Both calls have returned, but resolution has been lost.

The fixed configurations plus named expected failures are executable model
regressions. These traces were found by breadth-first TLC search, not by the Go
property generator's shrinker. They do not demonstrate a deployed system failure.

## Go tests transferred from the models

[formal_test.go](../../simulation/formal_test.go) exercises the actual Go attributor
without requiring Java or TLC. These are hand-translated traces and properties,
not an automatic refinement check.

| Model evidence | Go coverage |
|---|---|
| `conflict-stability`: resolved answers remain stable | `TestTLCResolvedResultSurvivesMetadataConflict` checks both activity-first and metadata-first resolution, followed by contradictory metadata and an activity replay. |
| `replay-first-corrupt`: first-arrival stability cannot authenticate origin | `TestTLCCaptureTruthWithUntrustedInputs/corrupt-first-activity` delivers E2 claiming A, then A's metadata, while its private capture truth says B. |
| `metadata-conflict`: overwrite corrupts a later attribution | `TestTLCCaptureTruthWithUntrustedInputs/conflicting-metadata` delivers A/WA, then A/WB, then E1. Private capture truth remains A/WA. |
| `replay`: first delivered origin survives conflicting replay | Existing `TestEventIDKeepsFirstOrigin`. |
| Fresh boot scope isolates reused PID/incarnation values | Existing `TestRebootUsesFreshScope`; this tests backend handling of supplied identities, not physical boot detection. |

The two untrusted-input assertions intentionally fail with the current corrected
implementation. They check `resolved => answer equals capture truth`, so an honest
unresolved answer would satisfy this safety assertion. The default suite runs
`TestTLCInputAssumptionCounterexamples`, which starts each assertion in an isolated
Go test process and requires the named test to fail specifically at the capture
truth assertion. Missing tests, setup/build errors, or unrelated failures do not
count. The direct demonstration is otherwise skipped to keep `go test ./...`
passing. No production fault switch is added.

The stability regression is a characterization of current behavior under invalid
input, not a promise that conflicting metadata is supported. Its targeted mutation
removes pending-event cleanup: an activity-first result then changes from WA to WB
when contradictory metadata arrives. The regression catches that mutation. If a
future input-validation policy rejects conflicts or changes their handling, review
these characterizations and the model together; do not preserve an unsafe behavior
just to retain an expected failure.

From `simulation/`, run the transferred tests before the generated property suite:

```sh
# Passing regression and automated expected-counterexample checks (exit 0).
go test . -run '^(TestTLCResolvedResultSurvivesMetadataConflict|TestTLCInputAssumptionCounterexamples)$' -count=1 -v
go test . -run '^TestTargetedMutations/keep-resolved-events-pending$' -count=1 -v

# Direct capture-truth demonstrations: both subtests intentionally fail (exit 1).
PID_ATTRIBUTION_UNTRUSTED_DEMO=1 go test . -run '^TestTLCCaptureTruthWithUntrustedInputs$' -count=1 -v

# Passing full suite; the preceding environment assignment was command-local.
go test ./... -count=1 -rapid.seed=11 -rapid.nofailfile
```

The regression, parent checks, and mutation check passed; both direct
demonstrations failed at the expected safety assertion. No goroutines were added.
Subsequent [boundary implementations and tests](../../simulation/BOUNDARIES.md)
cover capacity rejection, snapshot recovery, capture adaptation, boot-ID reading,
and serialized delivery. A real event source and actual reboot integration remain
future work. The original core and its intentional counterexamples remain intact.

## Retention

The [Go retention contract and experiment](../../simulation/RETENTION.md) define
the policy before implementation. `Retention.tla` starts after a valid physical
prefix: A starts, captures E1/E2, exits, and B reuses the PID with a distinct full
identity. A's metadata is initially cached at time zero. Only truthful messages
are delivered. `Truth` is an immutable capture ledger used to supply inputs and
check answers; delivery actions use their arguments and retained state.

Each metadata admission gets a lease; a later delivery after expiry may get a
new lease. Existing duplicates do not refresh deadlines. Pending events that
reach their deadline become terminal unresolved. All accepted event IDs and
published answers are retained. Duplicate deliveries and capacity rejections
have no state effects, represented by stuttering. `published` is an audit ledger
for stable answers; `dead` is backend terminal-expiry state. Restart restores
durable state, with logical time frozen during downtime. Workload payloads and
individual identity fields are abstracted into the complete symbolic key.

The corrected configuration checks `TypeOK`, `CaptureTruth`, `StableResults`,
`TerminalExpiry`, `Useful`, `Capacity`, and `Expiry`. `Useful` only applies to an
active accepted event with matching metadata still cached. Once resolved,
`StableResults` continues to require that answer after metadata expiration.
An implementation that always returns unresolved therefore cannot pass.

With the pinned TLC setup above, run from the root:

```sh
sh formal/extensions/run.sh retention-pid-fallback # exit 12: CaptureTruth
sh formal/extensions/run.sh retention-recompute    # exit 12: StableResults
sh formal/extensions/run.sh retention-revive       # exit 12: TerminalExpiry
sh formal/extensions/run.sh retention-unresolved   # exit 12: Useful
sh formal/extensions/run.sh retention              # exit 0
```

The shortest counterexamples, excluding Init:

1. **PID fallback, three transitions:** tick to one, expiring A; cache B; deliver
   E1 captured from A. Fallback to the retained holder of that PID returns B.
2. **Recompute after recovery, four transitions:** resolve E1 from A; expire A's
   metadata; crash; restart and recompute. E1 loses its published answer.
3. **Revive, four transitions:** expire A's metadata; accept E1 unresolved; expire
   E1 at tick two; admit A's metadata. A faulty resolver revives terminal E1.
4. **Always unresolved, one transition:** accept E1 while A's metadata is cached,
   but return unresolved. The narrowed usefulness contract still catches this.

The matching Go regressions and source-overlay mutations are listed in
[RETENTION.md](../../simulation/RETENTION.md#implementation-and-evidence).
These are hand-translated model traces, not an automated refinement proof.

No temporal property or fairness is added to this bounded safety check. Eventual
resolution requires delivery inside the admission-relative lease overlap,
available capacity and successful commits. Weak fairness alone allows delivery
after a finite deadline and is insufficient. Eventual resolved-or-expired status
needs a finite pending TTL, clock advancement to the deadline, and functioning
storage/recovery. This is a conditional engineering argument, not a liveness
claim established by these finite safety configurations.

## Limits and liveness

These are separate small models, not a combined cross-product check or a
refinement proof against Go. For example, a crash while a concurrent operation
holds a lock, a corrupted replay after record eviction, torn disk writes, and
resource exhaustion during boot recovery are not checked together. All named
bounds matter; finite fingerprints and abstraction are further limits.

The original `Storage.tla` policy deliberately retains accepted work rather than reclaiming it.
It can reject new work forever once full; it is a bounded-memory safety example,
not a usable retention/reclamation design. It does not bound payload bytes,
transport queues, journals, or process memory. Accepted events may also wait
forever if their metadata is rejected. An eventual-resolution promise would need
reserved capacity or admissible retries, in addition to eventual delivery.
The retention model reclaims metadata slots but preserves all event IDs and
answers, so event capacity can still become permanently full.

Durability assumes atomic commits and excludes disk corruption, partial writes,
acknowledgement loss, and replay after an ambiguous commit. Recovery progress
would need eventual restart and enough time without another crash. Serialized
delivery safety does not imply fair lock acquisition or completion. Capture needs
eventual completion and a trustworthy, nonrepeating boot identity; delayed boot
detection alone supplies neither coherence nor uniqueness.

The model checks do not verify an actual kernel sensor, boot-ID source, disk, or
concurrent Go wrapper. The subsequent Go implementations have separate test
evidence, not a model-refinement proof. The original no-fairness liveness failure
remains valid.
