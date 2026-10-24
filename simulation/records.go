package simulation

// ProcessKey identifies one lifetime. Incarnation never repeats within Scope.
// Scope is an opaque identity boundary supplied by the capture source. It must
// change across boots when incarnation counters can restart.
type ProcessKey struct {
	Scope       string
	PID         int
	Incarnation uint64
}

// Metadata is immutable for the lifetime of a process.
type Metadata struct {
	Key        ProcessKey
	Executable string
	Workload   string
}

type Activity struct {
	EventID string     // Globally identifies a logical event; replays keep the first origin.
	Key     ProcessKey // Captured when the activity occurs, before delivery.
	Kind    string
}

// When Resolved is false, Key and Workload do not identify an origin.
type Attribution struct {
	EventID  string
	Resolved bool
	Key      ProcessKey
	Workload string
}
