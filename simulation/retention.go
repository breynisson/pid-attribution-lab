package simulation

import (
	"encoding/json"
	"errors"
	"slices"
	"unicode/utf8"
)

// Retention uses caller-defined logical ticks from first backend admission.
// A zero TTL disables that expiry. Expiry is inclusive: age >= TTL.
type Retention struct {
	MetadataTTL uint64
	PendingTTL  uint64
}

// EventStatus distinguishes terminal uncertainty from an event still waiting.
type EventStatus struct {
	Attribution Attribution
	Expired     bool
}

type RetentionStats struct {
	Now      uint64
	Metadata int
	Events   int
	Pending  int
	Resolved int
	Expired  int
}

func expired(now, acceptedAt, ttl uint64) bool {
	return ttl != 0 && now >= acceptedAt && now-acceptedAt >= ttl
}

// AdvanceTo atomically persists time and expirations. Wall time and downtime do
// not advance this clock. Expired pending events retain their IDs and results.
func (b *Backend) AdvanceTo(now uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return b.failed
	}
	if now < b.snapshot.Now {
		return ErrClockBackwards
	}
	if now == b.snapshot.Now {
		return nil
	}
	next := b.snapshot
	next.Now = now
	next.Metadata = slices.DeleteFunc(slices.Clone(next.Metadata), func(m cachedMetadata) bool {
		return expired(now, m.AcceptedAt, next.Retention.MetadataTTL)
	})
	next.Events = slices.Clone(next.Events)
	for i := range next.Events {
		e := &next.Events[i]
		if !e.Result.Resolved && expired(now, e.AcceptedAt, next.Retention.PendingTTL) {
			e.Expired = true
		}
	}
	return b.commit(next)
}

func (b *Backend) Status(eventID string) (EventStatus, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return EventStatus{}, false, b.failed
	}
	for _, e := range b.snapshot.Events {
		if e.Activity.EventID == eventID {
			return EventStatus{Attribution: e.Result, Expired: e.Expired}, true, nil
		}
	}
	return EventStatus{}, false, nil
}

func (b *Backend) Stats() (RetentionStats, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return RetentionStats{}, b.failed
	}
	s := RetentionStats{Now: b.snapshot.Now, Metadata: len(b.snapshot.Metadata), Events: len(b.snapshot.Events)}
	for _, e := range b.snapshot.Events {
		switch {
		case e.Result.Resolved:
			s.Resolved++
		case e.Expired:
			s.Expired++
		default:
			s.Pending++
		}
	}
	return s, nil
}

// Restore published answers directly. In particular, do not lose resolved
// answers whose metadata expired, or requeue terminal expired events.
func coreFromSnapshot(s backendSnapshot) *Attributor {
	a := New()
	for _, m := range s.Metadata {
		a.metadata[m.Metadata.Key] = m.Metadata
	}
	for _, e := range s.Events {
		a.results[e.Activity.EventID] = e.Result
		if !e.Result.Resolved && !e.Expired {
			a.pending[e.Activity.Key] = append(a.pending[e.Activity.Key], e.Activity.EventID)
		}
	}
	return a
}

func decodeSnapshot(data []byte, retention Retention) (backendSnapshot, error) {
	var header struct{ Version int }
	if err := json.Unmarshal(data, &header); err != nil {
		return backendSnapshot{}, err
	}
	if header.Version != 1 {
		var s backendSnapshot
		err := json.Unmarshal(data, &s)
		return s, err
	}
	if retention != (Retention{}) {
		return backendSnapshot{}, errors.New("version-1 snapshot has no retention timestamps")
	}
	var old struct {
		Version  int
		Limits   Limits
		Metadata []Metadata
		Events   []Activity
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return backendSnapshot{}, err
	}
	s := backendSnapshot{Version: 2, Limits: old.Limits}
	a := New()
	for _, m := range old.Metadata {
		s.Metadata = append(s.Metadata, cachedMetadata{Metadata: m})
		a.DeliverMetadata(m)
	}
	for _, e := range old.Events {
		s.Events = append(s.Events, retainedEvent{Activity: e, Result: a.DeliverActivity(e)})
	}
	return s, nil // Common validation below also rejects legacy duplicates.
}

func validateSnapshot(s backendSnapshot) error {
	metadata := make(map[ProcessKey]Metadata)
	for _, m := range s.Metadata {
		_, duplicate := metadata[m.Metadata.Key]
		if !validMetadata(m.Metadata) || duplicate || m.AcceptedAt > s.Now || expired(s.Now, m.AcceptedAt, s.Retention.MetadataTTL) {
			return errors.New("invalid, duplicate, or expired metadata in snapshot")
		}
		metadata[m.Metadata.Key] = m.Metadata
	}
	seen := make(map[string]bool)
	for _, e := range s.Events {
		r := e.Result
		if !validActivity(e.Activity) || seen[e.Activity.EventID] || e.AcceptedAt > s.Now || r.EventID != e.Activity.EventID {
			return errors.New("invalid or duplicate event in snapshot")
		}
		seen[e.Activity.EventID] = true
		if r.Resolved {
			if e.Expired || r.Key != e.Activity.Key || !utf8.ValidString(r.Workload) {
				return errors.New("invalid resolved result in snapshot")
			}
			continue
		}
		if r.Key != (ProcessKey{}) || r.Workload != "" || e.Expired != expired(s.Now, e.AcceptedAt, s.Retention.PendingTTL) {
			return errors.New("invalid unresolved result or expiry in snapshot")
		}
		if _, available := metadata[e.Activity.Key]; available && !e.Expired {
			return errors.New("pending event with available metadata in snapshot")
		}
	}
	return nil
}
