// Package bom rolls up a bill of materials by tail recursion, as a state
// machine. A part has a value, its own cost, and lines, each a quantity of
// another part. The cost of one part is its value plus, for each line, the
// quantity times the cost of that part, and the number of each part a
// system needs is the quantities multiplied down every path to it, summed.
//
// That recursion is not a tail call: a part calls itself once per line.
// Passing the work still to do as an argument, a stack, makes it one:
//
//	walk(stack, need, subtotal) = need, subtotal         if stack is empty
//	walk(stack, need, subtotal) = walk(stack', need', subtotal')
//
// where stack', need' and subtotal' are what visiting the top entry makes
// of them. Going down to a part, the system needs times more of it, and
// the entry becomes one to come back to the part, with an entry for each
// line pushed above it. Coming back, every line's part has its subtotal,
// so the part's is its value plus theirs, and the entry is popped.
//
// The machine is the call: it holds the bill and all three arguments, and
// each EvCall evaluates it once. A tail call replaces the arguments, so
// the machine is the next call and nothing is left to return to. The
// holder's loop is the trampoline: it fires until the call has returned.
//
// The machine refuses a part that contains itself, numbers that would not
// fit in 64 bits, and more calls than the limit New is given.
package bom

import "code.eitanity.com/merilo/fsm"

// State is the walk's control state.
type State uint8

// State values.
const (
	stateNone State = iota
	Calling         // walk(stack, need, subtotal) is being evaluated
	Returned        // the stack is empty: the walk returned
	Cyclic          // a part is on the path to itself
	TooLarge        // a number would not fit in 64 bits, or the calls passed their limit
	BadInput        // New was given a bill or limit it does not accept
	numStates
)

// Event is an input to Fire.
type Event uint8

// Event values.
const (
	eventNone Event = iota
	EvCall          // evaluate walk(stack, need, subtotal) once
	numEvents
)

// The size of a bill the machine holds.
const (
	MaxParts = 16 // parts in a bill
	MaxLines = 8  // lines in a part
)

// MaxStack is the most entries the stack holds. Every entry on it is the
// way back to a part on the current path, or a line of one, and a part is
// on the path at most once, so an acyclic bill never needs more.
const MaxStack = MaxParts * (MaxLines + 1)

// maxValue is the largest number the walk holds.
const maxValue = int64(1<<63 - 1)

// invalidName is what String returns for a value outside the declared constants.
const invalidName = "invalid"

// Line is a quantity of one part in another.
type Line struct {
	Part     int64 // the part, an index into the bill
	Quantity int64 // how many, at least one
}

// Part is a part or an assembly. Its value is its own cost, such as the
// labour to assemble it.
type Part struct {
	Value int64 // at least zero
	Lines []Line
}

type line struct{ part, quantity int64 }

type part struct {
	value int64
	n     int64 // lines in use
	lines [MaxLines]line
}

// entry is one piece of work on the stack: to go down to a part, times
// more of it, or, with up set, to come back to it once its lines are done.
type entry struct {
	part  int64
	times int64
	up    bool
}

// Machine is the call walk(stack, need, subtotal) on one bill. Build it
// with New and change it only with Fire.
type Machine struct {
	state  State
	nparts int64
	parts  [MaxParts]part
	size   int64 // entries on the stack
	stack  [MaxStack]entry
	need   [MaxParts]int64
	sub    [MaxParts]int64
	known  [MaxParts]bool // the part's subtotal is in sub
	onPath [MaxParts]bool // the part's way back is on the stack
	calls  fsm.Counter
}

// New returns the call walk([top], 0, 0) on the bill parts, making at most
// calls tail calls. A bill of no parts or more than MaxParts, a part of
// more than MaxLines lines, a negative value, a line to a part not in the
// bill or of a quantity below one, or a limit below one gives a walk in
// BadInput.
func New(parts []Part, top, calls int64) Machine {
	n := int64(len(parts))
	if n < 1 || n > MaxParts || top < 0 || top >= n || calls < 1 {
		return Machine{state: BadInput}
	}
	var bill [MaxParts]part
	for i, p := range parts {
		k := int64(len(p.Lines))
		if p.Value < 0 || k > MaxLines {
			return Machine{state: BadInput}
		}
		bill[i] = part{value: p.Value, n: k}
		for j, ln := range p.Lines {
			if ln.Part < 0 || ln.Part >= n || ln.Quantity < 1 {
				return Machine{state: BadInput}
			}
			bill[i].lines[j] = line{part: ln.Part, quantity: ln.Quantity}
		}
	}
	return Machine{
		state: Calling, nparts: n, parts: bill,
		size: 1, stack: [MaxStack]entry{{part: top, times: 1}},
		calls: fsm.NewCounter(calls),
	}
}

