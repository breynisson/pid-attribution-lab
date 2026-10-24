package simulation_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	simulation "example.com/pid-attribution-lab"
)

type captureFunc func(int, string) (simulation.ProcessSnapshot, error)

func (f captureFunc) Capture(pid int, kind string) (simulation.ProcessSnapshot, error) {
	return f(pid, kind)
}

const (
	bootA = "11111111-1111-4111-8111-111111111111"
	bootB = "22222222-2222-4222-8222-222222222222"
)

func TestSensorSnapshotSurvivesPIDReuseAndBoot(t *testing.T) {
	bootPath := filepath.Join(t.TempDir(), "boot_id")
	writeBoot := func(id string) {
		t.Helper()
		if err := os.WriteFile(bootPath, []byte(id+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeBoot(bootA)
	boots, err := simulation.NewBootDetector(func() (string, error) { return simulation.ReadBootIDFile(bootPath) })
	if err != nil {
		t.Fatal(err)
	}
	// The source fixture owns physical state; the backend never sees this state
	// or the expected capture ledger below. Each call copies one coherent event.
	physical := simulation.ProcessSnapshot{PID: 7, Incarnation: 1, Workload: "WA"}
	sensor, err := simulation.NewSensor("host/ns", boots, captureFunc(func(pid int, kind string) (simulation.ProcessSnapshot, error) {
		snapshot := physical
		if physical.Workload == "WA" {
			// A exits and B starts AFTER the coherent snapshot, but BEFORE
			// Sensor.Capture returns. A late second lookup would return B.
			physical = simulation.ProcessSnapshot{PID: 7, Incarnation: 2, Workload: "WB"}
		}
		return snapshot, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	a, ma, err := sensor.Capture("EA", 7, "open")
	if err != nil {
		t.Fatal(err)
	}
	// B is now live on the same PID; A's delivery is delayed.
	b, mb, err := sensor.Capture("EB", 7, "connect")
	if err != nil {
		t.Fatal(err)
	}
	// A physical reboot kills B and resets allocation before C starts. The
	// detector has not been explicitly polled; Capture must read the fresh ID.
	writeBoot(bootB)
	physical = simulation.ProcessSnapshot{PID: 7, Incarnation: 1, Workload: "WC"}
	c, mc, err := sensor.Capture("EC", 7, "open")
	if err != nil {
		t.Fatal(err)
	}
	// Expected keys are specified independently, not copied from sensor output.
	wants := []simulation.Attribution{
		{EventID: "EA", Resolved: true, Key: simulation.ProcessKey{Scope: `["host/ns","11111111-1111-4111-8111-111111111111"]`, PID: 7, Incarnation: 1}, Workload: "WA"},
		{EventID: "EB", Resolved: true, Key: simulation.ProcessKey{Scope: `["host/ns","11111111-1111-4111-8111-111111111111"]`, PID: 7, Incarnation: 2}, Workload: "WB"},
		{EventID: "EC", Resolved: true, Key: simulation.ProcessKey{Scope: `["host/ns","22222222-2222-4222-8222-222222222222"]`, PID: 7, Incarnation: 1}, Workload: "WC"},
	}
	backend, err := simulation.OpenBackend(simulation.Limits{Metadata: 3, Events: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []simulation.Activity{c, a, b} {
		if _, err := backend.DeliverActivity(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.DeliverMetadata(mc); err != nil {
		t.Fatal(err)
	}
	requireBackendResult(t, backend, simulation.Attribution{EventID: "EA"})
	requireBackendResult(t, backend, simulation.Attribution{EventID: "EB"})
	for _, metadata := range []simulation.Metadata{mb, ma} {
		if err := backend.DeliverMetadata(metadata); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range wants {
		requireBackendResult(t, backend, want)
	}
}

func TestSensorRejectsBootChangeDuringCapture(t *testing.T) {
	boot := bootA
	boots, err := simulation.NewBootDetector(func() (string, error) { return boot, nil })
	if err != nil {
		t.Fatal(err)
	}
	sensor, err := simulation.NewSensor("host/ns", boots, captureFunc(func(int, string) (simulation.ProcessSnapshot, error) {
		snapshot := simulation.ProcessSnapshot{PID: 7, Incarnation: 1, Workload: "WA"}
		boot = bootB // Reboot after the event snapshot, before Capture completes.
		return snapshot, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	event, metadata, err := sensor.Capture("E1", 7, "open")
	if !errors.Is(err, simulation.ErrBootChanged) || event != (simulation.Activity{}) || metadata != (simulation.Metadata{}) {
		t.Fatalf("cross-boot capture must not be delivered: %+v, %+v, %v", event, metadata, err)
	}
}

func TestSensorReadFailureDoesNotUseCachedBoot(t *testing.T) {
	reads := 0
	boots, err := simulation.NewBootDetector(func() (string, error) {
		reads++
		if reads == 1 {
			return bootA, nil
		}
		return "", errors.New("boot source unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	sensor, err := simulation.NewSensor("host/ns", boots, captureFunc(func(int, string) (simulation.ProcessSnapshot, error) {
		return simulation.ProcessSnapshot{PID: 7, Incarnation: 1}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	event, metadata, err := sensor.Capture("E1", 7, "open")
	if err == nil || event != (simulation.Activity{}) || metadata != (simulation.Metadata{}) {
		t.Fatalf("failed final boot read must not fall back to cached ID: %+v, %+v, %v", event, metadata, err)
	}
}

func TestBootDetectorReportsChanges(t *testing.T) {
	boot := bootA
	boots, err := simulation.NewBootDetector(func() (string, error) { return boot, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		id      string
		changed bool
	}{{bootA, false}, {bootA, false}, {bootB, true}, {bootB, false}} {
		boot = step.id
		got, err := boots.Observe()
		if err != nil || got.ID != step.id || got.Changed != step.changed {
			t.Fatalf("fresh boot observation: %+v, %v; expected %+v", got, err, step)
		}
	}
}

func TestBootIDFileValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot_id")
	for _, contents := range []string{"", "boot-A", "zzzzzzzz-1111-4111-8111-111111111111"} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := simulation.ReadBootIDFile(path); err == nil {
			t.Fatalf("accepted invalid boot UUID %q", contents)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := simulation.ReadBootIDFile(path); err == nil {
		t.Fatal("missing boot source must fail")
	}
}

func TestLinuxBootIDIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux procfs; filesystem fixture tests run on every platform")
	}
	if id, err := simulation.ReadLinuxBootID(); err != nil || id == "" {
		t.Fatalf("Linux boot ID: %q, %v", id, err)
	}
}

func TestSensorConcurrentCaptures(t *testing.T) {
	boots, err := simulation.NewBootDetector(func() (string, error) { return bootA, nil })
	if err != nil {
		t.Fatal(err)
	}
	// The source has no lock: Sensor promises to serialize calls. Each capture
	// starts the next synthetic lifetime after the previous one has exited.
	var incarnation uint64
	sensor, err := simulation.NewSensor("host/ns", boots, captureFunc(func(pid int, kind string) (simulation.ProcessSnapshot, error) {
		incarnation++
		return simulation.ProcessSnapshot{PID: pid, Incarnation: incarnation, Workload: "WA"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	const count = 16
	start := make(chan struct{})
	events := make(chan simulation.Activity, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			event, _, err := sensor.Capture(fmt.Sprintf("E%d", i), 7, "open")
			if err != nil {
				t.Errorf("capture: %v", err)
				return
			}
			events <- event
		}(i)
	}
	close(start)
	workers.Wait()
	close(events)
	seen := make(map[uint64]bool)
	for e := range events {
		if e.Key.Incarnation == 0 || e.Key.Incarnation > count || seen[e.Key.Incarnation] {
			t.Fatalf("source serialization lost unique incarnation: %+v", e)
		}
		seen[e.Key.Incarnation] = true
	}
	if len(seen) != count {
		t.Fatalf("expected %d captured lifetimes, got %d", count, len(seen))
	}
}
