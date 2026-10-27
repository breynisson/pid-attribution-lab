package simulation_test

import (
	"errors"
	"fmt"
	"sort"
	"testing"

	simulation "example.com/pid-attribution-lab"
)

// A fixed experiment, not a production delay distribution or benchmark. Every
// process starts, captures one event, and exits before the next reuses PID 7.
// Delivery is delayed independently; rejected inputs are not retried.
func TestRetentionCoverageExperiment(t *testing.T) {
	type message struct {
		at       uint64
		metadata bool
		origin   simulation.Metadata
		event    simulation.Activity
	}
	var messages []message
	truth := make(map[string]simulation.Attribution)
	for i := 0; i < 24; i++ {
		base := uint64(2 * i)
		delay := []uint64{1, 3, 6, 9}[(i/2)%4]
		m := simulation.Metadata{Key: simulation.ProcessKey{Scope: "coverage-boot", PID: 7, Incarnation: uint64(i + 1)}, Workload: fmt.Sprintf("W%d", i)}
		e := simulation.Activity{EventID: fmt.Sprintf("E%d", i), Key: m.Key}
		truth[e.EventID] = simulation.Attribution{EventID: e.EventID, Resolved: true, Key: m.Key, Workload: m.Workload}
		mt, et := base, base+delay
		if i%2 != 0 {
			mt, et = et, mt
		}
		messages = append(messages, message{at: mt, metadata: true, origin: m}, message{at: et, event: e})
	}
	// Stable sorting defines the tie rule: original insertion order.
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].at < messages[j].at })
	t.Log("metadata_ttl,pending_ttl,metadata_cap,event_cap,resolved,expired,pending,event_reject,metadata_reject,peak_metadata,peak_pending,retained_events")
	for _, row := range []struct {
		mt, pt uint64
		mc, ec int
	}{
		{0, 0, 24, 24}, {2, 0, 24, 24}, {0, 2, 24, 24},
		{5, 5, 24, 24}, {10, 10, 24, 24},
		{0, 0, 3, 24}, {5, 5, 3, 24}, {10, 10, 3, 24}, {5, 5, 3, 8},
	} {
		b := retentionBackend(t, simulation.Limits{Metadata: row.mc, Events: row.ec}, simulation.Retention{MetadataTTL: row.mt, PendingTTL: row.pt}, nil)
		eventReject, metaReject, peakMeta, peakPending := 0, 0, 0, 0
		accepted := make(map[string]bool)
		for _, msg := range messages {
			advance(t, b, msg.at)
			var err error
			if msg.metadata {
				err = b.DeliverMetadata(msg.origin)
				if errors.Is(err, simulation.ErrCapacity) {
					metaReject++
				}
			} else {
				_, err = b.DeliverActivity(msg.event)
				if errors.Is(err, simulation.ErrCapacity) {
					eventReject++
				} else if err == nil {
					accepted[msg.event.EventID] = true
				}
			}
			if err != nil && !errors.Is(err, simulation.ErrCapacity) {
				t.Fatal(err)
			}
			s, err := b.Stats()
			if err != nil {
				t.Fatal(err)
			}
			peakMeta, peakPending = max(peakMeta, s.Metadata), max(peakPending, s.Pending)
			for id := range accepted {
				r, found, err := b.Result(id)
				if err != nil || !found || (r.Resolved && r != truth[id]) {
					t.Fatalf("coverage must never trade away capture truth: %s got %+v found=%t err=%v", id, r, found, err)
				}
			}
		}
		advance(t, b, 80) // Finish finite pending leases; unlimited waits remain.
		s, err := b.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if s.Resolved+s.Expired+s.Pending+eventReject != 24 || s.Events != len(accepted) {
			t.Fatalf("lost event accounting: %+v rejected=%d", s, eventReject)
		}
		t.Logf("%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d", row.mt, row.pt, row.mc, row.ec, s.Resolved, s.Expired, s.Pending, eventReject, metaReject, peakMeta, peakPending, s.Events)
	}
}
