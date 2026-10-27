# Bounded TLA+ model of event attribution

[Attribution.tla](Attribution.tla) models valid process lifetimes, capture-time
truth, unordered message delivery, and backend attribution. TLC first found a
false attribution with the PID-only algorithm. The corrected algorithm then
passed the configured safety invariants. Separate liveness runs failed without
delivery fairness and passed with it.

[Boundary models](extensions/README.md) additionally explore conflicting replays
and metadata, capacity, crashes, capture races, physical boots, and concurrent
calls. They distinguish current behavior from proposed contracts and include
expected counterexamples.

These are bounded model-checking results, not a proof that the Go implementation
refines the model or that arbitrary system sizes are correct.

## State and observation boundary

| State | Role |
|---|---|
| `started`, `live`, `worldMetadata` | Physical world: start only on a free PID within a scope, capture only while alive, and exit before PID reuse. Incarnations never repeat within a scope. |
| `oracle` | Private event-ID-to-origin/workload ledger written at capture time. |
| `sent`, `pending` | Messages created by the world and those awaiting delivery. Any pending message can be selected. |
| `seenMetadata` | Audit ledger of metadata ever delivered, used only for checking the contract. |
| `metadata`, `received`, `results` | Backend state: delivered metadata, received event keys, and current attribution results. |

`Deliver` chooses attribution using only its message and backend state. It never
reads `oracle`, `worldMetadata`, or `seenMetadata` to resolve an event. The oracle
copies truth from the world at capture, separately from the public activity
payload. This preserves the Go harness's independent observation boundary.

With `Faulty = TRUE`, metadata replaces the numeric PID's current entry, an exit
deletes that PID's entry, and activity looks up only PID. Metadata does not revisit
unresolved events. With `Faulty = FALSE`, metadata is retained by full key and
resolves received unresolved events with that exact key. Exits leave history
intact, and activity replays preserve the stored result.

`results[e] = None` represents an unresolved result. `received[e] = None` also
distinguishes an undelivered event, corresponding to the boolean returned by Go's
`Result`. The model scans received events on metadata delivery instead of modeling
Go's pending-event index: this abstracts the data structure, not the observable
delivery-call result. Each delivery is one atomic transition.
Executable labels and activity kinds are omitted because attribution does not
inspect them in the Go implementation.

`Replay` requeues an already-sent identical message. A set coalesces simultaneous
identical copies; repeated delivery is represented by requeueing after consumption.
The corrected checks allow unbounded repetition within a finite state graph.
Conflicting metadata and conflicting event-ID payloads are excluded. The Go
first-delivery-wins rule for conflicting IDs remains covered only by its Go tests.

## Properties and configurations

The checks start with safety, independently of any fairness assumption:

| Invariant | Meaning |
|---|---|
| `TypeOK` | State stays in the finite domains and no two live lifetimes share a scope/PID. |
| `NoFalseAttribution` | Every resolved key and workload equals the private capture-time truth. |
| `UsefulAttribution` | If an activity and its matching metadata have arrived, the activity is resolved now. |
| `HonestUncertainty` | A resolved result must come from delivered metadata matching the event's exact key. |

Useful attribution is a state invariant: a delivery call must finish the resolution
it makes possible. It does not by itself require undelivered messages to arrive.

Every configuration uses PID set `{7}`, workloads `{WA, WB}`, and model value
`None`. Workload is chosen independently for each lifetime and then stays fixed;
two lifetimes may belong to the same workload. The remaining bounds are:

| Configuration | Scopes | Incarnations per scope | Event IDs | Algorithm | Replay | Specification / checks |
|---|---|---|---|---|---|---|
| [faulty.cfg](faulty.cfg) | `{lab}` | `{1, 2}` | `{E1, E2}` | PID-only | No | `Spec`; type and no-false-attribution invariants |
| [corrected.cfg](corrected.cfg) | `{lab}` | `{1, 2}` | `{E1, E2}` | Full key | Yes | `Spec`; all four invariants |
| [scopes.cfg](scopes.cfg) | `{bootA, bootB}` | `{1}` | `{E1, E2}` | Full key | Yes | `Spec`; all four invariants |
| [unfair.cfg](unfair.cfg) | `{lab}` | `{1, 2}` | `{E1}` | Full key | Yes | `Spec`; all four invariants plus `EventualResolution` |
| [fair.cfg](fair.cfg) | `{lab}` | `{1, 2}` | `{E1}` | Full key | Yes | `FairSpec`; all four invariants plus `EventualResolution` |

The scope configuration checks equal PID/incarnation values across distinct
identity boundaries. It permits independent physical lifetimes in those scopes;
it does not implement a host reboot action or validate boot-identity capture.

