# Attribution boundary models

These focused models extend the original [attribution model](../README.md).
They separate existing Go behavior from proposed environment and storage
contracts. They do not implement these features in Go, nor compose all seven
complications into a single verified system.

## Contracts defined before modeling

1. **Conflicting activity replays:** the first delivered event ID fixes its key;
   subsequent conflicting payloads must not rebind it or change a resolved result.
   Agreement with capture truth additionally requires the first payload to be
   truthful. A forged first payload cannot be recognized from the ID alone.
2. **Conflicting metadata:** the current Go map overwrites metadata for a key;
   already-resolved results stay fixed. Capture-truth correctness requires
   immutable, truthful metadata. First-writer-wins alone would not authenticate
   the first value. Conflicting input is tested as a broken assumption, not
   silently excluded or presented as supported behavior.
3. **Resource limits:** count retained metadata entries and event records
   (including unresolved records). Compare today's unbounded admission with a
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
   Current Go requires serial calls; the lock is a proposed caller/wrapper contract.

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

`Storage.Next` constructs truthful input from the fixture's `World` and `Truth`,
corresponding to the source of a delivery, not a backend query of hidden truth.
`Activity` and `Meta` use explicit message arguments and retained backend state.
No backend uses
the private event ledger to repair a missing or corrupt input. The capture model
assumes a coherent physical snapshot in its passing variant; it checks that later
exit/reuse/reboot cannot change the saved identity, not that a real sensor can
obtain such a snapshot.

The concurrency counterexample is meaningful even under idealized atomic memory
accesses. Actual concurrent calls to the current Go implementation are unsupported
and also involve unsynchronized maps. The [Go memory model](https://go.dev/ref/mem)
requires synchronization for shared concurrent access. A real wrapper would need
to serialize all methods, including `Result`; this small model checks only the
two delivery calls. No goroutines, locks, storage engines, or sensors were added
to the Go implementation.

## Reproduce

Use the same pinned TLC jar and Java setup as the [original model](../README.md#reproduce).
From the repository root:

```sh
export TLA_JAR=/tmp/pid-attribution-tla2tools-1.7.4.jar
sh formal/extensions/check.sh
```

The checker exits **0** only if all thirteen configurations have their expected
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

## Limits and liveness

These are separate small models, not a combined cross-product check or a
refinement proof against Go. For example, a crash while a concurrent operation
holds a lock, a corrupted replay after record eviction, torn disk writes, and
resource exhaustion during boot recovery are not checked together. All named
bounds matter; finite fingerprints and abstraction are further limits.

The storage policy deliberately retains accepted work rather than reclaiming it.
It can reject new work forever once full; it is a bounded-memory safety example,
not a usable retention/reclamation design. It does not bound payload bytes,
transport queues, journals, or process memory. Accepted events may also wait
forever if their metadata is rejected. An eventual-resolution promise would need
reserved capacity or admissible retries, in addition to eventual delivery.

Durability assumes atomic commits and excludes disk corruption, partial writes,
acknowledgement loss, and replay after an ambiguous commit. Recovery progress
would need eventual restart and enough time without another crash. Serialized
delivery safety does not imply fair lock acquisition or completion. Capture needs
eventual completion and a trustworthy, nonrepeating boot identity; delayed boot
detection alone supplies neither coherence nor uniqueness.

No actual kernel sensor, boot-ID source, disk, or concurrent Go wrapper was
implemented or verified. Integration would require concrete contracts and tests
at those boundaries. The original no-fairness liveness failure remains valid.
