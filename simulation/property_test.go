package simulation_test

import (
	"fmt"
	"strings"
	"testing"

	simulation "example.com/pid-attribution-lab"
	"pgregory.net/rapid"
)

// Keep the causal five-step scenario fixed while varying its identities.
func TestPropertyDelayedEventAfterPIDReuse(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pid := rapid.IntRange(1, 3).Draw(t, "pid")
		first := rapid.Uint64Range(1, 100).Draw(t, "first incarnation")
		gap := rapid.Uint64Range(1, 100).Draw(t, "incarnation gap")
		workload := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "workload label")
		a := simulation.Metadata{
			Key:        simulation.ProcessKey{Scope: "lab", PID: pid, Incarnation: first},
			Executable: "A", Workload: workload + "-A",
		}
		b := simulation.Metadata{
			Key:        simulation.ProcessKey{Scope: "lab", PID: pid, Incarnation: first + gap},
			Executable: "B", Workload: workload + "-B",
		}
		backend := newAttributor()
		backend.DeliverMetadata(a)
		event := simulation.Activity{EventID: "E", Key: a.Key, Kind: "file-open"}
		// Capture the expected answer before exit or reuse, without backend state.
		want := simulation.Attribution{
			EventID: event.EventID, Resolved: true, Key: a.Key, Workload: a.Workload,
		}
		backend.DeliverExit(a.Key)
		backend.DeliverMetadata(b)
		got := backend.DeliverActivity(event)
		if got != want {
			t.Fatalf("capture origin: %+v\ndelivery: metadata A, exit A, metadata B, activity E\nexpected: %+v\nactual:   %+v", a, want, got)
		}
	})
}

// A message contains exactly one payload, selected by kind. All transport state
// lives in the harness; the backend only receives the selected payload.
type queuedMessage struct {
	kind     string
	metadata simulation.Metadata
	key      simulation.ProcessKey
	activity simulation.Activity
}

type observedActivity struct {
	eventID string
	result  simulation.Attribution
}

func TestStatefulAttribution(t *testing.T) {
	// Safety alone lets generation reach wrong-lifetime matches even when an
	// earlier unresolved result would already violate useful attribution.
	for _, property := range []string{"Safety", "UsefulAttribution"} {
		t.Run(property, func(t *testing.T) {
			rapid.Check(t, func(t *rapid.T) {
				checkStatefulAttribution(t, property == "UsefulAttribution")
			})
		})
	}
}

func checkStatefulAttribution(t *rapid.T, checkUseful bool) {
	backend := newAttributor()
	live := make(map[int]simulation.Metadata)
	oracle := make(map[string]simulation.Attribution)
	deliveredMetadata := make(map[simulation.ProcessKey]bool)
	var pending []queuedMessage
	var results []observedActivity
	var trace []string
	var incarnation uint64
	var eventNumber int

	t.Repeat(map[string]func(*rapid.T){
		"step": func(t *rapid.T) {
			// Iterate the fixed PID pool in order instead of ranging over maps:
			// candidate ordering must be stable for Rapid replay and shrinking.
			var freePIDs, livePIDs []int
			for _, pid := range []int{1, 2, 3} {
				if _, alive := live[pid]; alive {
					livePIDs = append(livePIDs, pid)
				} else {
					freePIDs = append(freePIDs, pid)
				}
			}
			var actions []string
			if len(freePIDs) > 0 {
				actions = append(actions, "start")
			}
			if len(livePIDs) > 0 {
				actions = append(actions, "activity", "exit")
			}
			if len(pending) > 0 {
				actions = append(actions, "deliver")
			}
			// Draw only enabled actions and valid targets; no broad filters or
			// discarded examples are needed to maintain physical causality.
			switch rapid.SampledFrom(actions).Draw(t, "enabled action") {
			case "start":
				pid := rapid.SampledFrom(freePIDs).Draw(t, "free PID")
				incarnation++ // Never reused, even when the numeric PID is reused.
				metadata := simulation.Metadata{
					Key:        simulation.ProcessKey{Scope: "lab", PID: pid, Incarnation: incarnation},
					Executable: fmt.Sprintf("process-%d", incarnation),
					Workload:   fmt.Sprintf("workload-%d", incarnation),
				}
				live[pid] = metadata
				pending = append(pending, queuedMessage{kind: "metadata", metadata: metadata})
				trace = append(trace, fmt.Sprintf("start %+v; queue metadata", metadata))
			case "activity":
				pid := rapid.SampledFrom(livePIDs).Draw(t, "live PID")
				origin := live[pid]
				eventNumber++
				event := simulation.Activity{
					EventID: fmt.Sprintf("E%d", eventNumber), Key: origin.Key, Kind: "file-open",
				}
				// Truth is copied from the world at capture time. It never reads
				// backend state or delivered metadata to determine event origin.
				oracle[event.EventID] = simulation.Attribution{
					EventID: event.EventID, Resolved: true, Key: origin.Key, Workload: origin.Workload,
				}
				pending = append(pending, queuedMessage{kind: "activity", activity: event})
				trace = append(trace, fmt.Sprintf("capture %s from %+v; queue activity", event.EventID, origin))
			case "exit":
				pid := rapid.SampledFrom(livePIDs).Draw(t, "live PID")
				key := live[pid].Key
				delete(live, pid) // Physical exit frees the PID before notification delivery.
				pending = append(pending, queuedMessage{kind: "exit", key: key})
				trace = append(trace, fmt.Sprintf("exit %+v; queue exit", key))
			case "deliver":
				index := rapid.IntRange(0, len(pending)-1).Draw(t, "queue index")
				message := pending[index]
				pending = append(pending[:index], pending[index+1:]...)
				switch message.kind {
				case "metadata":
					backend.DeliverMetadata(message.metadata)
					deliveredMetadata[message.metadata.Key] = true
					trace = append(trace, fmt.Sprintf("deliver metadata %+v", message.metadata))
				case "exit":
					backend.DeliverExit(message.key)
					trace = append(trace, fmt.Sprintf("deliver exit %+v", message.key))
				case "activity":
					result := backend.DeliverActivity(message.activity)
					results = append(results, observedActivity{eventID: message.activity.EventID, result: result})
					trace = append(trace, fmt.Sprintf("deliver activity %+v -> %+v", message.activity, result))
				}
			}
		},
		"": func(t *rapid.T) {
			// Repeat checks after every action, including every message delivery.
			// Query actual stored results to observe metadata-triggered updates;
			// never redeliver an activity or derive a result from oracle metadata.
			for i, observation := range results {
				got, ok := backend.Result(observation.eventID)
				want := oracle[observation.eventID]
				property := ""
				if !ok {
					property = "delivered event has a result"
				} else if observation.result.Resolved && observation.result != want {
					property = "no false attribution in activity response"
				} else if got.Resolved && got != want {
					property = "no false attribution"
				} else if got.Resolved && !deliveredMetadata[want.Key] {
					property = "honest uncertainty"
				} else if observation.result.Resolved && got != observation.result {
					property = "historical identity"
				} else if checkUseful && deliveredMetadata[want.Key] && !got.Resolved {
					property = "useful attribution"
				}
				if property != "" {
					expected := want
					if !deliveredMetadata[want.Key] {
						expected = simulation.Attribution{EventID: want.EventID}
					}
					t.Fatalf("%s violated\nworld/delivery trace:\n%s\ncapture origin: %+v\nexpected with delivered metadata: %+v\nactual:                           %+v",
						property, strings.Join(trace, "\n"), want, expected, got)
				}
				results[i].result = got
			}
		},
	})
}
