// Package rollup is the holder for package bom. It turns a named bill of
// materials into the call the machine is, and its loop is the trampoline
// that evaluates the call until it returns. The walk, the stack and the
// numbers are the machine's; nothing here recurses.
package rollup

import (
	"fmt"

	"code.eitanity.com/merilo/examples/bom"
	"code.eitanity.com/merilo/fsm"
)

// Line is a quantity of one part in another.
type Line struct {
	Part     int   // the part, an index into Bill.Parts
	Quantity int64 // how many, at least one
}

// Part is a part or an assembly. Its value is its own cost, such as the
// labour to assemble it, in the smallest unit of currency.
type Part struct {
	Name  string
	Value int64 // at least zero
	Lines []Line
}

// Bill is a bill of materials: every part, and the one the system is.
type Bill struct {
	Parts []Part
	Top   int // the system, an index into Parts
}

// Result is a finished rollup.
type Result struct {
	// Subtotal is, for each part, the cost of one: its value plus each
	// line's quantity times that part's subtotal. It is zero for a part
	// the system does not use.
	Subtotal []int64
	// Need is, for each part, how many one system takes in all: the
	// quantities multiplied down every path to it and summed. The system
	// itself needs one.
	Need []int64
	// Total is the cost of one system, its subtotal.
	Total int64
}

// Refused is the error for a walk the machine stopped.
type Refused struct{ State bom.State }

func (r Refused) Error() string {
	switch r.State {
	case bom.Cyclic:
		return "a part contains itself"
	case bom.TooLarge:
		return "the bill's numbers do not fit in 64 bits, or it needs more calls than its limit"
	case bom.BadInput:
		return "the bill or the limit is out of range"
	case bom.Calling, bom.Returned:
		return fmt.Sprintf("the walk stopped in %v", r.State)
	default:
		return fmt.Sprintf("the walk stopped in %v", r.State)
	}
}

// Walker is one rollup in progress: the call, the bill's size and its top
// part.
type Walker struct {
	m   bom.Machine
	n   int
	top int64
}

// Run rolls up b, making at most calls tail calls.
func Run(b Bill, calls int64) (Result, error) {
	w, err := New(b, calls)
	if err != nil {
		return Result{}, err
	}
	for w.m.State() == bom.Calling {
		if err := w.Step(); err != nil {
			return Result{}, err
		}
	}
	return w.Result()
}

// New returns the call on b, or an error when b or calls is out of range.
func New(b Bill, calls int64) (*Walker, error) {
	parts := make([]bom.Part, 0, len(b.Parts))
	for _, p := range b.Parts {
		lines := make([]bom.Line, 0, len(p.Lines))
		for _, ln := range p.Lines {
			lines = append(lines, bom.Line{Part: int64(ln.Part), Quantity: ln.Quantity})
		}
		parts = append(parts, bom.Part{Value: p.Value, Lines: lines})
	}
	w := &Walker{m: bom.New(parts, int64(b.Top), calls), n: len(b.Parts), top: int64(b.Top)}
	if s := w.m.State(); s != bom.Calling {
		return nil, Refused{State: s}
	}
	return w, nil
}

// Step evaluates the call once. Once the walk has returned, it does
// nothing; a refused walk is an error.
func (w *Walker) Step() error {
	var out fsm.Outcome
	w.m, out = w.m.Fire(bom.EvCall)
	if err := out.Err(); err != nil {
		return fmt.Errorf("call in %v: %w", w.m.State(), err)
	}
	switch s := w.m.State(); s {
	case bom.Calling, bom.Returned:
		return nil
	case bom.Cyclic, bom.TooLarge, bom.BadInput:
		return Refused{State: s}
	default:
		return Refused{State: s}
	}
}

// Result returns the rollup once the walk has returned.
func (w *Walker) Result() (Result, error) {
	if s := w.m.State(); s != bom.Returned {
		return Result{}, Refused{State: s}
	}
	r := Result{Subtotal: make([]int64, w.n), Need: make([]int64, w.n)}
	for p := range w.n {
		r.Need[p], _ = w.m.Need(int64(p))
		r.Subtotal[p], _ = w.m.Subtotal(int64(p))
	}
	r.Total, _ = w.m.Subtotal(w.top)
	return r, nil
}
