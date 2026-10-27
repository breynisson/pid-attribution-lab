# Go implementations of the modeled boundaries

The original `Attributor` and its API remain unchanged. The new
[Backend](backend.go) adds bounded admission, serialization, and optional
[file snapshots](file_snapshot.go). [Sensor and BootDetector](sensor.go) add a
capture adapter and a real Linux boot-ID reader. There are no new dependencies.
The capture source itself is an interface: no live kernel event collector is
implemented in this milestone.

## Storage and admission contract

`OpenBackend(Limits{Metadata: n, Events: m}, store)` returns a new or recovered
backend. A nil store is volatile; `FileSnapshot{Path: path}` persists in an
existing directory. A `SnapshotStore` has exactly one owning backend, including
across processes. Ownership is a caller obligation, not an interprocess lock.
Do not use an old backend after handing its store to a recovered instance.

Metadata and event capacities count retained entries, including pending events.
Zero rejects new entries, and negative capacities are invalid. New entries at
capacity return `ErrCapacity`; duplicates remain admissible. The default API has
no expiry or automatic retry. Opt-in [logical-time retention](RETENTION.md)
expires metadata and finalizes pending events as unresolved. Event IDs remain
retained; event capacity can remain full indefinitely. An accepted
event can remain unresolved if its metadata is rejected or never arrives.

Unlike the original core, this wrapper explicitly rejects contradictory metadata
with `ErrConflict`, comparing the entire cached metadata record. With expiration,
this check lasts only as long as the record remains cached. Event IDs retain
first-delivery-wins behavior. Neither rule establishes whether the first input
is true or authenticates later metadata after its previous lease was forgotten.

Each successful mutation saves the complete retained state before publishing it
in memory or returning success. Version-2 snapshots store event results, terminal
expiry flags, admission times, retention policy and logical time. Recovery restores
answers directly and rebuilds the pending index only for active unresolved events.
It never recomputes published answers from potentially expired metadata or queries
live processes. Structural inconsistencies are rejected; the snapshot is trusted
storage, not authenticated proof of capture truth. Recovery does not silently
reset damaged state. Reopening requires identical capacity and retention settings.
Version-1 snapshots migrate with expiration disabled and are saved as version 2
on the next mutation; opting them into finite TTLs is rejected because they lack
admission times. Earlier binaries cannot read version-2 snapshots.
Stored strings must be valid UTF-8: JSON's replacement of invalid sequences must
not silently change an accepted identity or workload during recovery.

