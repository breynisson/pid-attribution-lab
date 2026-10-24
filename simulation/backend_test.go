package simulation_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	simulation "example.com/pid-attribution-lab"
)

func backendOrigin(pid int) simulation.Metadata {
	return simulation.Metadata{Key: simulation.ProcessKey{Scope: "host/boot-A", PID: pid, Incarnation: uint64(pid)}, Workload: fmt.Sprintf("W%d", pid)}
}

func requireBackendResult(t *testing.T, b *simulation.Backend, want simulation.Attribution) {
	t.Helper()
	got, found, err := b.Result(want.EventID)
	if err != nil || !found || got != want {
		t.Fatalf("expected %+v; got %+v, found=%t, err=%v", want, got, found, err)
	}
}

func TestBackendCapacityAndImmutableMetadata(t *testing.T) {
	b, err := simulation.OpenBackend(simulation.Limits{Metadata: 1, Events: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, other := backendOrigin(1), backendOrigin(2)
	event := simulation.Activity{EventID: "E1", Key: a.Key}
	if got, err := b.DeliverActivity(event); err != nil || got.Resolved {
		t.Fatalf("activity before metadata: %+v, %v", got, err)
	}
	if _, err := b.DeliverActivity(simulation.Activity{EventID: "E2", Key: other.Key}); !errors.Is(err, simulation.ErrCapacity) {
		t.Fatalf("second event should exceed capacity: %v", err)
	}
	if _, found, err := b.Result("E2"); err != nil || found {
		t.Fatalf("rejected event must not be retained: found=%t, err=%v", found, err)
	}
	if err := b.DeliverMetadata(a); err != nil {
		t.Fatal(err)
	}
	want := simulation.Attribution{EventID: "E1", Resolved: true, Key: a.Key, Workload: a.Workload}
	requireBackendResult(t, b, want)
	if err := b.DeliverMetadata(other); !errors.Is(err, simulation.ErrCapacity) {
		t.Fatalf("second metadata should exceed capacity: %v", err)
	}
	// Duplicates still work at capacity; conflicting activity IDs keep the first.
	if err := b.DeliverMetadata(a); err != nil {
		t.Fatal(err)
	}
	event.Key = other.Key
	if got, err := b.DeliverActivity(event); err != nil || got != want {
		t.Fatalf("duplicate at capacity: %+v, %v", got, err)
	}
	conflict := a
	conflict.Workload = "incorrect"
	if err := b.DeliverMetadata(conflict); !errors.Is(err, simulation.ErrConflict) {
		t.Fatalf("conflict must be rejected: %v", err)
	}
	if err := b.DeliverExit(a.Key); err != nil {
		t.Fatal(err)
	}
	requireBackendResult(t, b, want)
}

type failingSnapshot struct {
	data       []byte
	failBefore bool
	failAfter  bool
}

func (s *failingSnapshot) Load() ([]byte, error) { return append([]byte(nil), s.data...), nil }
func (s *failingSnapshot) Save(data []byte) error {
	if s.failBefore {
		return errors.New("failed before commit")
	}
	s.data = append([]byte(nil), data...)
	if s.failAfter {
		return errors.New("acknowledgement lost after commit")
	}
	return nil
}

func TestBackendStorageFailureRequiresRecovery(t *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		t.Run(fmt.Sprintf("after-commit=%t", afterCommit), func(t *testing.T) {
			store := &failingSnapshot{}
			limits := simulation.Limits{Metadata: 1, Events: 1}
			b, err := simulation.OpenBackend(limits, store)
			if err != nil {
				t.Fatal(err)
			}
			store.failBefore, store.failAfter = !afterCommit, afterCommit
			origin := backendOrigin(1)
			e := simulation.Activity{EventID: "E1", Key: origin.Key}
			if _, err := b.DeliverActivity(e); !errors.Is(err, simulation.ErrStorage) {
				t.Fatalf("failed save must not acknowledge activity: %v", err)
			}
			if _, _, err := b.Result(e.EventID); !errors.Is(err, simulation.ErrStorage) {
				t.Fatalf("uncertain instance must refuse reads: %v", err)
			}
			if err := b.DeliverMetadata(origin); !errors.Is(err, simulation.ErrStorage) {
				t.Fatalf("uncertain instance must refuse writes: %v", err)
			}
			store.failBefore, store.failAfter = false, false
			recovered, err := simulation.OpenBackend(limits, store)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := recovered.Result(e.EventID); err != nil || found != afterCommit {
				t.Fatalf("recovery must reflect surviving snapshot: found=%t, err=%v", found, err)
			}
			// Retrying the same ID handles either commit outcome without duplication.
			if _, err := recovered.DeliverActivity(e); err != nil {
				t.Fatal(err)
			}
			if err := recovered.DeliverMetadata(origin); err != nil {
				t.Fatal(err)
			}
			requireBackendResult(t, recovered, simulation.Attribution{EventID: "E1", Resolved: true, Key: origin.Key, Workload: origin.Workload})
		})
	}
}

// A separate process exits without any shutdown/flush hook after acceptance.
func TestBackendCrashWriter(t *testing.T) {
	path := os.Getenv("PID_ATTRIBUTION_CRASH_SNAPSHOT")
	if path == "" {
		t.Skip("subprocess helper for TestBackendRecoversAfterProcessExit")
	}
	b, err := simulation.OpenBackend(simulation.Limits{Metadata: 2, Events: 2}, simulation.FileSnapshot{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	a, other := backendOrigin(1), backendOrigin(2)
	if err := b.DeliverMetadata(a); err != nil {
		t.Fatal(err)
	}
	for i, origin := range []simulation.Metadata{a, other} {
		if _, err := b.DeliverActivity(simulation.Activity{EventID: fmt.Sprintf("E%d", i+1), Key: origin.Key}); err != nil {
			t.Fatal(err)
		}
	}
	os.Exit(23)
}

func TestBackendRecoversAfterProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackendCrashWriter$")
	cmd.Env = append(os.Environ(), "PID_ATTRIBUTION_CRASH_SNAPSHOT="+path)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("writer did not exit after acceptance: %v\n%s", err, output)
	}
	limits := simulation.Limits{Metadata: 2, Events: 2}
	store := simulation.FileSnapshot{Path: path}
	b, err := simulation.OpenBackend(limits, store)
	if err != nil {
		t.Fatal(err)
	}
	a, other := backendOrigin(1), backendOrigin(2)
	requireBackendResult(t, b, simulation.Attribution{EventID: "E1", Resolved: true, Key: a.Key, Workload: a.Workload})
	requireBackendResult(t, b, simulation.Attribution{EventID: "E2"})
	if err := b.DeliverMetadata(other); err != nil {
		t.Fatal(err)
	}
	restored, err := simulation.OpenBackend(limits, store) // Relinquish b; one owner at a time.
	if err != nil {
		t.Fatal(err)
	}
	want := simulation.Attribution{EventID: "E2", Resolved: true, Key: other.Key, Workload: other.Workload}
	requireBackendResult(t, restored, want)
	if got, err := restored.DeliverActivity(simulation.Activity{EventID: "E2", Key: a.Key}); err != nil || got != want {
		t.Fatalf("restart must retain first event identity: %+v, %v", got, err)
	}
}

func TestBackendRejectsInvalidSnapshots(t *testing.T) {
	for _, data := range []string{"", "{", "null", `{"Version":2}`, `{"Version":1,"Limits":{"Metadata":2,"Events":1}}`} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := simulation.OpenBackend(simulation.Limits{Metadata: 1, Events: 1}, simulation.FileSnapshot{Path: path}); err == nil {
				t.Fatal("invalid snapshot must not silently initialize an empty backend")
			}
		})
	}
}

