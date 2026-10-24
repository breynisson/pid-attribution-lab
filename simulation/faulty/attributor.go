// Package faulty deliberately demonstrates incorrect attribution after PID reuse.
// It is a learning example, not a correct implementation.
package faulty

import simulation "example.com/pid-attribution-lab"

type Attributor struct {
	current map[int]simulation.Metadata
}

func New() *Attributor {
	return &Attributor{current: make(map[int]simulation.Metadata)}
}

func (a *Attributor) DeliverMetadata(metadata simulation.Metadata) {
	a.current[metadata.Key.PID] = metadata
}

func (a *Attributor) DeliverExit(key simulation.ProcessKey) {
	delete(a.current, key.PID)
}

func (a *Attributor) DeliverActivity(event simulation.Activity) simulation.Attribution {
	result := simulation.Attribution{EventID: event.EventID}
	// BUG: the current PID holder need not be the event's capture-time origin.
	metadata, ok := a.current[event.Key.PID]
	if ok {
		result.Resolved = true
		result.Key = metadata.Key
		result.Workload = metadata.Workload
	}
	return result
}
