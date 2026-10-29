package order

import (
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/fsm"
)

// table lists the machines and events Total checks. Position 0 of each
// is the zero value, which must be refused as Invalid.
type table struct {
	machines []Machine
	events   []Event
}

func newTable() table {
	t := table{machines: []Machine{{}}, events: []Event{eventNone}}
	for s := Idle; s < numStates; s++ {
		t.machines = append(t.machines, Machine{state: s, retries: fsm.NewCounter(MaxRetries)})
	}
	full := fsm.NewCounter(MaxRetries)
	for range MaxRetries {
		full, _ = full.Inc()
	}
	t.machines = append(t.machines, Machine{state: Pending, retries: full})
	for e := EvSubmit; e < numEvents; e++ {
		t.events = append(t.events, e)
	}
	return t
}

func (t table) Machines() int { return len(t.machines) }
func (t table) Events() int   { return len(t.events) }

func (t table) Fire(m, e int) (fsm.Outcome, bool) {
	before := t.machines[m]
	next, acts, out := before.Fire(t.events[e])
	return out, next == before && acts == 0
}

func (t table) Name(m, e int) string {
	return fmt.Sprintf("state=%d retries=%d / event=%d", t.machines[m].state, t.machines[m].retries.Value(), t.events[e])
}

func TestTotal(t *testing.T) {
	merilo.Total(t, newTable())
}