All checks use breadth-first model checking, one worker, fingerprint polynomial
index 0, seed 1, and `-Xmx1g`. There is no simulation mode, symmetry reduction,
state constraint, or search-depth cutoff. The finite constants bound the graph.
`CHECK_DEADLOCK FALSE` allows intentional terminal states after the finite world
has exhausted its work. TLA+ stuttering is allowed; disabling deadlock reporting
does not add delivery fairness or establish progress.

## Reproduce

The checked artifact is the official [TLA+ v1.7.4 release](https://github.com/tlaplus/tlaplus/releases/tag/v1.7.4),
whose jar reports **TLC 2.19, 08 August 2024, revision 5a47802**. It is an optional
verification tool, not a Go dependency, and is not committed to this repository.
From the repository root, with Java available:

```sh
export TLA_JAR=/tmp/pid-attribution-tla2tools-1.7.4.jar
curl -fL -o "$TLA_JAR" https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar
shasum -a 256 "$TLA_JAR"
```

The downloaded jar's SHA-256 was
`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`;
its SHA-1 also matched the release's published
`bee4a54f3ee3d4afc347c3240ec2d9e93b075104`.

Run safety first, then liveness separately:

```sh
sh formal/run.sh faulty     # Expected exit 12: NoFalseAttribution violation.
sh formal/run.sh corrected  # Expected exit 0: all configured invariants pass.
sh formal/run.sh scopes     # Expected exit 0: all configured invariants pass.
sh formal/run.sh unfair     # Expected exit 13: temporal property violation.
sh formal/run.sh fair       # Expected exit 0: conditional eventual resolution.
```

[run.sh](run.sh) forwards TLC's exit status and keeps search files in a temporary
directory removed on exit. It uses an absolute `TLA_JAR` path. Setup/parser errors
are not expected counterexamples. In the execution sandbox, TLC's local RMI
listener required permission to run outside the sandbox even with one worker.

## Observed outcomes

Run on 2026-10-24 with Eclipse Temurin/OpenJDK 21.0.12.1 on macOS. The faulty check
was executed before checking the corrected algorithm.

| Configuration | Generated states | Distinct states | Queue remaining | Exit | Outcome |
|---|---:|---:|---:|---:|---|
| `faulty` | 862 | 552 | 345 | 12 | `NoFalseAttribution` violated; stopped at the first counterexample. |
| `corrected` | 85,541 | 14,689 | 0 | 0 | Completed; no configured invariant violation. |
| `scopes` | 97,013 | 16,453 | 0 | 0 | Completed; no configured invariant violation. |
| `unfair` | 10,541 | 2,161 | 0 | 13 | Safety invariants held; `EventualResolution` violated. |
| `fair` | 10,541 | 2,161 | 0 | 0 | Completed; invariants and `EventualResolution` passed under fairness. |

The faulty trace has seven states including Init, hence six transitions. The
completed corrected and scope graphs have reported depth 17; the fair graph has
depth 14. These are observed graph depths, not configured limits. TLC uses state
fingerprints; its reported optimistic collision estimates were `5.6E-11` for the
corrected run, `7.2E-11` for scopes, and `9.8E-13` for the fair run.

### Safety counterexample and Go regression

| Transition | Action |
|---|---|
| 1 | Start A with key `(lab, 7, 1)` and workload `WA`; queue metadata. |
| 2 | A captures E1; record its true origin and queue activity. |
| 3 | A exits physically; queue its exit notification. |
| 4 | Start B with key `(lab, 7, 2)` and workload `WA`; queue metadata. |
| 5 | Deliver only B's metadata. |
| 6 | Deliver E1, still carrying A's key. |

The oracle says E1 came from A, but the PID-only backend returns B. Both workloads
are `WA`: workload equality alone would miss the wrong process lifetime. A's
metadata is still queued, so the corrected result at this point is **unresolved**.
The shorter trace needs neither A's metadata nor its exit notification delivered.

`TestTLCDelayedEventBeforeOriginMetadata` in
[attributor_test.go](../simulation/attributor_test.go) preserves these six
transitions as an ordinary regression. From the repository root:

```sh
cd simulation
go test . -run '^TestTLCDelayedEventBeforeOriginMetadata$' -count=1 -v
PID_ATTRIBUTION_FAULTY=1 go test . -run '^TestTLCDelayedEventBeforeOriginMetadata$' -count=1 -v
```

The corrected version passed; the faulty version exited 1 with incarnation 2
where unresolved was required. The Go production code was not changed.

## Liveness is conditional

`EventualResolution` says: for each event, once it has been captured, it eventually
has a resolved result. `Spec` alone permits infinite stuttering. The `unfair` run
found a finite prefix ending with E1 received but unresolved, its matching metadata
still pending, and then stuttering forever. This is a liveness failure without a
safety failure.

`FairSpec` adds weak fairness to each individual `Deliver(m)` action. Once a message
is queued, delivery remains enabled until it is delivered. Per-message fairness
therefore excludes starvation of that message, even if other messages are replayed.
This is stronger than merely allowing the transport to keep doing some work.
See [Lamport, Specifying Systems, chapter 8](https://lamport.azurewebsites.net/tla/book-02-03-04.pdf)
for weak fairness semantics.

The successful fair check depends jointly on these assumptions:

1. Matching immutable metadata exists: `Start` always queues it before an event
   can be captured. Capture queues the activity itself.
2. Neither queued message can be dropped or permanently withheld. The model has
   no drop action, and weak fairness requires eventual delivery while enabled.
3. Metadata, received unresolved events, and results are retained without expiry.
4. Keys correctly identify unique lifetimes and event IDs identify unique logical
   events; replays are nonconflicting.

Fairness is applied uniformly to metadata, activity, and exit deliveries; eventual
resolution needs the metadata and activity deliveries in particular. No fairness
is imposed on starting, capturing, exiting, or replaying: the claim concerns events
that actually get captured. No delivery-time bound is promised. Fairness filters
infinite behaviors, not reachable states, which explains the identical fair and
unfair state counts.

Permanent loss, metadata that never exists, eviction, or unfair delivery would
invalidate this liveness argument. The fair result must not be read as an
unconditional eventual-resolution guarantee for telemetry ingestion.

### What can be hardened without fairness?

The unfair counterexample does not justify a change to the attribution algorithm.
In its last state, the backend has E1 and another lifetime's metadata, but never
receives E1's matching metadata. Returning a resolved workload would guess missing
information. Even a retry worker cannot ensure progress if it never runs or its
responses never arrive. Adding retries would require a transport contract and
would still leave an execution/delivery assumption.

The existing implementation provides a useful guarantee without fairness: whenever
both inputs have arrived, resolution completes within the delivery call that makes
it possible. `UsefulAttribution` checks this in the safety configurations, which
have no fairness assumption. Missing inputs remain explicitly unresolved, and
unrelated events with available metadata can resolve. This requires retained
metadata and pending events, consistent metadata, and unique identities. Unbounded
retention has a memory cost. The subsequent [retention extension](extensions/README.md#retention)
adds expiry under a narrower usefulness contract, preserving answers directly
across recovery. This original model retains its unlimited-duration assumptions.

`TestTLCUnfairDeliveryAndRecovery` in
[attributor_test.go](../simulation/attributor_test.go) preserves the unfair trace's
backend-visible prefix. It then checks that a known event can resolve while E1
waits, that replaying E1 cannot replace its missing metadata, and that delivering
the matching metadata resolves E1 immediately without redelivering its activity.
The recovery continuation is an additional scenario, not the infinite unfair
behavior. A finite Go test cannot demonstrate infinite starvation.

```sh
cd simulation
go test . -run '^TestTLCUnfairDeliveryAndRecovery$' -count=1 -v
```

This regression passed with the existing algorithm; only its API comments were
clarified. No goroutine or retry loop was added. `unfair.cfg` deliberately remains
an expected temporal failure (exit 13). Making it pass requires changing the
environment assumptions or the requested liveness guarantee, not weakening the
safety checks or treating an unresolved event as resolved.

## Limits and relationship to Go

TLC completed the corrected configurations' finite graphs; it did not establish correctness for
arbitrary numbers of scopes, PIDs, lifetimes, workloads, or events. The two safety
configurations do not jointly cover a larger Cartesian product of their bounds.
This base model omits conflicting replays, conflicting metadata, resource limits,
crashes, real sensor capture, concurrent delivery calls, and physical boot detection.
The separate [boundary models](extensions/README.md) explore abstractions of these
complications; they are not a combined-system check or a real sensor integration.
An incarnation is a unique label, not a modeled timestamp or monotonic clock.

The model and Go implementation share the observation boundary and attribution
contract, but there is no implementation-refinement proof or automatic equivalence
check. Translating TLC's counterexample into a passing/failing Go regression is
concrete alignment evidence, not proof of refinement. The generated Go tests
remain sampled tests; bounded TLC results do not make them exhaustive.

Tool references: [official TLC command-line documentation](https://github.com/tlaplus/tlaplus/blob/master/general/docs/current-tools.md)
and [official extension's configuration reference](https://github.com/tlaplus/vscode-tlaplus/blob/master/resources/knowledgebase/tlc-config-files.md).
