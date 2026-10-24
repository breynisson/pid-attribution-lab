package simulation_test

import (
	"fmt"
	"testing"

	simulation "example.com/pid-attribution-lab"
)

func TestDelayedEventAfterPIDReuse(t *testing.T) {
	a := simulation.Metadata{
		Key:        simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 1},
		Executable: "A",
		Workload:   "workload-A",
	}
	b := simulation.Metadata{
		Key:        simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 2},
		Executable: "B",
		Workload:   "workload-B",
	}
	backend := newAttributor()
	// The world and oracle belong only to the test; the backend sees deliveries.
	live := make(map[int]simulation.Metadata)
	oracle := make(map[string]simulation.Attribution)
	var pending []simulation.Activity

	t.Log("1: A starts with PID 7; deliver A's metadata")
	live[7] = a
	backend.DeliverMetadata(a)

	t.Log("2: A creates E; capture its identity and queue E without delivering it")
	origin := live[7]
	event := simulation.Activity{EventID: "E", Key: origin.Key, Kind: "file-open"}
	oracle[event.EventID] = simulation.Attribution{
		EventID: event.EventID, Resolved: true, Key: origin.Key, Workload: origin.Workload,
	}
	pending = append(pending, event)

	t.Log("3: A exits; free PID 7 and deliver A's exit")
	delete(live, 7)
	backend.DeliverExit(a.Key)

	t.Log("4: B starts with the now-free PID 7; deliver B's metadata")
	if _, occupied := live[7]; occupied {
		t.Fatal("invalid world: cannot reuse a live PID")
	}
	live[7] = b
	backend.DeliverMetadata(b)

	t.Log("5: deliver the queued event E, still carrying A's original key")
	delayed := pending[0]
	pending = pending[1:]
	got := backend.DeliverActivity(delayed)
	want := oracle[delayed.EventID]
	if got != want {
		t.Fatalf("E must belong to A, its capture-time origin:\nexpected: %+v\nactual:   %+v", want, got)
	}
}

// Preserves the eight-action Safety trace shrunk by Rapid from seed 11.
// The unrelated PID 2/3 starts are retained to match that trace exactly.
func TestNewEventWithOnlyPreviousLifetimeMetadata(t *testing.T) {
	backend := newAttributor()
	live := make(map[int]simulation.Metadata)
	var metadataQueue []simulation.Metadata
	for pid := 1; pid <= 3; pid++ {
		metadata := simulation.Metadata{
			Key:        simulation.ProcessKey{Scope: "lab", PID: pid, Incarnation: uint64(pid)},
			Executable: fmt.Sprintf("process-%d", pid), Workload: fmt.Sprintf("workload-%d", pid),
		}
		live[pid] = metadata
		metadataQueue = append(metadataQueue, metadata)
	}
	// The old process exits physically, but its exit is not delivered.
	delete(live, 1)
	if _, occupied := live[1]; occupied {
		t.Fatal("invalid world: cannot reuse a live PID")
	}
	live[1] = simulation.Metadata{
		Key:        simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 4},
		Executable: "process-4", Workload: "workload-4",
	}
	metadataQueue = append(metadataQueue, live[1])
	origin := live[1]
	event := simulation.Activity{EventID: "E1", Key: origin.Key, Kind: "file-open"}
	// Deliver only the previous lifetime's metadata, then the new event.
	backend.DeliverMetadata(metadataQueue[0])
	got := backend.DeliverActivity(event)
	want := simulation.Attribution{EventID: event.EventID}
	if got != want {
		t.Fatalf("new event must remain unresolved without its matching metadata:\ncapture origin: %+v\ndelivery: old metadata, new activity (new metadata queued; old exit undelivered)\nexpected: %+v\nactual:   %+v", origin, want, got)
	}
}

// The four-action useful-attribution counterexample from milestone 2, seed 3.
func TestActivityBeforeMetadata(t *testing.T) {
	backend := newAttributor()
	origin := simulation.Metadata{
		Key:        simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 1},
		Executable: "process-1", Workload: "workload-1",
	}
	event := simulation.Activity{EventID: "E1", Key: origin.Key, Kind: "file-open"}
	if _, ok := backend.Result(event.EventID); ok {
		t.Fatal("an undelivered event must not have a result")
	}
	if got := backend.DeliverActivity(event); got != (simulation.Attribution{EventID: event.EventID}) {
		t.Fatalf("activity without metadata must be unresolved, got %+v", got)
	}
	backend.DeliverMetadata(origin)
	want := simulation.Attribution{
		EventID: event.EventID, Resolved: true, Key: origin.Key, Workload: origin.Workload,
	}
	if got, ok := backend.Result(event.EventID); !ok || got != want {
		t.Fatalf("matching metadata must resolve the stored event: expected %+v, got %+v (present=%t)", want, got, ok)
	}
}

