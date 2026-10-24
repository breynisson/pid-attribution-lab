package simulation

// Attributor associates activities with immutable metadata for their exact
// capture-time process identity. Use New to initialize it. Calls must be serial.
// Process keys identify unique lifetimes; history is retained without expiry.
// Event IDs identify logical events: their first delivery fixes their origin.
// Resolution is synchronous once both activity and matching metadata arrive.
// Missing metadata leaves an event unresolved indefinitely; this component does
// not fetch metadata or schedule delivery. Callers own delivery progress.
type Attributor struct {
	metadata map[ProcessKey]Metadata
	pending  map[ProcessKey][]string
	results  map[string]Attribution
}

func New() *Attributor {
	return &Attributor{
		metadata: make(map[ProcessKey]Metadata),
		pending:  make(map[ProcessKey][]string),
		results:  make(map[string]Attribution),
	}
}

// DeliverMetadata resolves all received events for this exact key before returning.
func (a *Attributor) DeliverMetadata(metadata Metadata) {
	a.metadata[metadata.Key] = metadata
	for _, eventID := range a.pending[metadata.Key] {
		a.results[eventID] = Attribution{
			EventID: eventID, Resolved: true, Key: metadata.Key, Workload: metadata.Workload,
		}
	}
	delete(a.pending, metadata.Key)
}

// DeliverExit does not evict historical metadata or pending events. Attribution
// concerns the lifetime that generated an event, regardless of whether it is
// still alive. This component does not track liveness.
func (a *Attributor) DeliverExit(_ ProcessKey) {}

func (a *Attributor) DeliverActivity(event Activity) Attribution {
	// Replays return the latest result and cannot requeue or rebind an event ID.
	if result, exists := a.results[event.EventID]; exists {
		return result
	}
	result := Attribution{EventID: event.EventID}
	if metadata, ok := a.metadata[event.Key]; ok {
		result.Resolved = true
		result.Key = metadata.Key
		result.Workload = metadata.Workload
	} else {
		a.pending[event.Key] = append(a.pending[event.Key], event.EventID)
	}
	a.results[event.EventID] = result
	return result
}

// Result returns the latest attribution and whether the event has been delivered.
// An earlier DeliverActivity return value is a snapshot; query again after
// metadata delivery to observe resolution without redelivering the activity.
func (a *Attributor) Result(eventID string) (Attribution, bool) {
	result, ok := a.results[eventID]
	return result, ok
}
