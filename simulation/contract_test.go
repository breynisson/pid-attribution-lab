package simulation_test

import (
	"os"

	simulation "example.com/pid-attribution-lab"
	"example.com/pid-attribution-lab/faulty"
)

type attributor interface {
	DeliverMetadata(simulation.Metadata)
	DeliverExit(simulation.ProcessKey)
	DeliverActivity(simulation.Activity) simulation.Attribution
	Result(string) (simulation.Attribution, bool)
}

// All regressions and generated properties exercise the same contract. The
// faulty implementation is an explicit opt-in, intentionally failing demo.
func newAttributor() attributor {
	if os.Getenv("PID_ATTRIBUTION_FAULTY") == "1" {
		return &faultyObserver{Attributor: faulty.New(), results: make(map[string]simulation.Attribution)}
	}
	return simulation.New()
}

// Preserve the original faulty API and behavior. This adapter only records its
// returned values; it neither resolves events nor consults world/oracle state.
type faultyObserver struct {
	*faulty.Attributor
	results map[string]simulation.Attribution
}

func (a *faultyObserver) DeliverActivity(event simulation.Activity) simulation.Attribution {
	result := a.Attributor.DeliverActivity(event)
	a.results[event.EventID] = result
	return result
}

func (a *faultyObserver) Result(eventID string) (simulation.Attribution, bool) {
	result, ok := a.results[eventID]
	return result, ok
}
