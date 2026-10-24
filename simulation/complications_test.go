package simulation_test

import (
	"fmt"
	"testing"

	simulation "example.com/pid-attribution-lab"
)

func requireResult(t *testing.T, backend attributor, want simulation.Attribution) {
	t.Helper()
	if got, ok := backend.Result(want.EventID); !ok || got != want {
		t.Fatalf("expected %+v, got %+v (present=%t)", want, got, ok)
	}
}

func TestDuplicateDelivery(t *testing.T) {
	backend := newAttributor()
	origin := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 1}, Workload: "workload-A",
	}
	event := simulation.Activity{EventID: "E1", Key: origin.Key, Kind: "file-open"}
	unresolved := simulation.Attribution{EventID: event.EventID}
	want := simulation.Attribution{EventID: event.EventID, Resolved: true, Key: origin.Key, Workload: origin.Workload}
	for i := 0; i < 3; i++ {
		if got := backend.DeliverActivity(event); got != unresolved {
			t.Fatalf("replay before metadata: expected %+v, got %+v", unresolved, got)
		}
		requireResult(t, backend, unresolved)
	}
	backend.DeliverMetadata(origin)
	requireResult(t, backend, want)
	for i := 0; i < 3; i++ {
		backend.DeliverMetadata(origin)
		backend.DeliverExit(origin.Key)
		if got := backend.DeliverActivity(event); got != want {
			t.Fatalf("replay after resolution: expected %+v, got %+v", want, got)
		}
		requireResult(t, backend, want)
	}
}

func TestEventIDKeepsFirstOrigin(t *testing.T) {
	backend := newAttributor()
	a := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 1}, Workload: "workload-A",
	}
	b := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 2, Incarnation: 2}, Workload: "workload-B",
	}
	event := simulation.Activity{EventID: "E1", Key: a.Key, Kind: "file-open"}
	conflictingReplay := simulation.Activity{EventID: "E1", Key: b.Key, Kind: "network-connect"}
	unresolved := simulation.Attribution{EventID: event.EventID}
	backend.DeliverActivity(event)
	// A replay is not a new capture, even if it supplies a different payload.
	if got := backend.DeliverActivity(conflictingReplay); got != unresolved {
		t.Fatalf("a replay must keep the first unresolved event: got %+v", got)
	}
	backend.DeliverMetadata(b)
	requireResult(t, backend, unresolved)
	backend.DeliverMetadata(a)
	want := simulation.Attribution{EventID: event.EventID, Resolved: true, Key: a.Key, Workload: a.Workload}
	requireResult(t, backend, want)
	if got := backend.DeliverActivity(conflictingReplay); got != want {
		t.Fatalf("a replay must keep the first event's origin: expected %+v, got %+v", want, got)
	}
	requireResult(t, backend, want)
}

func TestScopeIsolation(t *testing.T) {
	for _, metadataFirst := range []bool{false, true} {
		name := "activity-first"
		if metadataFirst {
			name = "metadata-first"
		}
		t.Run(name, func(t *testing.T) {
			backend := newAttributor()
			a := simulation.Metadata{
				Key: simulation.ProcessKey{Scope: "scope-A", PID: 1, Incarnation: 1}, Workload: "workload-A",
			}
			b := simulation.Metadata{
				Key: simulation.ProcessKey{Scope: "scope-B", PID: 1, Incarnation: 1}, Workload: "workload-B",
			}
			if metadataFirst {
				backend.DeliverMetadata(a)
			}
			backend.DeliverActivity(simulation.Activity{EventID: "EA", Key: a.Key})
			gotB := backend.DeliverActivity(simulation.Activity{EventID: "EB", Key: b.Key})
			if gotB != (simulation.Attribution{EventID: "EB"}) {
				t.Fatalf("scope A must not resolve scope B's event: got %+v", gotB)
			}
			if !metadataFirst {
				backend.DeliverMetadata(a)
			}
			wantA := simulation.Attribution{EventID: "EA", Resolved: true, Key: a.Key, Workload: a.Workload}
			requireResult(t, backend, wantA)
			requireResult(t, backend, simulation.Attribution{EventID: "EB"})
			backend.DeliverMetadata(b)
			requireResult(t, backend, wantA)
			requireResult(t, backend, simulation.Attribution{EventID: "EB", Resolved: true, Key: b.Key, Workload: b.Workload})
		})
	}
}

