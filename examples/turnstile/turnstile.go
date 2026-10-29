// Package turnstile is the smallest machine merilo checks: one mode, no
// actions, so Fire returns only the next machine and the outcome.
package turnstile

import "code.eitanity.com/merilo/fsm"

// State is the turnstile's control state.
type State uint8

// State values. The zero value is an unexported sentinel.
const (
	stateNone State = iota
	Locked
	Unlocked
	numStates
)

// Event is an input to Fire.
type Event uint8

// Event values.
const (
	eventNone Event = iota
	EvCoin
	EvPush
	numEvents
)

// Machine is one turnstile. Build it with New and change it only with Fire.
type Machine struct {
	state State
}

// New returns a locked turnstile.
func New() Machine { return Machine{state: Locked} }

// State returns the control state.
func (m Machine) State() State { return m.state }

// Fire applies e: a coin unlocks, a push through an unlocked turnstile
// locks it again, and a push on a locked one is rejected.
func (m Machine) Fire(e Event) (Machine, fsm.Outcome) {
	switch m.state {
	case Locked:
		switch e {
		case EvCoin:
			m.state = Unlocked
			return m, fsm.Moved
		case EvPush:
			return m, fsm.Rejected
		default:
			return m, fsm.Invalid
		}
	case Unlocked:
		switch e {
		case EvCoin:
			return m, fsm.Ignored
		case EvPush:
			m.state = Locked
			return m, fsm.Moved
		default:
			return m, fsm.Invalid
		}
	default:
		return m, fsm.Invalid
	}
}

func (s State) String() string {
	switch s {
	case Locked:
		return "Locked"
	case Unlocked:
		return "Unlocked"
	default:
		return "invalid"
	}
}
