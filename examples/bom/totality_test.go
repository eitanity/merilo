package bom

import (
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/fsm"
)

// chain is a bill of three parts, each containing the next.
var chain = []Part{
	{Value: 5, Lines: []Line{{Part: 1, Quantity: 2}}},
	{Value: 7, Lines: []Line{{Part: 2, Quantity: 3}}},
	{Value: 11},
}

// after fires n calls at m.
func after(m Machine, n int) Machine {
	for range n {
		m, _ = m.Fire(EvCall)
	}
	return m
}

// table lists the machines and events Total checks. Position 0 of each is
// the zero value, which must be refused as Invalid.
type table struct {
	machines []Machine
	events   []Event
}

func newTable() table {
	start := New(chain, 0, 10)
	looped := New([]Part{{Lines: []Line{{Part: 1, Quantity: 1}}}, {Lines: []Line{{Part: 0, Quantity: 1}}}}, 0, 10)
	return table{
		machines: []Machine{
			{},
			start,
			after(start, 3),  // coming back to the leaf
			after(start, 5),  // coming back to the top
			after(looped, 2), // about to go down to a part on its own path
			New(chain, 0, 1), // one call allowed
			after(New([]Part{{Value: maxValue, Lines: []Line{{Part: 1, Quantity: 2}}}, {Value: maxValue}}, 0, 10), 3), // its subtotal overflows next
			after(start, 7),             // returned
			{state: Calling, nparts: 1}, // an empty stack
			{state: Cyclic},
			{state: TooLarge},
			New(nil, 0, 10),
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
	return fmt.Sprintf("%v size=%d calls=%d / event %d", g.state, g.size, g.calls.Value(), t.events[e])
}

func TestTotal(t *testing.T) { merilo.Total(t, newTable()) }

// TestInvalidEvent: an event outside the declared ones is refused as
// Invalid.
func TestInvalidEvent(t *testing.T) {
	m := New(chain, 0, 10)
	if next, out := m.Fire(numEvents); out != fsm.Invalid || next != m {
		t.Errorf("event %d gave %v, %v; want Invalid and no change", numEvents, out, next.State())
	}
}

// TestBadInput: New refuses every bill and limit it does not accept.
func TestBadInput(t *testing.T) {
	big := make([]Part, 0, MaxParts+1)
	for range MaxParts + 1 {
		big = append(big, Part{})
	}
	cases := map[string]Machine{
		"no parts":          New(nil, 0, 10),
		"too many parts":    New(big, 0, 10),
		"top out of range":  New(chain, 3, 10),
		"no calls":          New(chain, 0, 0),
		"negative value":    New([]Part{{Value: -1}}, 0, 10),
		"too many lines":    New([]Part{{Lines: make([]Line, MaxLines+1)}}, 0, 10),
		"line out of range": New([]Part{{Lines: []Line{{Part: 1, Quantity: 1}}}}, 0, 10),
		"zero quantity":     New([]Part{{Lines: []Line{{Part: 0, Quantity: 0}}}}, 0, 10),
	}
	for name, m := range cases {
		if m.State() != BadInput {
			t.Errorf("%s: %v, want BadInput", name, m.State())
		}
	}
}

// TestWidestBill: a bill at MaxParts and MaxLines, each part holding the
// next MaxLines times, fills the stack as far as an acyclic bill can and
// never past MaxStack.
func TestWidestBill(t *testing.T) {
	parts := make([]Part, 0, MaxParts)
	for i := range int64(MaxParts) {
		var p Part
		for range MaxLines {
			if i+1 < MaxParts {
				p.Lines = append(p.Lines, Line{Part: i + 1, Quantity: 1})
			}
		}
		parts = append(parts, p)
	}
	m := New(parts, 0, 10000)
	deepest := int64(0)
	for m.State() == Calling {
		var out fsm.Outcome
		if m, out = m.Fire(EvCall); out != fsm.Moved {
			t.Fatalf("call %d gave %v", m.Calls(), out)
		}
		deepest = max(deepest, m.Size())
	}
	want := int64((MaxParts-1)*MaxLines + 1)
	if m.State() != TooLarge || deepest != want {
		t.Errorf("ended %v with the stack at most %d deep; want TooLarge from the call limit, at most %d deep", m.State(), deepest, want)
	}
}
