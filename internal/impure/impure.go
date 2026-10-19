// Package impure is an acceptance-test fixture (AC-15): a machine whose
// Fire consults a helper package's hidden state, so replaying the same
// path can reach a different world.
package impure

import "github.com/eitanity/merilo/internal/impure/clock"

// State is the control state.
type State uint8

const (
	stateNone State = iota
	Even
	Odd
)

// Event is an input to Fire.
type Event uint8

const (
	eventNone Event = iota
	EvStep
)

// Mode is derived from State.
type Mode uint8

const (
	modeNone Mode = iota
	Normal
)

// Outcome reports what Fire did.
type Outcome uint8

const (
	outcomeNone Outcome = iota
	Moved
)

// Actions is always empty.
type Actions struct{}

// Machine flips between Even and Odd by the clock, not by its own state.
type Machine struct{ state State }

// New returns a machine in Even.
func New() Machine { return Machine{state: Even} }

// State returns the control state.
func (m Machine) State() State { return m.state }

// Mode is always Normal.
func (m Machine) Mode() Mode {
	switch m.state {
	case Even, Odd:
		return Normal
	default:
		panic("impure: invalid state")
	}
}

// AppendKey appends every field.
func (m Machine) AppendKey(b []byte) []byte { return append(b, byte(m.state)) }

// Fire reads the clock: the impurity under test.
func (m Machine) Fire(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvStep:
		if clock.Now()%2 == 0 {
			m.state = Even
		} else {
			m.state = Odd
		}
		return m, Actions{}, Moved
	default:
		panic("impure: invalid event")
	}
}