func TestBackendConcurrentDelivery(t *testing.T) {
	const count = 32
	limits := simulation.Limits{Metadata: 1, Events: count}
	store := simulation.FileSnapshot{Path: filepath.Join(t.TempDir(), "snapshot.json")}
	b, err := simulation.OpenBackend(limits, store)
	if err != nil {
		t.Fatal(err)
	}
	origin := backendOrigin(1)
	// All activities were captured while the origin was alive, before its exit.
	// The workers vary only delivery order, including delayed exit notifications.
	events := make([]simulation.Activity, count)
	for i := range events {
		events[i] = simulation.Activity{EventID: fmt.Sprintf("E%d", i), Key: origin.Key}
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			e := events[i]
			if _, err := b.DeliverActivity(e); err != nil {
				t.Errorf("activity: %v", err)
			}
			if err := b.DeliverMetadata(origin); err != nil {
				t.Errorf("metadata: %v", err)
			}
			if _, _, err := b.Result(e.EventID); err != nil {
				t.Errorf("query: %v", err)
			}
			if err := b.DeliverExit(origin.Key); err != nil {
				t.Errorf("exit: %v", err)
			}
		}(i)
	}
	close(start)
	workers.Wait()
	for i := 0; i < count; i++ {
		requireBackendResult(t, b, simulation.Attribution{EventID: fmt.Sprintf("E%d", i), Resolved: true, Key: origin.Key, Workload: origin.Workload})
	}
	recovered, err := simulation.OpenBackend(limits, store) // Relinquish b after workers finish.
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		requireBackendResult(t, recovered, simulation.Attribution{EventID: fmt.Sprintf("E%d", i), Resolved: true, Key: origin.Key, Workload: origin.Workload})
	}
}

