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

## Milestone 1 setup and stopping point

`go.mod` pins Rapid v1.3.0 for milestone 2. Its
[tagged module declaration](https://github.com/flyingmutant/rapid/blob/v1.3.0/go.mod)
requires Go 1.23, compatible with the installed Go 1.27.1. There are no production
dependencies or Rapid imports yet. Downloading Rapid was attempted but failed
because DNS could not resolve `proxy.golang.org`; no `go.sum` was generated.
The milestone 1 test does not need that download.

The suite intentionally stays red at this stage. No property tests, corrected
attributor, or opt-in failure flag have been added; those belong to later
milestones. `PROJECT_PLAN.md` remains unchanged.

## Milestone 2: generated counterexamples

`faulty/property_test.go` now imports the pinned Rapid v1.3.0 as a test-only
dependency. The module is available locally and `go.sum` now includes its content
checksum. Production code and public APIs are unchanged. These tests still
intentionally fail; the fix and opt-in failure flag belong to milestone 3.

The constrained property keeps the original five-step scenario and varies PID
in `{1, 2, 3}`, the initial incarnation, a positive incarnation gap, and a workload
label with distinct A/B suffixes. The bounded incarnation values cannot overflow.
With seed 1, Rapid shrank the inputs to PID 1, incarnations 1 and 2, and workloads
`a-A` and `a-B`. It still attributes A's delayed event to B.

The stateful test uses `rapid.T.Repeat` to draw enabled actions and their valid
targets. Starts choose free PIDs; capture and exit choose live PIDs. A physical
exit immediately frees the PID, independently of its notification's delivery.
Each start gets a never-repeated incarnation and each event a unique ID.
Metadata, exit notifications, and activities enter one queue; a delivery removes
any generated queue index. All choices go through Rapid, with PID candidates in
fixed numeric order and observations in delivery order, avoiding map iteration
nondeterminism. There are no goroutines, sleeps, or invalid-transition filters.

The private oracle copies the actual live process's key and workload at event
creation. It never reads the backend or uses its PID lookup. A separate set tracks
which exact metadata keys have been delivered, solely to decide when resolution
is required. Only message payloads cross into the backend.

`Safety` checks every resolved result against capture-time truth after every
action, including every delivery. `UsefulAttribution` also requires resolution
once both an event and its matching metadata have arrived. Keeping a safety-only
run allows generation to reach wrong-lifetime matches even if an earlier missing
resolution would have stopped the combined check.

### The shrunk safety trace

Starting at seed 1, the safety run found a failure at seed 11 and shrank it to:

| Action | World or delivery |
|---|---|
| 1 | Start PID 1, incarnation 1; queue metadata. |
| 2 | Start PID 2, incarnation 2; queue metadata. |
| 3 | Start PID 3, incarnation 3; queue metadata. |
| 4 | PID 1, incarnation 1 exits; queue its exit. |
| 5 | Start PID 1 again, now incarnation 4; queue metadata. |
| 6 | Incarnation 4 creates E1; record its origin and queue activity. |
| 7 | Deliver only incarnation 1's metadata. |
| 8 | Deliver E1, still carrying incarnation 4's key. |

This reverses the original failure: a **new process's event is attributed to an
old process**. The backend receives old metadata and matches the numeric PID,
ignoring the event's incarnation. The true origin is `(lab, 1, 4)` with
`workload-4`, but its metadata has not arrived. The expected result is therefore
**unresolved**; the actual result confidently names `(lab, 1, 1)` and `workload-1`.
An undelivered old exit does not make the old process the event's origin.

`TestNewEventWithOnlyPreviousLifetimeMetadata` preserves this trace as an ordinary
Go regression, including the unrelated PID 2/3 starts. Those starts are not
essential to the bug: Rapid's shrunk output is not guaranteed globally minimal.
The regression asserts the correct behavior and intentionally fails against
`faulty`, independently of Rapid seeds or persisted failure files.

### The shrunk useful-attribution trace

Starting at seed 2, the combined run found a failure at seed 3 and shrank it to:

1. Start PID 1, incarnation 1, queuing metadata.
2. Create E1 from that live process, queuing activity and recording its origin.
3. Deliver E1 first: an unresolved result is correct at this point.
4. Deliver the matching metadata: a resolved result is now required, but none is
   exposed by the current implementation.

The harness retains the values returned by `DeliverActivity`. The current API
has no result query or update notification, and `DeliverMetadata` returns nothing.
The harness does not redeliver the activity or invent an updated result. This
detects the missing observable resolution in the current contract; milestone 3
must provide a way to observe updated attribution and adapt this observation
code. Comparing old value copies alone cannot validate a future implementation's
internal resolution. No current public API was changed for this milestone.

### Reproduce

From `simulation/` (add `GOCACHE=/tmp/pid-attribution-go-build` if needed):

```sh
# Constrained identities within the original causal skeleton.
go test ./faulty -run '^TestPropertyDelayedEventAfterPIDReuse$' -count=1 -v -rapid.seed=1 -rapid.nofailfile

# Replay the stateful safety counterexample and its ordinary regression.
go test ./faulty -run '^TestStatefulAttribution$/Safety$' -count=1 -v -rapid.seed=11 -rapid.nofailfile
go test ./faulty -run '^TestNewEventWithOnlyPreviousLifetimeMetadata$' -count=1 -v

# Replay activity-before-metadata missing resolution.
go test ./faulty -run '^TestStatefulAttribution$/UsefulAttribution$' -count=1 -v -rapid.seed=3 -rapid.nofailfile

# Run the entire intentionally failing suite with a reproducible seed.
go test ./... -count=1 -rapid.seed=11 -rapid.nofailfile
```

Each command above is expected to exit with status 1 for an attribution assertion,
not a build failure. `-rapid.nofailfile` keeps these demonstrations from writing
failure artifacts; omit it to use Rapid's persisted replay. Omit `-rapid.seed` to
explore fresh sequences. A different seed may find a different valid failure.
The tests expose counterexamples; they do not exhaustively verify all schedules.

API reference: [Rapid v1.3.0 documentation](https://pkg.go.dev/pgregory.net/rapid@v1.3.0).
