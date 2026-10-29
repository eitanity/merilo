package watchdog

import (
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/fsm"
)

// table checks every state at generations 0 to 2 against every event kind
// with generations 0 to 2, so stale and current timeouts both meet each
// state.
type table struct {
	machines []Machine
	events   []Event
}

func newTable() table {
	t := table{machines: []Machine{{}}, events: []Event{{}}}
	// Generations near both ends, so the exhausted generation meets
	// every event too.
	gens := []uint64{0, 1, 2, MaxGen - 1, MaxGen}
	for s := Idle; s < numStates; s++ {
		for _, g := range gens {
			t.machines = append(t.machines, Machine{state: s, gen: g})
		}
	}
	for k := EvArm; k < numKinds; k++ {
		for _, g := range gens {
			t.events = append(t.events, Event{Kind: k, Gen: g})
		}
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

// TestGenerations: over the same table, no stale timeout fires the
// watchdog and no generation goes down.
func TestGenerations(t *testing.T) {
	tab := newTable()
	for _, before := range tab.machines[1:] {
		for _, e := range tab.events[1:] {
			next, _, _ := before.Fire(e)
			if e.Kind == EvTimeout && before.state == Armed && e.Gen != before.gen && next.state == Fired {
				t.Errorf("%+v / %+v: a stale timeout fired the watchdog", before, e)
			}
			if next.gen < before.gen {
				t.Errorf("%+v / %+v: the generation went down", before, e)
			}
		}
	}
}

func (t table) Name(m, e int) string { return fmt.Sprintf("%+v / %+v", t.machines[m], t.events[e]) }

func TestTotal(t *testing.T) { merilo.Total(t, newTable()) }
