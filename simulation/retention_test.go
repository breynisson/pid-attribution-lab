package simulation_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"

	simulation "example.com/pid-attribution-lab"
)

func retentionBackend(t *testing.T, limits simulation.Limits, policy simulation.Retention, store simulation.SnapshotStore) *simulation.Backend {
	t.Helper()
	b, err := simulation.OpenBackendWithRetention(limits, policy, store)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func advance(t *testing.T, b *simulation.Backend, now uint64) {
	t.Helper()
	if err := b.AdvanceTo(now); err != nil {
		t.Fatal(err)
	}
}

func metadata(t *testing.T, b *simulation.Backend, m simulation.Metadata) {
	t.Helper()
	if err := b.DeliverMetadata(m); err != nil {
		t.Fatal(err)
	}
}

func activity(t *testing.T, b *simulation.Backend, e simulation.Activity) {
	t.Helper()
	if _, err := b.DeliverActivity(e); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionBoundary(t *testing.T) {
	for _, metadataFirst := range []bool{false, true} {
		for _, delay := range []uint64{4, 5} {
			t.Run(fmt.Sprintf("metadata-first=%t/delay=%d", metadataFirst, delay), func(t *testing.T) {
				b := retentionBackend(t, simulation.Limits{Metadata: 1, Events: 1}, simulation.Retention{MetadataTTL: 5, PendingTTL: 5}, nil)
				m := backendOrigin(1)
				e := simulation.Activity{EventID: "E", Key: m.Key}
				if metadataFirst {
					metadata(t, b, m)
					advance(t, b, delay)
					activity(t, b, e)
				} else {
					activity(t, b, e)
					advance(t, b, delay)
					metadata(t, b, m)
				}
				want := simulation.Attribution{EventID: "E"}
				if delay < 5 {
					want.Resolved, want.Key, want.Workload = true, m.Key, m.Workload
				}
				requireBackendResult(t, b, want)
				s, found, err := b.Status("E")
				if err != nil || !found || s.Expired != (!metadataFirst && delay == 5) {
					t.Fatalf("status: %+v found=%t err=%v", s, found, err)
				}
			})
		}
	}
}

// A was captured alive, exited, and B reused its PID. A's history then expired.
// A late event may wait for fresh A metadata, but must never be attributed to B.
func TestRetentionExpiredHistoryNeverUsesCurrentPID(t *testing.T) {
	b := retentionBackend(t, simulation.Limits{Metadata: 2, Events: 1}, simulation.Retention{MetadataTTL: 2, PendingTTL: 4}, nil)
	a := backendOrigin(7)
	e := simulation.Activity{EventID: "old", Key: a.Key}
	metadata(t, b, a)
	if err := b.DeliverExit(a.Key); err != nil {
		t.Fatal(err)
	}
	advance(t, b, 2)
	replacement := a
	replacement.Key.Incarnation++
	replacement.Workload = "replacement"
	metadata(t, b, replacement)
	activity(t, b, e)
	requireBackendResult(t, b, simulation.Attribution{EventID: e.EventID})
	// Re-admission is an explicit new lease, not a refresh of an existing one.
	metadata(t, b, a)
	requireBackendResult(t, b, simulation.Attribution{EventID: e.EventID, Resolved: true, Key: a.Key, Workload: a.Workload})
}

func TestRetentionDuplicatesDoNotRefreshAndExpiryIsTerminal(t *testing.T) {
	b := retentionBackend(t, simulation.Limits{Metadata: 1, Events: 2}, simulation.Retention{MetadataTTL: 2, PendingTTL: 2}, nil)
	a, other := backendOrigin(1), backendOrigin(2)
	e := simulation.Activity{EventID: "E", Key: other.Key}
	metadata(t, b, a)
	activity(t, b, e)
	advance(t, b, 1)
	metadata(t, b, a)
	activity(t, b, e)
	advance(t, b, 2)
	stats, err := b.Stats()
	if err != nil || stats != (simulation.RetentionStats{Now: 2, Events: 1, Expired: 1}) {
		t.Fatalf("duplicates extended leases: %+v, %v", stats, err)
	}
	metadata(t, b, other) // Freed metadata slot, but E has stopped waiting.
	activity(t, b, e)
	activity(t, b, simulation.Activity{EventID: "E", Key: a.Key}) // Conflicting replay.
	requireBackendResult(t, b, simulation.Attribution{EventID: "E"})
	activity(t, b, simulation.Activity{EventID: "new", Key: other.Key})
	requireBackendResult(t, b, simulation.Attribution{EventID: "new", Resolved: true, Key: other.Key, Workload: other.Workload})
	if _, err := b.DeliverActivity(simulation.Activity{EventID: "over-capacity", Key: a.Key}); !errors.Is(err, simulation.ErrCapacity) {
		t.Fatalf("expired event ID must still occupy capacity: %v", err)
	}
}

func TestRetentionRecoveryPreservesResultsAndExpiry(t *testing.T) {
	limits := simulation.Limits{Metadata: 2, Events: 2}
	policy := simulation.Retention{MetadataTTL: 2, PendingTTL: 3}
	store := simulation.FileSnapshot{Path: filepath.Join(t.TempDir(), "snapshot.json")}
	b := retentionBackend(t, limits, policy, store)
	a, other := backendOrigin(1), backendOrigin(2)
	metadata(t, b, a)
	activity(t, b, simulation.Activity{EventID: "resolved", Key: a.Key})
	activity(t, b, simulation.Activity{EventID: "expired", Key: other.Key})
	advance(t, b, 3)
	b = retentionBackend(t, limits, policy, store)
	requireBackendResult(t, b, simulation.Attribution{EventID: "resolved", Resolved: true, Key: a.Key, Workload: a.Workload})
	s, found, err := b.Status("expired")
	if err != nil || !found || !s.Expired || s.Attribution.Resolved {
		t.Fatalf("recovered expired status: %+v, %t, %v", s, found, err)
	}
	metadata(t, b, other)
	requireBackendResult(t, b, simulation.Attribution{EventID: "expired"})
	stats, err := b.Stats()
	if err != nil || stats.Now != 3 || stats.Pending != 0 || stats.Resolved != 1 || stats.Expired != 1 {
		t.Fatalf("recovered stats: %+v, %v", stats, err)
	}
	if _, err := simulation.OpenBackend(limits, store); err == nil {
		t.Fatal("must reject a different retention policy on reopen")
	}
}

func TestRetentionClockAndOverflow(t *testing.T) {
	for _, ttl := range []uint64{0, 3} {
		t.Run(fmt.Sprint(ttl), func(t *testing.T) {
			b := retentionBackend(t, simulation.Limits{Metadata: 1, Events: 1}, simulation.Retention{MetadataTTL: ttl, PendingTTL: ttl}, nil)
			advance(t, b, math.MaxUint64-2)
			metadata(t, b, backendOrigin(1))
			activity(t, b, simulation.Activity{EventID: "pending", Key: backendOrigin(2).Key})
			advance(t, b, math.MaxUint64)
			advance(t, b, math.MaxUint64)
			if err := b.AdvanceTo(0); !errors.Is(err, simulation.ErrClockBackwards) {
				t.Fatalf("backwards time: %v", err)
			}
			s, err := b.Stats()
			if err != nil || s.Metadata != 1 || s.Pending != 1 {
				t.Fatalf("overflow or zero TTL expired entries: %+v, %v", s, err)
			}
		})
	}
}

func TestRetentionClockCommitFailure(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			store := &failingSnapshot{}
			limits := simulation.Limits{Metadata: 1, Events: 1}
			policy := simulation.Retention{MetadataTTL: 2, PendingTTL: 2}
			b := retentionBackend(t, limits, policy, store)
			metadata(t, b, backendOrigin(1))
			activity(t, b, simulation.Activity{EventID: "E", Key: backendOrigin(2).Key})
			store.failBefore, store.failAfter = !after, after
			if err := b.AdvanceTo(2); !errors.Is(err, simulation.ErrStorage) {
				t.Fatalf("clock commit error: %v", err)
			}
			if _, err := b.Stats(); !errors.Is(err, simulation.ErrStorage) {
				t.Fatalf("uncertain backend served stats: %v", err)
			}
			if _, _, err := b.Status("E"); !errors.Is(err, simulation.ErrStorage) {
				t.Fatalf("uncertain backend served status: %v", err)
			}
			store.failBefore, store.failAfter = false, false
			b = retentionBackend(t, limits, policy, store)
			stats, err := b.Stats()
			want := simulation.RetentionStats{Metadata: 1, Events: 1, Pending: 1}
			if after {
				want = simulation.RetentionStats{Now: 2, Events: 1, Expired: 1}
			}
			if err != nil || stats != want {
				t.Fatalf("non-atomic recovery: %+v want %+v err=%v", stats, want, err)
			}
		})
	}
}

