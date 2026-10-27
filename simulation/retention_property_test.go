package simulation_test

import (
	"fmt"
	"testing"

	simulation "example.com/pid-attribution-lab"
	"pgregory.net/rapid"
)

func TestPropertyRetentionWithinWindow(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		gap := rapid.Uint64Range(0, 20).Draw(t, "gap")
		policy := simulation.Retention{
			MetadataTTL: gap + rapid.Uint64Range(1, 10).Draw(t, "metadata margin"),
			PendingTTL:  gap + rapid.Uint64Range(1, 10).Draw(t, "pending margin"),
		}
		m := backendOrigin(rapid.IntRange(1, 10).Draw(t, "pid"))
		e := simulation.Activity{EventID: "E", Key: m.Key}
		want := simulation.Attribution{EventID: "E", Resolved: true, Key: m.Key, Workload: m.Workload}
		for _, metadataFirst := range []bool{false, true} {
			b, err := simulation.OpenBackendWithRetention(simulation.Limits{Metadata: 1, Events: 1}, policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			if metadataFirst {
				err = b.DeliverMetadata(m)
			} else {
				_, err = b.DeliverActivity(e)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := b.AdvanceTo(gap); err != nil {
				t.Fatal(err)
			}
			if metadataFirst {
				_, err = b.DeliverActivity(e)
			} else {
				err = b.DeliverMetadata(m)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, found, err := b.Result("E")
			if err != nil || !found || got != want {
				t.Fatalf("within-window order=%t: got %+v found=%t err=%v want %+v", metadataFirst, got, found, err, want)
			}
		}
	})
}

func TestPropertyRetentionSchedules(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		policy := simulation.Retention{MetadataTTL: rapid.Uint64Range(0, 6).Draw(t, "metadata TTL"), PendingTTL: rapid.Uint64Range(0, 6).Draw(t, "pending TTL")}
		limits := simulation.Limits{Metadata: 3, Events: 6}
		store := &failingSnapshot{}
		b, err := simulation.OpenBackendWithRetention(limits, policy, store)
		if err != nil {
			t.Fatal(err)
		}
		var metas []simulation.Metadata
		var events []simulation.Activity
		var truth []simulation.Attribution
		// Valid physical prefix: start A, capture twice, exit; then B and C reuse
		// PID 7. All generated operations below vary transport, never capture a
		// dead process. The oracle is frozen here, before any backend delivery.
		for lifetime := 1; lifetime <= 3; lifetime++ {
			m := simulation.Metadata{Key: simulation.ProcessKey{Scope: "boot", PID: 7, Incarnation: uint64(lifetime)}, Workload: fmt.Sprintf("W%d", lifetime)}
			metas = append(metas, m)
			for j := 0; j < 2; j++ {
				id := fmt.Sprintf("E%d-%d", lifetime, j)
				events = append(events, simulation.Activity{EventID: id, Key: m.Key})
				truth = append(truth, simulation.Attribution{EventID: id, Resolved: true, Key: m.Key, Workload: m.Workload})
			}
		}
		// External observation log. Each metadata message has at most one lease
		// here; deterministic regressions separately cover duplicate/re-admission.
		metaTime := []int{-1, -1, -1}
		eventTime := []int{-1, -1, -1, -1, -1, -1}
		now := 0
		check := func() {
			for i, e := range events {
				s, found, err := b.Status(e.EventID)
				if err != nil || found != (eventTime[i] >= 0) {
					t.Fatalf("admission status %s: %+v found=%t err=%v", e.EventID, s, found, err)
				}
				if !found {
					continue
				}
				et, mt := eventTime[i], metaTime[i/2]
				// Geometric interval oracle: leases must intersect. It neither
				// calls the resolver nor copies its pending-index transitions.
				join := max(et, mt)
				resolved := mt >= 0 && (policy.MetadataTTL == 0 || join-mt < int(policy.MetadataTTL)) && (policy.PendingTTL == 0 || join-et < int(policy.PendingTTL))
				want := simulation.Attribution{EventID: e.EventID}
				if resolved {
					want = truth[i]
				}
				expired := !resolved && policy.PendingTTL != 0 && now-et >= int(policy.PendingTTL)
				if s.Attribution != want || s.Expired != expired {
					t.Fatalf("event %s now=%d eventTime=%d metaTime=%d TTL=%+v: got %+v want %+v expired=%t", e.EventID, now, et, mt, policy, s, want, expired)
				}
			}
		}
		steps := rapid.IntRange(10, 60).Draw(t, "steps")
		for step := 0; step < steps; step++ {
			switch rapid.IntRange(0, 4).Draw(t, "action") {
			case 0:
				i := rapid.IntRange(0, 2).Draw(t, "metadata")
				if metaTime[i] < 0 {
					if err := b.DeliverMetadata(metas[i]); err != nil {
						t.Fatal(err)
					}
					metaTime[i] = now
				}
			case 1, 2: // Includes replays, which must not extend pending leases.
				i := rapid.IntRange(0, 5).Draw(t, "activity")
				if _, err := b.DeliverActivity(events[i]); err != nil {
					t.Fatal(err)
				}
				if eventTime[i] < 0 {
					eventTime[i] = now
				}
			case 3:
				now += rapid.IntRange(0, 3).Draw(t, "advance")
				if err := b.AdvanceTo(uint64(now)); err != nil {
					t.Fatal(err)
				}
			case 4:
				b, err = simulation.OpenBackendWithRetention(limits, policy, store)
				if err != nil {
					t.Fatal(err)
				}
			}
			check()
		}
	})
}
