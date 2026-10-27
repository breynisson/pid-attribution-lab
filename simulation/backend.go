package simulation

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"unicode/utf8"
)

var (
	ErrCapacity       = errors.New("attribution capacity exhausted")
	ErrConflict       = errors.New("metadata conflicts with retained identity")
	ErrStorage        = errors.New("storage outcome uncertain; reopen backend before continuing")
	ErrClockBackwards = errors.New("logical clock cannot move backwards")
)

// Limits bound retained entries, including pending events, not bytes or queues.
// Zero rejects new entries. Event IDs are never evicted; metadata may expire.
type Limits struct {
	Metadata int
	Events   int
}

// SnapshotStore has a single owner. Load returns nil for a new store. Save must
// atomically replace its snapshot and durably commit it before returning nil.
// An error may mean the snapshot committed but acknowledgement failed.
type SnapshotStore interface {
	Load() ([]byte, error)
	Save([]byte) error
}

type backendSnapshot struct {
	Version   int
	Limits    Limits
	Retention Retention
	Now       uint64
	Metadata  []cachedMetadata
	Events    []retainedEvent
}

type cachedMetadata struct {
	Metadata   Metadata
	AcceptedAt uint64
}

type retainedEvent struct {
	Activity   Activity
	Result     Attribution
	AcceptedAt uint64
	Expired    bool
}

// Backend adds bounded admission, whole-call serialization, and optional durable
// snapshots to Attributor. Use OpenBackend; do not copy a Backend. Only one
// Backend may own a SnapshotStore at a time, including across processes.
// Metadata is immutable; conflicts with cached records are rejected. Event IDs retain
// first-delivery-wins semantics. Neither rule authenticates the first input.
type Backend struct {
	mu       sync.Mutex
	core     *Attributor
	snapshot backendSnapshot
	store    SnapshotStore
	failed   error
}

// OpenBackend retains metadata and pending events indefinitely. A nil store is
// volatile. Reopening requires the same limits and retention configuration.
func OpenBackend(limits Limits, store SnapshotStore) (*Backend, error) {
	return OpenBackendWithRetention(limits, Retention{}, store)
}

// OpenBackendWithRetention enables explicit logical-time expiry. See RETENTION.md.
func OpenBackendWithRetention(limits Limits, retention Retention, store SnapshotStore) (*Backend, error) {
	if limits.Metadata < 0 || limits.Events < 0 {
		return nil, errors.New("negative attribution capacity")
	}
	b := &Backend{core: New(), store: store, snapshot: backendSnapshot{Version: 2, Limits: limits, Retention: retention}}
	if store == nil {
		return b, nil
	}
	data, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	if data == nil {
		if err := b.commit(b.snapshot); err != nil {
			return nil, err
		}
		return b, nil
	}
	s, err := decodeSnapshot(data, retention)
	if err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	if s.Version != 2 || s.Limits != limits || s.Retention != retention || len(s.Metadata) > limits.Metadata || len(s.Events) > limits.Events {
		return nil, errors.New("snapshot version, limits, or entry count mismatch")
	}
	if err := validateSnapshot(s); err != nil {
		return nil, err
	}
	b.snapshot, b.core = s, coreFromSnapshot(s)
	return b, nil
}

func validKey(key ProcessKey) bool {
	return key.Scope != "" && utf8.ValidString(key.Scope) && key.PID > 0 && key.Incarnation > 0
}

// JSON replaces invalid UTF-8. Reject it instead of changing accepted records
// during recovery (or making distinct byte-string identities collide).
func validMetadata(m Metadata) bool {
	return validKey(m.Key) && utf8.ValidString(m.Executable) && utf8.ValidString(m.Workload)
}

func validActivity(e Activity) bool {
	return e.EventID != "" && validKey(e.Key) && utf8.ValidString(e.EventID) && utf8.ValidString(e.Kind)
}

// commit saves before publishing in memory. Any storage error stops all further
// operations on this instance: reopening determines which snapshot survived.
func (b *Backend) commit(next backendSnapshot) error {
	if b.store != nil {
		data, err := json.Marshal(next)
		if err == nil {
			err = b.store.Save(data)
		}
		if err != nil {
			b.failed = fmt.Errorf("%w: %w", ErrStorage, err)
			return b.failed
		}
	}
	b.snapshot = next
	b.core = coreFromSnapshot(next)
	return nil
}

func (b *Backend) DeliverMetadata(m Metadata) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return b.failed
	}
	if old, exists := b.core.metadata[m.Key]; exists {
		if old != m {
			return ErrConflict
		}
		return nil
	}
	if !validMetadata(m) {
		return errors.New("invalid metadata identity")
	}
	if len(b.snapshot.Metadata) >= b.snapshot.Limits.Metadata {
		return ErrCapacity
	}
	next := b.snapshot
	next.Metadata = append(slices.Clone(next.Metadata), cachedMetadata{Metadata: m, AcceptedAt: next.Now})
	candidate := coreFromSnapshot(b.snapshot)
	candidate.DeliverMetadata(m)
	next.Events = slices.Clone(next.Events)
	for i := range next.Events {
		next.Events[i].Result, _ = candidate.Result(next.Events[i].Activity.EventID)
	}
	return b.commit(next)
}

func (b *Backend) DeliverActivity(e Activity) (Attribution, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return Attribution{}, b.failed
	}
	if result, exists := b.core.Result(e.EventID); exists {
		return result, nil
	}
	if !validActivity(e) {
		return Attribution{}, errors.New("invalid activity identity")
	}
	if len(b.snapshot.Events) >= b.snapshot.Limits.Events {
		return Attribution{}, ErrCapacity
	}
	next := b.snapshot
	candidate := coreFromSnapshot(b.snapshot)
	result := candidate.DeliverActivity(e)
	next.Events = append(slices.Clone(next.Events), retainedEvent{Activity: e, Result: result, AcceptedAt: next.Now})
	if err := b.commit(next); err != nil {
		return Attribution{}, err
	}
	return result, nil
}

// DeliverExit does not affect retention deadlines or consume capacity.
func (b *Backend) DeliverExit(key ProcessKey) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return b.failed
	}
	b.core.DeliverExit(key)
	return nil
}

func (b *Backend) Result(eventID string) (Attribution, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return Attribution{}, false, b.failed
	}
	result, exists := b.core.Result(eventID)
	return result, exists, nil
}
