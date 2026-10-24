package simulation

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

var ErrBootChanged = errors.New("boot changed during capture; observation discarded")

type BootObservation struct {
	ID      string
	Changed bool // Compared with this detector's last successful observation.
}

// BootDetector reads on every observation; its previous ID is used only to
// report changes, never as a fallback identity. The provider must return a fresh,
// nonrepeating boot identity for the same host as the capture source.
type BootDetector struct {
	mu   sync.Mutex
	read func() (string, error)
	last string
}

func NewBootDetector(read func() (string, error)) (*BootDetector, error) {
	if read == nil {
		return nil, errors.New("boot reader is required")
	}
	return &BootDetector{read: read}, nil
}

func (b *BootDetector) Observe() (BootObservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, err := b.read()
	if err != nil {
		return BootObservation{}, err
	}
	if id == "" || !utf8.ValidString(id) {
		return BootObservation{}, errors.New("invalid boot identity")
	}
	observation := BootObservation{ID: id, Changed: b.last != "" && id != b.last}
	b.last = id
	return observation, nil
}

// ReadLinuxBootID reads the local kernel's boot UUID. It returns an error when
// procfs is unavailable; it never invents or reuses a cached boot identity.
func ReadLinuxBootID() (string, error) {
	return ReadBootIDFile("/proc/sys/kernel/random/boot_id")
}

// ReadBootIDFile supports an explicitly mounted procfs and filesystem fixtures.
func ReadBootIDFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.ToLower(strings.TrimSpace(string(data)))
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return "", errors.New("invalid boot UUID")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(decoded) != 16 {
		return "", errors.New("invalid boot UUID")
	}
	return id, nil
}

// ProcessSnapshot must describe the process at the instant the source captures
// the event. Incarnation must be unique within this host/namespace/boot boundary,
// including across source restarts; a PID or an unproven timestamp is insufficient.
type ProcessSnapshot struct {
	PID         int
	Incarnation uint64
	Executable  string
	Workload    string
}

// CaptureSource captures an event and its process snapshot coherently. It is NOT
// a later PID lookup for an event that already happened. Implementations own
// this atomicity/identity contract; Sensor cannot reconstruct lost provenance.
type CaptureSource interface {
	Capture(pid int, kind string) (ProcessSnapshot, error)
}

// Sensor turns source snapshots into immutable delivery values. It serializes
// source calls and checks boot identity on either side of capture. No background
// goroutine, event queue, poller, or kernel event collector is started.
type Sensor struct {
	mu       sync.Mutex
	boundary string
	boots    *BootDetector
	source   CaptureSource
}

// boundary must distinguish the tenant, host, and PID-namespace lifetime.
func NewSensor(boundary string, boots *BootDetector, source CaptureSource) (*Sensor, error) {
	if boundary == "" || !utf8.ValidString(boundary) || boots == nil || source == nil {
		return nil, errors.New("sensor requires a boundary, boot detector, and capture source")
	}
	return &Sensor{boundary: boundary, boots: boots, source: source}, nil
}

// Capture requires a globally unique event ID. A boot change or source failure
// returns no deliverable activity or metadata. Successful values may be queued
// and delivered independently; future observations cannot alter their identity.
func (s *Sensor) Capture(eventID string, pid int, kind string) (Activity, Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if eventID == "" || !utf8.ValidString(eventID) || !utf8.ValidString(kind) || pid <= 0 {
		return Activity{}, Metadata{}, errors.New("invalid capture request")
	}
	before, err := s.boots.Observe()
	if err != nil {
		return Activity{}, Metadata{}, err
	}
	snapshot, err := s.source.Capture(pid, kind)
	if err != nil {
		return Activity{}, Metadata{}, err
	}
	after, err := s.boots.Observe()
	if err != nil {
		return Activity{}, Metadata{}, err
	}
	if before.ID != after.ID {
		return Activity{}, Metadata{}, ErrBootChanged
	}
	if snapshot.PID != pid || snapshot.Incarnation == 0 {
		return Activity{}, Metadata{}, fmt.Errorf("source returned an invalid process identity for PID %d", pid)
	}
	// An encoded tuple avoids ambiguous concatenations of caller-controlled IDs.
	scope, _ := json.Marshal([]string{s.boundary, before.ID})
	key := ProcessKey{Scope: string(scope), PID: snapshot.PID, Incarnation: snapshot.Incarnation}
	metadata := Metadata{Key: key, Executable: snapshot.Executable, Workload: snapshot.Workload}
	if !validMetadata(metadata) {
		return Activity{}, Metadata{}, errors.New("source returned invalid metadata")
	}
	return Activity{EventID: eventID, Key: key, Kind: kind}, metadata, nil
}