func TestRetentionLegacySnapshot(t *testing.T) {
	limits := simulation.Limits{Metadata: 1, Events: 1}
	m := backendOrigin(1)
	data, err := json.Marshal(struct {
		Version  int
		Limits   simulation.Limits
		Metadata []simulation.Metadata
		Events   []simulation.Activity
	}{1, limits, []simulation.Metadata{m}, []simulation.Activity{{EventID: "E", Key: m.Key}}})
	if err != nil {
		t.Fatal(err)
	}
	store := &failingSnapshot{data: data}
	if _, err := simulation.OpenBackendWithRetention(limits, simulation.Retention{MetadataTTL: 1}, store); err == nil {
		t.Fatal("legacy timestamps cannot be invented")
	}
	b := retentionBackend(t, limits, simulation.Retention{}, store)
	advance(t, b, 100)
	b = retentionBackend(t, limits, simulation.Retention{}, store)
	requireBackendResult(t, b, simulation.Attribution{EventID: "E", Resolved: true, Key: m.Key, Workload: m.Workload})
}

func TestRetentionConcurrentDeadline(t *testing.T) {
	for i := 0; i < 20; i++ {
		b := retentionBackend(t, simulation.Limits{Metadata: 1, Events: 1}, simulation.Retention{PendingTTL: 1}, nil)
		m := backendOrigin(1)
		activity(t, b, simulation.Activity{EventID: "E", Key: m.Key})
		// Whole calls race: metadata either resolves before expiry, or the expiry
		// commits first. Both outcomes are valid; a revived expired event is not.
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, call := range []func() error{func() error { return b.AdvanceTo(1) }, func() error { return b.DeliverMetadata(m) }} {
			wg.Add(1)
			go func(call func() error) {
				defer wg.Done()
				<-start
				if err := call(); err != nil {
					t.Error(err)
				}
			}(call)
		}
		close(start)
		wg.Wait()
		s, found, err := b.Status("E")
		if err != nil || !found || s.Expired == s.Attribution.Resolved {
			t.Fatalf("inconsistent deadline race: %+v, %t, %v", s, found, err)
		}
	}
}
