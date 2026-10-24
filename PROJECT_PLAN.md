# PID reuse and trustworthy event attribution

Build a small Go project that reproduces incorrect attribution of a delayed event after PID reuse, finds the error through property-based testing, and checks a corrected implementation against an independent model. Then model the same safety property in TLA+.

The central question is: **Can we associate an event with the process that generated it, even when that process has exited and another process now has the same PID?**

This is a learning project motivated by runtime security telemetry, not a description of Upwind's implementation. Complete one milestone per Codex session and explain the counterexamples before moving on.

## Scope and environment

Start with a deterministic, in-memory simulation. Use Go's standard `testing` package and Rapid (`pgregory.net/rapid`) for property-based tests. Rapid is a test-only dependency; keep its imports in test files. No production dependencies, live sensor, database, Kubernetes cluster, or Linux privileges are needed for the core project. It can run on macOS. At setup, select and pin a tagged Rapid release compatible with the installed Go toolchain.

Rapid provides generated inputs, automatic shrinking, stateful tests, and persisted failure replay. Use `rapid.Check` for properties and `rapid.T.Repeat` for action sequences with invariant checks. These cover the requirements of this project without a Python-to-Go transport layer. See the [Rapid documentation](https://pkg.go.dev/pgregory.net/rapid).

Keep the first model and implementation in one goroutine, using an explicit queue to simulate different delivery schedules. Draw generated choices through Rapid and sort map-derived candidate lists before selecting from them, so Go map iteration order does not undermine replay. Goroutine scheduling and data-race testing are separate later concerns.

Use logical steps instead of wall-clock time and sleeps. Deliberately choose a tiny PID pool so reuse happens frequently. The estimate is five sessions of roughly one to two hours for the core work, with separate sessions for extensions.

Create a dedicated project directory when starting implementation. Keep this plan and the existing employment documents intact.

## The first counterexample

| Step | What really happens | What the backend receives |
|---|---|---|
| 1 | Process A starts with PID 7 | Metadata identifying A |
| 2 | A generates event E | Nothing yet; E is delayed |
| 3 | A exits | A's exit notification |
| 4 | Process B starts with PID 7 | Metadata identifying B |
| 5 | No new process activity | Event E from A finally arrives |

A backend that asks “who currently has PID 7?” attributes E to B.

The required answer is A if A's metadata is available. If identity cannot be resolved, the result must explicitly remain unresolved. Guessing B is incorrect.

## Specify the information available

For the first version, use these conceptual records:

| Record | Fields and meaning |
|---|---|
| Process identity | `ProcessKey(scope, pid, incarnation)` |
| Scope | Initially one fixed value; later distinguish tenant, host, boot, and PID namespace lifetimes |
| Incarnation | A synthetic counter that never repeats within the scope, even after PID reuse |
| Metadata | Process key, executable label, and immutable workload identity |
| Activity event | Unique event ID, process key captured at event creation, and a small activity kind |
| Attribution | Event ID mapped to an exact process key and its workload, or explicitly unresolved |

The simulation assumes a process belongs to one workload throughout its lifetime. It does not model threads, `exec`, or workload migration initially.

**The incarnation is an explicit model assumption, not a claim that Linux exposes a universal generation counter.** A real sensor would need a justified identity scheme, including its namespace, reboot, capture-race, and collision assumptions.

Capture the full key at event creation. Looking up identity later using only the PID recreates the original race.

Distinguish two kinds of uncertainty:

- An event has a full process key but its metadata has not arrived. It can be resolved later.
- An event has only a PID and insufficient historical information. More software cleverness cannot necessarily recover its origin.

Construct two histories with identical PID-only observations at the attribution interface but different correct origins. If the interface cannot distinguish them, it cannot guarantee the right answer for both. Do this thought experiment before implementing the fix.

## Keep the model independent of the implementation

Use three separate components:

1. **World model:** tracks actual process lifetimes and records the true origin of each event when it is created.
2. **Transport model:** queues metadata, exits, and activity events; allows explicit delivery choices.
3. **Attributor:** sees only delivered messages and returns attribution results.

Keep the oracle's `event_id -> true origin and workload` ledger private to the test harness. Never pass that ledger to the attributor. The attributor may see the event's public process key, but it must obtain workload metadata through delivered messages.

The oracle must use capture-time truth, not the attributor's current PID table or shared attribution helper. A test that copies the implementation's lookup can reproduce its bug and still pass.

Physical lifetimes obey causality: a process must be alive to generate an event; a PID can be reused only after exit. Delivery order is separate and may differ from capture order.

## Properties to check

The first five properties apply to the core model, where history and unresolved events are retained without expiration.

| Property | Exact expectation |
|---|---|
| No false attribution | Every resolved event has the same process key and workload as the oracle recorded at capture time |
| Useful attribution | If both the event and its matching metadata have been delivered, the result is resolved by the end of that delivery call |
| Honest uncertainty | An event whose matching metadata is unavailable remains unresolved; another lifetime's metadata cannot fill the gap |
| Historical identity | Exiting or reusing a PID cannot change the attribution of a previously resolved event |
| Delivery-order independence | Once the same complete message set is delivered, final attribution agrees across delivery orders; intermediate unresolved results may differ |
| Scope isolation | In the later multi-scope model, matching numeric PIDs and incarnation counters in different scopes cannot cross-match |

The useful-attribution property prevents an implementation from “passing” safety by returning unresolved for everything.

Delivery-order independence assumes immutable, nonconflicting metadata and no expiration. Do not claim it for arbitrary security rules that depend on event order.

## Milestone 1 Explain and reproduce the bug

Create the minimal records, a deliberately faulty attributor using the current PID mapping, and the five-step scenario above. Deliver A's metadata before the delayed activity so the correct answer is unambiguous.

Write one ordinary Go test asserting that E belongs to A. Run it against the faulty implementation and inspect the failure. Keep faulty code clearly separated as a learning example.

Completion:

- The failure reports A as expected and B as actual.
- You can explain why “look up the PID more carefully” is insufficient.
- The counterexample is recorded in a short Markdown note.

Codex prompt:

> Work only on milestone 1 of this plan. Before editing, explain the records and assumptions. Implement the smallest deterministic PID-reuse counterexample and a deliberately faulty attributor. Run the narrow test and show why it fails. Do not fix the implementation yet.

## Milestone 2 Generate and shrink counterexamples

Begin with a small Rapid test varying the reused PID, distinct incarnation values, and workload identities while retaining the causal five-step skeleton. Then introduce variable delivery order.

Build a state-machine test using `rapid.T.Repeat`, with actions such as:

- Start a process on a free PID, queuing its metadata.
- Generate an activity event for a live process, recording private oracle truth.
- Exit a live process, freeing its PID and queuing an exit notification.
- Deliver one selected queued message.

Use a PID pool such as `{1, 2, 3}`. Generate meaningful choices from current state instead of discarding most examples with broad filters. Check the safety and useful-attribution properties after every delivery.

Rapid should find and shrink a failing action sequence. The reduced example need not reproduce the hand-written trace exactly, and shrinking does not promise a globally minimal counterexample.

Completion:

- A generated counterexample fails against the faulty implementation.
- The failure shows capture origin, delivery order, expected identity, and actual result.
- Turn the reduced trace into an ordinary Go regression test; do not rely only on Rapid's persisted failure files.

Codex prompt:

> Implement milestone 2. Start with a constrained property test, then add stateful generation. Keep physical process transitions valid while varying message delivery. Make the oracle independent. Explain the shrunk failure in plain language and preserve it as a regression test.

## Milestone 3 Correct the implementation

Implement an attributor indexed by full process identity. Retain historical metadata after exit. Store unresolved events under their exact process keys and reconsider them when matching metadata arrives.

Treat an exit as a fact about one process lifetime. It must not delete another lifetime's metadata or make delayed events unresolvable when their metadata is retained.

Run the deterministic regression first, then the property suite using `go test`. Compare both implementations under the same contract. In normal project checks, test the corrected version; expose the deliberately failing demonstration through an explicit opt-in flag or environment variable so `go test ./...` passes by default.

Completion:

- The corrected version passes the regression and generated tests.
- Activity-before-metadata resolves when matching metadata arrives.
- A stale exit notification cannot disturb a newer lifetime.
- The original faulty version still fails the same behavioural contract.

Codex prompt:

> Implement milestone 3 with the smallest change justified by the counterexample. Use the complete capture-time identity and retain historical metadata. Run the regression before the property suite. Explain which guarantees depend on retention and identity uniqueness.

## Milestone 4 Challenge the specification

Add one complication at a time, first stating which property it should preserve:

- Duplicate message delivery, with event IDs defining the deduplication contract.
- Equal PID and incarnation values across separate scopes.
- A host reboot introducing a fresh boot identity.
- Metadata that is never delivered: unresolved must remain explicit.
- Delayed old metadata and exits arriving after several PID reuses.

Keep these as separate changes so a new counterexample has an understandable cause.

Probe the tests with small deliberate faults: ignore incarnation, ignore scope, delete history on exit, and always return unresolved. Each applicable mutation should be caught by a named test. This checks the strength of the suite rather than merely increasing example counts.

Completion: document which property catches each mutation and what the tests still do not establish.

Codex prompt:

> Work through milestone 4 one complication at a time. Define each added contract before changing code. Demonstrate that targeted faulty variants are caught, including an implementation that always returns unresolved.

## Milestone 5 Write up the result

Create a concise project README and a findings note covering:

- The original failure trace and the smallest generated counterexample.
- The information required to distinguish process lifetimes.
- The safety property and the useful-attribution property.
- Why the oracle is independent.
- The corrected design and its retention assumptions.
- What passing property-based tests supports, and what it does not prove.
- How these lessons could apply to runtime-security event ingestion.

Keep the project small. Add modules for records, attribution, and tests only as needed; introduce no service framework.

Codex prompt:

> Complete milestone 5. Explain the result so another engineer can reproduce it and assess its limits. Include commands for the passing suite and the intentionally failing demonstration. Avoid claiming exhaustive verification from generated tests.

## Optional formal model

Model the same system in TLA+ after the Go contract is clear. Use finite sets of PIDs, process lifetimes, workloads, and event IDs.

Represent:

- Actual live processes and the capture-time origin ledger.
- Pending messages, allowing arbitrary delivery order.
- Metadata known to the backend.
- Unresolved events and resolved attributions.

Preserve the same observation boundary: the attribution action cannot consult the oracle ledger.

First model the PID-only lookup and have TLC find a safety violation. Then model the full-key lookup and check the no-false-attribution invariant across the bounded state space. Also check useful attribution when matching information has arrived.

If adding “every event eventually resolves,” state the necessary conditions: its metadata exists, neither message is permanently dropped, required history remains available, and delivery is fair for those messages. Without these assumptions, unconditional eventual resolution is false.

Translate a model-checker counterexample into a Go regression. Keep the model and code properties aligned, while recognizing that checking the model does not establish implementation refinement.

Codex prompt:

> Build a small TLA+ model of the existing Go contract. Start with safety and reproduce the faulty counterexample before checking the corrected algorithm. Report TLC's configuration and outcome. Discuss liveness separately with explicit delivery and retention assumptions.

## Optional bounded retention

Introduce metadata expiration only after the core properties are understood. Use logical time and an explicit maximum supported delay or unresolved-on-expiration policy.

Safety must survive expiration: insufficient history may produce an unresolved result, never a confident match to the current PID holder. Narrow the useful-attribution and order-independence contracts to the stated retention conditions.

Explore the engineering tradeoff between retaining history, buffering unresolved events, resource bounds, and attribution coverage.

## Optional real Linux observation

Move to a disposable Linux VM for this stage. Inspect the identity information available to an actual sensor and validate how it is captured. Investigate host versus namespace PID, process versus thread identity, start-time precision, boot identity, and lifecycle races.

Replay a few captured observations through the Go pipeline. Treat this as an integration exercise: synthetic incarnation uniqueness does not prove a chosen kernel-derived identifier has the same guarantees. Avoid forcing real PID exhaustion merely to reproduce a bug already covered by simulation.

## References

- [Rapid documentation](https://pkg.go.dev/pgregory.net/rapid): generated inputs, state-machine actions, invariant checks, and automatic shrinking.
- [Rapid project and examples](https://github.com/flyingmutant/rapid): usage, failure persistence, and a sample queue state machine.
- [Linux eBPF verifier](https://docs.kernel.org/bpf/verifier.html): execution-safety checks, distinct from correct application-level attribution.
- [Running BPF programs from user space](https://docs.kernel.org/bpf/bpf_prog_run.html): possible later testing route for supported BPF program types.
- [Leslie Lamport's overview of TLA+](https://lamport.azurewebsites.net/tla/high-level-view.html): specifying and checking system behaviour.

These references explain the tools and mechanisms. The contracts and milestones above are proposed choices for this project.
