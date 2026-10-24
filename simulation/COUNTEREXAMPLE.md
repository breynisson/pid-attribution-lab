# Milestone 1: a delayed event crosses PID lifetimes

The ordinary Go test reproduces a false attribution. The implementation in
`faulty/` is deliberately incorrect and remains unfixed for this milestone.

From the repository root, run:

```sh
cd simulation
go test ./faulty -run '^TestDelayedEventAfterPIDReuse$' -count=1 -v
```

In a restricted environment, set `GOCACHE=/tmp/pid-attribution-go-build` on the
`go test` command if the default build cache is not writable.

## Records and assumptions

A process key is `(scope, PID, incarnation)`. The single scope is `lab`, and
incarnations are synthetic values that never repeat within that scope. They are
an assumption of this simulation, not a claim about Linux process identifiers.
Metadata contains the key, executable label, and immutable workload. Each event
has a unique ID, a kind, and the full process key captured at creation. An
attribution contains the event ID and either a resolved key/workload or an
explicit unresolved status.

The test uses one goroutine, logical steps, and an explicit activity queue.
Metadata and exit notifications are delivered immediately in this trace. The
test's live-process map models physical lifetimes: A creates E while alive, and
B starts only after A exits. The private oracle records A's identity and workload
at capture time; it never consults the backend's PID table. The backend receives
only delivered metadata, exits, and activities, never the oracle or world state.

## Observed failure

| Step | World and delivery |
|---|---|
| 1 | A starts with key `(lab, 7, 1)` and workload `workload-A`; deliver A's metadata. |
| 2 | A creates E; capture A's full key and queue E. |
| 3 | A exits; free PID 7 and deliver A's exit. |
| 4 | B starts with key `(lab, 7, 2)` and workload `workload-B`; deliver B's metadata. |
| 5 | Deliver E from the queue, still carrying `(lab, 7, 1)`. |

The narrow test was run with Go 1.27.1 and exited with status 1:

```text
E must belong to A, its capture-time origin:
expected: {EventID:E Resolved:true Key:{Scope:lab PID:7 Incarnation:1} Workload:workload-A}
actual:   {EventID:E Resolved:true Key:{Scope:lab PID:7 Incarnation:2} Workload:workload-B}
```

A's metadata was delivered before E, so the expected origin is unambiguous.
The faulty implementation deletes A's metadata on exit, then stores B under the
same numeric PID. On activity delivery it ignores the event's incarnation and
reads the current PID table. This produces a confidently wrong result.

## Why a more careful PID lookup is insufficient

The table correctly says B owns PID 7 at delivery time. Rechecking it or locking
it still answers a different question from who generated E. No concurrent race
is needed: this failure happens in a fully sequential execution.

Consider an interface that receives only PID, event ID, and activity kind for
activities, with no capture timestamp or lifetime identifier:

- History 1: A creates E before exiting; E is delayed until B starts.
- History 2: A exits, B starts, and B creates E.

Both can deliver exactly the same observations: metadata A, exit A, metadata B,
then activity `(E, PID 7, file-open)`. Their correct origins differ. Any algorithm
given those identical observations must return the same answer for both, so it
cannot guarantee a correct resolved answer for both. Explicitly unresolved is
honest when the available information cannot distinguish the histories.

Our actual event record includes a full capture-time key, so these histories
are distinguishable. The bug is that the attributor ignores that information.
Using it and retaining historical metadata are deferred to milestone 3.

## Setup and stopping point

`go.mod` pins Rapid v1.3.0 for milestone 2. Its
[tagged module declaration](https://github.com/flyingmutant/rapid/blob/v1.3.0/go.mod)
requires Go 1.23, compatible with the installed Go 1.27.1. There are no production
dependencies or Rapid imports yet. Downloading Rapid was attempted but failed
because DNS could not resolve `proxy.golang.org`; no `go.sum` was generated.
The milestone 1 test does not need that download.

The suite intentionally stays red at this stage. No property tests, corrected
attributor, or opt-in failure flag have been added; those belong to later
milestones. `PROJECT_PLAN.md` remains unchanged.