func TestPendingEventsMatchExactLifetime(t *testing.T) {
	backend := newAttributor()
	a := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 1}, Workload: "workload-A",
	}
	b := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 2}, Workload: "workload-B",
	}
	// A creates two events and exits before B starts and creates a third.
	backend.DeliverActivity(simulation.Activity{EventID: "E1", Key: a.Key})
	backend.DeliverActivity(simulation.Activity{EventID: "E2", Key: a.Key})
	backend.DeliverExit(a.Key)
	backend.DeliverActivity(simulation.Activity{EventID: "E3", Key: b.Key})
	backend.DeliverMetadata(b)
	for _, eventID := range []string{"E1", "E2"} {
		if got, ok := backend.Result(eventID); !ok || got != (simulation.Attribution{EventID: eventID}) {
			t.Fatalf("B's metadata must leave %s unresolved: got %+v (present=%t)", eventID, got, ok)
		}
	}
	// A's delayed metadata must resolve both pending events, even after its exit.
	backend.DeliverMetadata(a)
	for i, origin := range []simulation.Metadata{a, a, b} {
		eventID := fmt.Sprintf("E%d", i+1)
		want := simulation.Attribution{EventID: eventID, Resolved: true, Key: origin.Key, Workload: origin.Workload}
		if got, ok := backend.Result(eventID); !ok || got != want {
			t.Fatalf("expected %+v, got %+v (present=%t)", want, got, ok)
		}
	}
}

func TestStaleExitPreservesHistory(t *testing.T) {
	backend := newAttributor()
	a := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 1}, Workload: "workload-A",
	}
	b := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 2}, Workload: "workload-B",
	}
	backend.DeliverMetadata(a)
	backend.DeliverActivity(simulation.Activity{EventID: "E1", Key: a.Key})
	// A captures another event and exits; both deliveries are delayed until B starts.
	delayed := simulation.Activity{EventID: "E2", Key: a.Key}
	backend.DeliverMetadata(b)
	backend.DeliverExit(a.Key)
	backend.DeliverActivity(delayed)
	backend.DeliverActivity(simulation.Activity{EventID: "E3", Key: b.Key})
	for i, origin := range []simulation.Metadata{a, a, b} {
		eventID := fmt.Sprintf("E%d", i+1)
		want := simulation.Attribution{EventID: eventID, Resolved: true, Key: origin.Key, Workload: origin.Workload}
		if got, ok := backend.Result(eventID); !ok || got != want {
			t.Fatalf("stale exit must preserve history and the newer lifetime: expected %+v, got %+v (present=%t)", want, got, ok)
		}
	}
}

// TLC's faulty.cfg counterexample has six transitions after Init. Both lifetimes
// have workload WA: checking only workload equality would miss the false match.
func TestTLCDelayedEventBeforeOriginMetadata(t *testing.T) {
	backend := newAttributor()
	// 1: A starts, queuing metadata. 2: A captures E1, queuing its activity.
	a := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 1}, Workload: "WA",
	}
	event := simulation.Activity{EventID: "E1", Key: a.Key}
	// 3: A physically exits; its exit notification stays queued.
	// 4: B starts on the now-free PID, with the same workload but a new lifetime.
	b := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 2}, Workload: "WA",
	}
	// 5: Deliver B's metadata. 6: Deliver A's event, before A's metadata.
	backend.DeliverMetadata(b)
	want := simulation.Attribution{EventID: event.EventID}
	if got := backend.DeliverActivity(event); got != want {
		t.Fatalf("TLC trace: A's metadata is unavailable; same workload does not mean same process:\ncapture origin: %+v\nexpected: %+v\nactual:   %+v", a, want, got)
	}
	requireResult(t, backend, want)
}

func TestTLCUnfairDeliveryAndRecovery(t *testing.T) {
	backend := newAttributor()
	// unfair.cfg: A starts and captures E1, then exits before B starts.
	// Incarnations are unique labels, not an ordering requirement.
	a := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 2}, Workload: "WA",
	}
	event := simulation.Activity{EventID: "E1", Key: a.Key}
	b := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 1}, Workload: "WA",
	}
	backend.DeliverMetadata(b)
	backend.DeliverExit(a.Key)
	unresolved := simulation.Attribution{EventID: event.EventID}
	if got := backend.DeliverActivity(event); got != unresolved {
		t.Fatalf("withheld origin metadata: expected %+v, got %+v", unresolved, got)
	}
	// TLC queues an E1 replay, then B exits and its exit is delivered.
	// Neither the replay nor A's metadata is delivered before infinite stuttering.
	backend.DeliverExit(b.Key)
	requireResult(t, backend, unresolved)

	// Extend the finite prefix with other work. It cannot substitute for A's
	// metadata, and A's pending event must not prevent known events resolving.
	known := simulation.Activity{EventID: "E2", Key: b.Key} // Captured before B exited.
	wantKnown := simulation.Attribution{EventID: known.EventID, Resolved: true, Key: b.Key, Workload: b.Workload}
	if got := backend.DeliverActivity(known); got != wantKnown {
		t.Fatalf("known event during starvation: expected %+v, got %+v", wantKnown, got)
	}
	if got := backend.DeliverActivity(event); got != unresolved {
		t.Fatalf("activity replay cannot supply metadata: expected %+v, got %+v", unresolved, got)
	}
	requireResult(t, backend, unresolved)

	// Recovery is a separate continuation: delivery resumes. Resolution must
	// finish in this call, without a worker, timer, or another activity delivery.
	backend.DeliverMetadata(a)
	want := simulation.Attribution{EventID: event.EventID, Resolved: true, Key: a.Key, Workload: a.Workload}
	requireResult(t, backend, want)
	requireResult(t, backend, wantKnown)
	backend.DeliverExit(a.Key)
	backend.DeliverMetadata(b)
	if got := backend.DeliverActivity(event); got != want {
		t.Fatalf("recovered result must remain stable: expected %+v, got %+v", want, got)
	}
}
