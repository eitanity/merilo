package fib

import (
	"fmt"
	"math"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/fsm"
)

// table lists the machines and events Total checks. Position 0 of each is
// the zero value, which must be refused as Invalid.
type table struct {
	machines []Machine
	events   []Event
}

func newTable() table {
	var full number
	for i := range full {
		full[i] = math.MaxUint64
	}
	return table{
		machines: []Machine{
			{},
			New(0),
			New(1),
			New(math.MaxInt64),
			{state: Calling, n: 2, a: full, b: number{1}},                   // a+b overflows
			{state: Calling, n: 2, a: number{math.MaxUint64}, b: number{1}}, // a carry into the next word
			{state: Returned, a: number{5}},
			{state: TooLarge},
			{state: BadInput},
		},
		events: []Event{eventNone, EvCall},
	}
}

func (t table) Machines() int { return len(t.machines) }
func (t table) Events() int   { return len(t.events) }

func (t table) Fire(m, e int) (fsm.Outcome, bool) {
	before := t.machines[m]
	next, out := before.Fire(t.events[e])
	return out, next == before
}

func (t table) Name(m, e int) string {
	g := t.machines[m]
	n, a, b := g.Args()
	return fmt.Sprintf("%v fib(%d, %v, %v) / event %d", g.state, n, a, b, t.events[e])
}

func TestTotal(t *testing.T) { merilo.Total(t, newTable()) }