func TestBackendConcurrentCapacity(t *testing.T) {
	b, err := simulation.OpenBackend(simulation.Limits{Events: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const count = 16
	start := make(chan struct{})
	accepted := make(chan string, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			id := fmt.Sprintf("E%d", i)
			_, err := b.DeliverActivity(simulation.Activity{EventID: id, Key: backendOrigin(1).Key})
			if err == nil {
				accepted <- id
			} else if !errors.Is(err, simulation.ErrCapacity) {
				t.Errorf("unexpected admission error: %v", err)
			}
		}(i)
	}
	close(start)
	workers.Wait()
	close(accepted)
	if len(accepted) != 1 {
		t.Fatalf("capacity one admitted %d concurrent events", len(accepted))
	}
	winner := <-accepted
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("E%d", i)
		if _, found, err := b.Result(id); err != nil || found != (id == winner) {
			t.Fatalf("admission/query mismatch for %s: found=%t, err=%v", id, found, err)
		}
	}
}

func TestBackendRejectsMetadataCounterexample(t *testing.T) {
	b, err := simulation.OpenBackend(simulation.Limits{Metadata: 1, Events: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	origin := backendOrigin(1)
	if err := b.DeliverMetadata(origin); err != nil {
		t.Fatal(err)
	}
	conflict := origin
	conflict.Workload = "incorrect"
	if err := b.DeliverMetadata(conflict); !errors.Is(err, simulation.ErrConflict) {
		t.Fatalf("expected explicit conflict rejection: %v", err)
	}
	got, err := b.DeliverActivity(simulation.Activity{EventID: "E1", Key: origin.Key})
	want := simulation.Attribution{EventID: "E1", Resolved: true, Key: origin.Key, Workload: origin.Workload}
	if err != nil || got != want {
		t.Fatalf("model's A/WA, A/WB, E1 trace: expected %+v, got %+v, %v", want, got, err)
	}
}

func TestBackendRejectsLossySnapshotStrings(t *testing.T) {
	store := &failingSnapshot{}
	limits := simulation.Limits{Metadata: 1, Events: 1}
	b, err := simulation.OpenBackend(limits, store)
	if err != nil {
		t.Fatal(err)
	}
	before := string(store.data)
	bad := backendOrigin(1)
	bad.Workload = string([]byte{0xff})
	if err := b.DeliverMetadata(bad); err == nil {
		t.Fatal("JSON would silently replace the invalid workload on recovery")
	}
	if _, err := b.DeliverActivity(simulation.Activity{EventID: string([]byte{0xff}), Key: bad.Key}); err == nil {
		t.Fatal("JSON would silently change the event ID on recovery")
	}
	bad.Key.Scope = string([]byte{0xff})
	if _, err := b.DeliverActivity(simulation.Activity{EventID: "E1", Key: bad.Key}); err == nil {
		t.Fatal("JSON would silently change the process scope on recovery")
	}
	if string(store.data) != before {
		t.Fatal("rejected invalid strings must not change the durable snapshot")
	}
}
