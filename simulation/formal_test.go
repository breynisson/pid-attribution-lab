package simulation_test

import (
	"os"
	"strings"
	"testing"

	simulation "example.com/pid-attribution-lab"
)

// Conflicts.tla's StableResults property is weaker than capture truth. This
// characterizes current result stability under an invalid metadata stream;
// it does not make conflicting metadata a supported source of attribution.
func TestTLCResolvedResultSurvivesMetadataConflict(t *testing.T) {
	for _, metadataFirst := range []bool{false, true} {
		name := "activity-first"
		if metadataFirst {
			name = "metadata-first"
		}
		t.Run(name, func(t *testing.T) {
			backend := newAttributor()
			origin := simulation.Metadata{
				Key: simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 1}, Workload: "WA",
			}
			event := simulation.Activity{EventID: "E1", Key: origin.Key}
			want := simulation.Attribution{EventID: event.EventID, Resolved: true, Key: origin.Key, Workload: origin.Workload}
			if metadataFirst {
				backend.DeliverMetadata(origin)
			}
			initial := simulation.Attribution{EventID: event.EventID}
			if metadataFirst {
				initial = want
			}
			if got := backend.DeliverActivity(event); got != initial {
				t.Fatalf("initial delivery: expected %+v, got %+v", initial, got)
			}
			if !metadataFirst {
				backend.DeliverMetadata(origin)
			}
			requireResult(t, backend, want)
			conflict := origin
			conflict.Workload = "WB"
			backend.DeliverMetadata(conflict)
			requireResult(t, backend, want)
			if got := backend.DeliverActivity(event); got != want {
				t.Fatalf("replay must retain the published result: expected %+v, got %+v", want, got)
			}
			requireResult(t, backend, want)
		})
	}
}

// Preserve the two TLC counterexamples as direct, intentionally failing safety
// assertions. Normally the parent check below runs each in an isolated process.
func TestTLCCaptureTruthWithUntrustedInputs(t *testing.T) {
	if os.Getenv("PID_ATTRIBUTION_UNTRUSTED_DEMO") != "1" {
		t.Skip("intentional failure demo; exercised by TestTLCInputAssumptionCounterexamples")
	}
	a := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 7, Incarnation: 1}, Workload: "WA",
	}
	b := simulation.Metadata{
		Key: simulation.ProcessKey{Scope: "lab", PID: 8, Incarnation: 2}, Workload: "WB",
	}
	for _, scenario := range []struct {
		name    string
		truth   simulation.Attribution
		deliver func(attributor)
	}{
		{
			name:  "corrupt-first-activity",
			truth: simulation.Attribution{EventID: "E2", Resolved: true, Key: b.Key, Workload: b.Workload},
			deliver: func(backend attributor) {
				// replay-first-corrupt.cfg: E2 was captured from B, but its
				// first delivered payload claims A. Only A's metadata arrives.
				backend.DeliverActivity(simulation.Activity{EventID: "E2", Key: a.Key})
				backend.DeliverMetadata(a)
			},
		},
		{
			name:  "conflicting-metadata",
			truth: simulation.Attribution{EventID: "E1", Resolved: true, Key: a.Key, Workload: a.Workload},
			deliver: func(backend attributor) {
				// metadata-conflict.cfg: A/WA is overwritten by A/WB before
				// E1 arrives. The private truth above retains the original WA.
				backend.DeliverMetadata(a)
				conflict := a
				conflict.Workload = "WB"
				backend.DeliverMetadata(conflict)
				backend.DeliverActivity(simulation.Activity{EventID: "E1", Key: a.Key})
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			backend := newAttributor()
			scenario.deliver(backend)
			got, present := backend.Result(scenario.truth.EventID)
			if !present {
				t.Fatal("delivered event is missing from results")
			}
			// CaptureTruth permits honest uncertainty. It forbids a resolved
			// answer that differs from independently recorded capture truth.
			if got.Resolved && got != scenario.truth {
				t.Fatalf("capture truth violated by untrusted input:\ntruth: %+v\nactual: %+v", scenario.truth, got)
			}
		})
	}
}

func TestTLCInputAssumptionCounterexamples(t *testing.T) {
	if os.Getenv("PID_ATTRIBUTION_FAULTY") == "1" {
		t.Skip("these counterexamples characterize the corrected implementation")
	}
	for _, name := range []string{"corrupt-first-activity", "conflicting-metadata"} {
		t.Run(name, func(t *testing.T) {
			testName := "TestTLCCaptureTruthWithUntrustedInputs/" + name
			passed, output := runNamedContract(t, testName, "", "PID_ATTRIBUTION_UNTRUSTED_DEMO=1")
			if passed {
				t.Fatal("capture truth now holds; review the model/input contract and update this expected counterexample")
			}
			if !strings.Contains(output, "capture truth violated by untrusted input:") {
				t.Fatalf("failed for a reason other than the expected safety violation:\n%s", output)
			}
			t.Logf("reproduced TLC input-assumption failure:\n%s", output)
		})
	}
}
