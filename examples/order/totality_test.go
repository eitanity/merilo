package order

import (
	"fmt"
	"testing"

	"github.com/eitanity/merilo"
)

// table lists the machines and events Total checks. Position 0 of each
// is the zero value, which must panic.
type table struct {
	machines []Machine
	events   []Event
}

func newTable() table {
	t := table{machines: []Machine{{}}, events: []Event{eventNone}}
	for s := Idle; s < numStates; s++ {
		t.machines = append(t.machines, Machine{state: s})
	}
	t.machines = append(t.machines, Machine{state: Pending, retries: MaxRetries})
	for e := EvSubmit; e < numEvents; e++ {
		t.events = append(t.events, e)
	}
	return t
}

func (t table) Machines() int { return len(t.machines) }
func (t table) Events() int   { return len(t.events) }

func (t table) Fire(m, e int) (quiet, unchanged bool) {
	before := t.machines[m]
	next, acts, out := before.Fire(t.events[e])
	if out == outcomeNone {
		panic("Fire returned no outcome")
	}
	return out == Ignored || out == Rejected, next == before && acts.Len() == 0
}

func (t table) Name(m, e int) string {
	return fmt.Sprintf("state=%d retries=%d / event=%d", t.machines[m].state, t.machines[m].retries, t.events[e])
}

func TestTotal(t *testing.T) {
	merilo.Total(t, newTable())
}
