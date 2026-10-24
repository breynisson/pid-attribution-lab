package faulty_test

import (
	"testing"

	simulation "example.com/pid-attribution-lab"
	"example.com/pid-attribution-lab/faulty"
)

// This test intentionally fails until a corrected attributor is introduced.
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
	backend := faulty.New()
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