// State returns the control state.
func (m Machine) State() State { return m.state }

// Size returns how many entries are on the stack.
func (m Machine) Size() int64 { return m.size }

// Calls returns how many tail calls the walk has made.
func (m Machine) Calls() int64 { return m.calls.Value() }

// Need returns how many of part p one system needs, as far as the walk has
// gone, or false when p is not in the bill.
func (m Machine) Need(p int64) (int64, bool) {
	if p < 0 || p >= m.nparts {
		return 0, false
	}
	return m.need[p], true
}

// Subtotal returns the cost of one of part p, or false when p is not in
// the bill or the walk has not yet come back to it.
func (m Machine) Subtotal(p int64) (int64, bool) {
	if p < 0 || p >= m.nparts || !m.known[p] {
		return 0, false
	}
	return m.sub[p], true
}

// Fire applies e.
func (m Machine) Fire(e Event) (Machine, fsm.Outcome) {
	switch m.state {
	case Calling:
		return m.call(e)
	case Returned:
		switch e {
		case EvCall:
			return m, fsm.Ignored
		default:
			return m, fsm.Invalid
		}
	case Cyclic, TooLarge, BadInput:
		switch e {
		case EvCall:
			return m, fsm.Rejected
		default:
			return m, fsm.Invalid
		}
	default:
		return m, fsm.Invalid
	}
}

// call is the definition: return when the stack is empty, and otherwise
// visit the top entry, which is the tail call.
func (m Machine) call(e Event) (Machine, fsm.Outcome) {
	switch e {
	case EvCall:
		if m.size == 0 {
			m.state = Returned
			return m, fsm.Moved
		}
		if m.size < 0 || m.size > MaxStack {
			return m, fsm.Invalid
		}
		top := m.stack[m.size-1]
		if top.part < 0 || top.part >= m.nparts {
			return m, fsm.Invalid
		}
		calls, ok := m.calls.Inc()
		if !ok {
			m.state = TooLarge
			return m, fsm.Moved
		}
		if top.up {
			return m.up(top.part, calls)
		}
		return m.down(top, calls)
	default:
		return m, fsm.Invalid
	}
}

// down goes down to the top entry's part: the system needs times more of
// it, the entry becomes the way back to it, and each of its lines, times
// its quantity, is pushed above, the first line on top.
func (m Machine) down(top entry, calls fsm.Counter) (Machine, fsm.Outcome) {
	p := m.parts[top.part]
	if m.onPath[top.part] {
		m.state = Cyclic
		return m, fsm.Moved
	}
	if p.n < 0 || p.n > MaxLines || m.size < 1 || m.size > MaxStack || p.n > MaxStack-m.size {
		return m, fsm.Invalid
	}
	if top.times > maxValue-m.need[top.part] {
		m.state = TooLarge
		return m, fsm.Moved
	}
	for i := int64(0); i < p.n; i++ {
		if top.times > maxValue/p.lines[i].quantity {
			m.state = TooLarge
			return m, fsm.Moved
		}
	}
	m.calls = calls
	m.need[top.part] += top.times
	m.onPath[top.part] = true
	m.stack[m.size-1] = entry{part: top.part, up: true}
	for i := p.n - 1; i >= 0; i-- {
		ln := p.lines[i]
		m.stack[m.size] = entry{part: ln.part, times: top.times * ln.quantity}
		m.size++
	}
	return m, fsm.Moved
}

// up comes back to part q: every line's part has its subtotal, so q's is
// its value plus each line's quantity times that part's subtotal. The
// entry is popped.
func (m Machine) up(q int64, calls fsm.Counter) (Machine, fsm.Outcome) {
	p := m.parts[q]
	if p.n < 0 || p.n > MaxLines || m.size < 1 || m.size > MaxStack {
		return m, fsm.Invalid
	}
	sub := p.value
	for i := int64(0); i < p.n; i++ {
		ln := p.lines[i]
		if ln.part < 0 || ln.part >= m.nparts || !m.known[ln.part] {
			return m, fsm.Invalid
		}
		c := m.sub[ln.part]
		if c > maxValue/ln.quantity || ln.quantity*c > maxValue-sub {
			m.state = TooLarge
			return m, fsm.Moved
		}
		sub += ln.quantity * c
	}
	m.calls = calls
	m.sub[q], m.known[q], m.onPath[q] = sub, true, false
	m.size--
	return m, fsm.Moved
}

func (s State) String() string {
	switch s {
	case Calling:
		return "Calling"
	case Returned:
		return "Returned"
	case Cyclic:
		return "Cyclic"
	case TooLarge:
		return "TooLarge"
	case BadInput:
		return "BadInput"
	default:
		return invalidName
	}
}