func TestRebootUsesFreshScope(t *testing.T) {
	backend := newAttributor()
	before := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "host-1/boot-A", PID: 1, Incarnation: 1}, Workload: "before-reboot",
	}
	after := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "host-1/boot-B", PID: 1, Incarnation: 1}, Workload: "after-reboot",
	}
	// The old process captured this event before the reboot; delivery is delayed.
	delayed := simulation.Activity{EventID: "old", Key: before.Key}
	backend.DeliverMetadata(after)
	newEvent := simulation.Activity{EventID: "new", Key: after.Key}
	backend.DeliverActivity(newEvent)
	wantNew := simulation.Attribution{EventID: "new", Resolved: true, Key: after.Key, Workload: after.Workload}
	requireResult(t, backend, wantNew)
	if got := backend.DeliverActivity(delayed); got != (simulation.Attribution{EventID: "old"}) {
		t.Fatalf("post-reboot metadata must not resolve a pre-reboot event: got %+v", got)
	}
	backend.DeliverMetadata(before)
	backend.DeliverExit(before.Key)
	requireResult(t, backend, simulation.Attribution{EventID: "old", Resolved: true, Key: before.Key, Workload: before.Workload})
	requireResult(t, backend, wantNew)
	newEvent.EventID = "new-after-stale-messages"
	wantNew.EventID = newEvent.EventID
	if got := backend.DeliverActivity(newEvent); got != wantNew {
		t.Fatalf("old boot messages must not disturb the new boot: expected %+v, got %+v", wantNew, got)
	}
}

func TestMissingMetadataRemainsUnresolved(t *testing.T) {
	backend := newAttributor()
	missing := simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 1}
	event := simulation.Activity{EventID: "missing", Key: missing}
	unresolved := simulation.Attribution{EventID: event.EventID}
	if got := backend.DeliverActivity(event); got != unresolved {
		t.Fatalf("missing metadata must be explicit: expected %+v, got %+v", unresolved, got)
	}
	backend.DeliverExit(missing)
	requireResult(t, backend, unresolved)
	backend.DeliverMetadata(simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 2, Incarnation: 2}, Workload: "unrelated",
	})
	requireResult(t, backend, unresolved)
	replacement := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: 3}, Workload: "replacement",
	}
	backend.DeliverMetadata(replacement)
	requireResult(t, backend, unresolved)
	backend.DeliverActivity(simulation.Activity{EventID: "known", Key: replacement.Key})
	requireResult(t, backend, simulation.Attribution{EventID: "known", Resolved: true, Key: replacement.Key, Workload: replacement.Workload})
	backend.DeliverExit(replacement.Key)
	requireResult(t, backend, unresolved)
	if got := backend.DeliverActivity(event); got != unresolved {
		t.Fatalf("a replay must not guess the replacement's identity: got %+v", got)
	}
}

func TestDelayedMessagesAcrossSeveralPIDReuses(t *testing.T) {
	for _, exitFirst := range []bool{false, true} {
		name := "metadata-before-exit"
		if exitFirst {
			name = "exit-before-metadata"
		}
		t.Run(name, func(t *testing.T) {
			backend := newAttributor()
			var processes []simulation.Metadata
			var events, delayed []simulation.Activity
			var oracle []simulation.Attribution
			// Each lifetime captures two events, then exits before the next
			// starts. All messages are queued; only lifetime 4 remains alive.
			for i := 1; i <= 4; i++ {
				origin := simulation.Metadata{
					Key:      simulation.ProcessKey{Scope: "lab", PID: 1, Incarnation: uint64(i)},
					Workload: fmt.Sprintf("workload-%d", i),
				}
				event := simulation.Activity{EventID: fmt.Sprintf("E%d", i), Key: origin.Key}
				processes = append(processes, origin)
				events = append(events, event)
				delayed = append(delayed, simulation.Activity{EventID: "late-" + event.EventID, Key: origin.Key})
				oracle = append(oracle, simulation.Attribution{
					EventID: event.EventID, Resolved: true, Key: origin.Key, Workload: origin.Workload,
				})
			}
			for _, event := range events {
				backend.DeliverActivity(event)
			}
			known := make([]bool, len(processes))
			check := func() {
				for i, want := range oracle {
					if !known[i] {
						want = simulation.Attribution{EventID: want.EventID}
					}
					requireResult(t, backend, want)
				}
			}
			check()
			// The newest metadata arrives first, followed by scrambled old messages.
			for _, i := range []int{3, 1, 0, 2} {
				if i < 3 && exitFirst {
					backend.DeliverExit(processes[i].Key)
					check()
				}
				backend.DeliverMetadata(processes[i])
				known[i] = true
				check()
				if i < 3 && !exitFirst {
					backend.DeliverExit(processes[i].Key)
					check()
				}
			}
			// Deliver second events only after all old exits and metadata arrived.
			for i, event := range delayed {
				want := oracle[i]
				want.EventID = event.EventID
				if got := backend.DeliverActivity(event); got != want {
					t.Fatalf("delayed event after repeated reuse: expected %+v, got %+v", want, got)
				}
				requireResult(t, backend, want)
				check()
			}
		})
	}
}