`FileSnapshot` writes a temporary file in the destination directory, syncs it,
closes it, renames it over the snapshot, then syncs the directory. This follows
the filesystem contract underlying Go's [Sync](https://pkg.go.dev/os#File.Sync)
and [Rename](https://pkg.go.dev/os#Rename) operations. Saving is restricted to
Linux/macOS; it assumes a local filesystem with reliable sync and atomic rename.
The parent directory must already exist. A crash may leave an unused temporary
file; recovery reads only the named snapshot, never a partially written temp file.

Any save error returns `ErrStorage` and stops further reads and writes on that
backend instance. A save can have committed even if its acknowledgement failed.
Discard the instance, reopen the store, and retry with the same event ID and
payload. This handles either surviving snapshot without claiming exactly-once
delivery. Capacity rejection and metadata conflicts do not poison the instance.

Snapshots and candidate indexes cost O(retained entries) work per accepted
delivery or clock advance, and hold the backend mutex during I/O. This is a small
lab implementation. Entry limits do not bound payload bytes, temporary copies,
disk space, or upstream queues. There is no journal, reclamation protocol,
multi-writer coordination, disk-corruption repair, or power-loss certification.

## Capture and boot contract

Construct a `BootDetector` with `ReadLinuxBootID` (or an explicitly selected
`ReadBootIDFile` source), then a `Sensor` with that detector, a stable identity
boundary, and a `CaptureSource` implementation. The boundary must distinguish
tenant, host, and PID-namespace lifetime. Scope is an encoded JSON tuple of that
boundary and the captured boot ID, avoiding ambiguous string concatenation.

The [Linux boot ID](https://docs.kernel.org/admin-guide/sysctl/kernel.html#random)
reader reads `/proc/sys/kernel/random/boot_id` each time and validates its UUID
format. `BootDetector.Observe` reports changes relative to its last successful
observation. A missing or malformed source returns an error; the previous value
is never used as a fallback. The detector is explicit/pull-based, with no poller.
Its change flag is relative to all calls, including the sensor's internal reads;
it is not a subscription or a durable boot-history log.

`Sensor.Capture(eventID, pid, kind)` reads boot identity, asks the source to capture
the event and process snapshot together, then reads boot identity again. A boot
change or any read/capture error returns no deliverable activity or metadata.
When accepted, both outputs contain a complete value-copy identity that later
reuse/reboot cannot change. The two outputs may be delivered independently and
in either order. A reboot during capture conservatively discards the observation,
even when its earlier snapshot might have been usable.

The source must capture coherently at the event, not look up a PID after an event
already occurred. It must supply an incarnation unique within the boundary/boot,
including across source restarts. The adapter does not manufacture that guarantee
from `/proc` timestamps. Event IDs must be globally unique, and boot IDs must not
repeat. Both boot reads and the source must refer to the same host. A reused boot
ID, a stale provider, a mismatched namespace, or a dishonest source can violate
the contract; repeated reads cannot authenticate them.

Fixture sources implement valid synthetic process transitions for tests. They
are not a Linux capture implementation. A real collector still needs a concrete
event source, a justified incarnation scheme, namespace handling, and Linux
integration tests. The only live OS reader implemented here is the boot-ID reader.

## Concurrency and goroutines

`Backend` holds one mutex across each complete delivery/query, including durable
commit and pending resolution. `Sensor` serializes complete capture operations;
`BootDetector` serializes observations. This prevents the modeled overlap where
an activity overwrites a metadata-triggered resolution with its stale unresolved
answer. Do not copy these objects, share a store among owners, or reenter them
from a store/source callback.

Production code starts **no goroutines**. The tests use goroutines, a channel
start gate, and a wait group to overlap calls without sleeps. They check final
resolution, admission at capacity, persisted recovery, and serialized access to
a source that has no internal lock. The race detector checks the executed
interleavings; it does not prove every schedule safe or establish fair scheduling.
The old `Attributor` remains serial-only.

## Reproduce and evidence

From `simulation/`, on macOS or Linux with a suitable local filesystem:

```sh
unset PID_ATTRIBUTION_FAULTY PID_ATTRIBUTION_UNTRUSTED_DEMO PID_ATTRIBUTION_CRASH_SNAPSHOT
# Narrow checks before the full property suite.
go test . -run '^(TestBackend|TestSensor|TestBoot)' -count=1 -v
go test -race . -run '^(TestBackend|TestSensor|TestBoot)' -count=1
go test ./... -count=1 -rapid.seed=11 -rapid.nofailfile
go vet ./...

# Linux only: reads actual procfs; explicitly skipped on macOS.
go test . -run '^TestLinuxBootIDIntegration$' -count=1 -v
```

Set `GOCACHE=/tmp/pid-attribution-go-build` if the default cache is not writable.
The crash helper is normally skipped; its parent starts a separate process,
accepts resolved and unresolved events, then exits with code 23 without a shutdown
hook. The parent requires that exact exit before testing recovery and delayed
resolution. Injected store errors separately cover failure before commit and
acknowledgement loss after commit. They do not simulate a physical power failure.

| Model concern | Implementation evidence |
|---|---|
| Capacity exceeded by a second event | `TestBackendCapacityAndImmutableMetadata`, `TestBackendConcurrentCapacity` |
| Volatile crash loses accepted work | `TestBackendRecoversAfterProcessExit`, `TestBackendStorageFailureRequiresRecovery` |
| Conflicting metadata poisons later events | `TestBackendRejectsMetadataCounterexample` (the original core demonstration remains intentionally failing) |
| Late PID lookup selects replacement | `TestSensorSnapshotSurvivesPIDReuseAndBoot`, including reuse before the capture call returns |
| Stale boot detection reuses scope | Same end-to-end test with a changed boot file and a reset incarnation; `TestSensorRejectsBootChangeDuringCapture` |
| Missing boot source falls back to old identity | `TestSensorReadFailureDoesNotUseCachedBoot`, `TestBootIDFileValidation` |
| Concurrent activity overwrites resolved result | `TestBackendConcurrentDelivery`, including recovery of every accepted result |
| Source called concurrently without synchronization | `TestSensorConcurrentCaptures` under `-race` |

The focused tests, full Go suite, `go vet`, and race checks passed on macOS;
Linux reader integration was skipped there. The test binary also cross-compiled
for Linux/amd64, which checks compilation, not Linux execution. The model
configurations do not prove the
snapshot format, filesystem protocol, error states, or combined Go pipeline.
These tests give implementation evidence, not a refinement proof or exhaustive
verification. Liveness still needs delivery, available capacity, working storage,
eventual execution, and (after a crash) a successful recovery interval.
