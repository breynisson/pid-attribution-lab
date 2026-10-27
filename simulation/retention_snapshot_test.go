package simulation

import (
	"encoding/json"
	"slices"
	"testing"
)

type snapshotFixture []byte

func (s snapshotFixture) Load() ([]byte, error) { return s, nil }
func (s snapshotFixture) Save([]byte) error     { panic("reopening must not write") }

// Recovery must reject structurally valid JSON with contradictory persisted
// state, rather than turn it into a different accepted history.
func TestRetentionRejectsInconsistentSnapshots(t *testing.T) {
	key := ProcessKey{Scope: "boot", PID: 7, Incarnation: 1}
	base := backendSnapshot{
		Version: 2, Limits: Limits{Metadata: 2, Events: 2},
		Retention: Retention{MetadataTTL: 2, PendingTTL: 2}, Now: 1,
		Metadata: []cachedMetadata{{Metadata: Metadata{Key: key, Workload: "A"}}},
		Events:   []retainedEvent{{Activity: Activity{EventID: "E", Key: key}, Result: Attribution{EventID: "E", Resolved: true, Key: key, Workload: "A"}}},
	}
	for name, corrupt := range map[string]func(*backendSnapshot){
		"future metadata":                    func(s *backendSnapshot) { s.Metadata[0].AcceptedAt = 2 },
		"future event":                       func(s *backendSnapshot) { s.Events[0].AcceptedAt = 2 },
		"stale metadata":                     func(s *backendSnapshot) { s.Now = 2 },
		"duplicate metadata":                 func(s *backendSnapshot) { s.Metadata = append(s.Metadata, s.Metadata[0]) },
		"duplicate event":                    func(s *backendSnapshot) { s.Events = append(s.Events, s.Events[0]) },
		"wrong origin":                       func(s *backendSnapshot) { s.Events[0].Result.Key.Incarnation++ },
		"wrong event ID":                     func(s *backendSnapshot) { s.Events[0].Result.EventID = "other" },
		"resolved and expired":               func(s *backendSnapshot) { s.Events[0].Expired = true },
		"pending despite available metadata": func(s *backendSnapshot) { s.Events[0].Result = Attribution{EventID: "E"} },
		"missed deadline":                    func(s *backendSnapshot) { s.Metadata = nil; s.Now = 2; s.Events[0].Result = Attribution{EventID: "E"} },
		"premature expiry": func(s *backendSnapshot) {
			s.Metadata = nil
			s.Events[0].Result = Attribution{EventID: "E"}
			s.Events[0].Expired = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			s.Metadata, s.Events = slices.Clone(base.Metadata), slices.Clone(base.Events)
			corrupt(&s)
			data, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := OpenBackendWithRetention(base.Limits, base.Retention, snapshotFixture(data)); err == nil {
				t.Fatal("accepted inconsistent snapshot")
			}
		})
	}
}
