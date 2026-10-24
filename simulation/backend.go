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
	ErrCapacity = errors.New("attribution capacity exhausted")
	ErrConflict = errors.New("metadata conflicts with retained identity")
	ErrStorage  = errors.New("storage outcome uncertain; reopen backend before continuing")
)

// Limits bound retained entries, including pending events, not bytes or queues.
// Zero rejects new entries. Accepted entries are never evicted.
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
	Version  int
	Limits   Limits
	Metadata []Metadata
	Events   []Activity
}

// Backend adds bounded admission, whole-call serialization, and optional durable
// snapshots to Attributor. Use OpenBackend; do not copy a Backend. Only one
// Backend may own a SnapshotStore at a time, including across processes.
// Metadata is immutable; conflicting deliveries are rejected. Event IDs retain
// first-delivery-wins semantics. Neither rule authenticates the first input.
type Backend struct {
	mu       sync.Mutex
	core     *Attributor
	snapshot backendSnapshot
	store    SnapshotStore
	failed   error
}

// OpenBackend restores accepted inputs and reconstructs their pending/results
// indexes. A nil store is volatile. Reopening requires the same limits.
func OpenBackend(limits Limits, store SnapshotStore) (*Backend, error) {
	if limits.Metadata < 0 || limits.Events < 0 {
		return nil, errors.New("negative attribution capacity")
	}
	b := &Backend{core: New(), store: store, snapshot: backendSnapshot{Version: 1, Limits: limits}}
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
	var restored backendSnapshot
	if err := json.Unmarshal(data, &restored); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	b.snapshot = restored
	s := restored
	if s.Version != 1 || s.Limits != limits || len(s.Metadata) > limits.Metadata || len(s.Events) > limits.Events {
		return nil, errors.New("snapshot version, limits, or entry count mismatch")
	}
	for _, m := range s.Metadata {
		if !validMetadata(m) {
			return nil, errors.New("invalid metadata identity in snapshot")
		}
		if _, exists := b.core.metadata[m.Key]; exists {
			return nil, errors.New("duplicate metadata identity in snapshot")
		}
		b.core.DeliverMetadata(m)
	}
	for _, e := range s.Events {
		if !validActivity(e) {
			return nil, errors.New("invalid activity in snapshot")
		}
		if _, exists := b.core.Result(e.EventID); exists {
			return nil, errors.New("duplicate event ID in snapshot")
		}
		b.core.DeliverActivity(e)
	}
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
	next.Metadata = append(slices.Clone(next.Metadata), m)
	if err := b.commit(next); err != nil {
		return err
	}
	b.core.DeliverMetadata(m)
	return nil
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
	next.Events = append(slices.Clone(next.Events), e)
	if err := b.commit(next); err != nil {
		return Attribution{}, err
	}
	return b.core.DeliverActivity(e), nil
}

// DeliverExit retains history and does not consume capacity.
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
