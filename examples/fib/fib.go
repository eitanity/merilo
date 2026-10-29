// Package fib computes Fibonacci numbers by tail recursion, as a state
// machine:
//
//	fib(n)       = fib(n, 0, 1)
//	fib(n, a, b) = a                    if n == 0
//	fib(n, a, b) = fib(n-1, b, a+b)     otherwise
//
// The machine is the call fib(n, a, b): it holds all three arguments, and
// each EvCall evaluates it once. A tail call replaces the arguments, so
// the machine is the next call and nothing is left to return to. The
// holder's loop is the trampoline: it fires until the call has returned.
//
// a and b are big integers of up to Bits bits. A big.Int holds a slice,
// so a machine holding one could not be compared with ==; the machine
// holds each number as Words 64-bit words instead, and its accessors
// return them as big.Int values.
package fib

import (
	"encoding/binary"
	"math/big"
	"math/bits"

	"code.eitanity.com/merilo/fsm"
)

// State is the call's control state.
type State uint8

// State values.
const (
	stateNone State = iota
	Calling         // fib(n, a, b) is being evaluated
	Returned        // it returned a
	TooLarge        // a+b would not fit in Bits bits
	BadInput        // n was negative
	numStates
)

// Event is an input to Fire.
type Event uint8

// Event values.
const (
	eventNone Event = iota
	EvCall          // evaluate fib(n, a, b) once
	numEvents
)

// The size of the numbers the call holds.
const (
	Words = 16         // 64-bit words in a number
	Bits  = Words * 64 // bits in a number
)

// number is an unsigned integer of Words words, the lowest first.
type number [Words]uint64

// invalidName is what String returns for a value outside the declared constants.
const invalidName = "invalid"

// Machine is the call fib(n, a, b). Build it with New and change it only
// with Fire.
type Machine struct {
	state State
	n     int64
	a, b  number
}

// New returns the call fib(n, 0, 1), or one in BadInput when n is negative.
func New(n int64) Machine {
	if n < 0 {
		return Machine{state: BadInput}
	}
	return Machine{state: Calling, n: n, b: number{1}}
}

// State returns the control state.
func (m Machine) State() State { return m.state }

// Args returns the arguments of the call.
func (m Machine) Args() (n int64, a, b *big.Int) { return m.n, toBig(m.a), toBig(m.b) }

// Result returns fib(n) once the call has returned.
func (m Machine) Result() (*big.Int, bool) { return toBig(m.a), m.state == Returned }

// toBig returns x as a big.Int.
func toBig(x number) *big.Int {
	var buf [Words * 8]byte
	for i, w := range x {
		binary.BigEndian.PutUint64(buf[(Words-1-i)*8:], w)
	}
	return new(big.Int).SetBytes(buf[:])
}

// add returns x+y, and false when the sum does not fit in Bits bits.
func add(x, y number) (number, bool) {
	var sum number
	var carry uint64
	for i := range x {
		sum[i], carry = bits.Add64(x[i], y[i], carry)
	}
	return sum, carry == 0
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
	case TooLarge, BadInput:
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

// call is the definition: fib(n, a, b) is a if n == 0, and otherwise the
// tail call fib(n-1, b, a+b).
func (m Machine) call(e Event) (Machine, fsm.Outcome) {
	switch e {
	case EvCall:
		if m.n <= 0 {
			m.state = Returned
			return m, fsm.Moved
		}
		sum, ok := add(m.a, m.b)
		if !ok {
			m.state = TooLarge
			return m, fsm.Moved
		}
		m.n, m.a, m.b = m.n-1, m.b, sum
		return m, fsm.Moved
	default:
		return m, fsm.Invalid
	}
}

func (s State) String() string {
	switch s {
	case Calling:
		return "Calling"
	case Returned:
		return "Returned"
	case TooLarge:
		return "TooLarge"
	case BadInput:
		return "BadInput"
	default:
		return invalidName
	}
}
