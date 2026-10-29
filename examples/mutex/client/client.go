// Package client is one client of a lock server: it asks for the lock,
// holds it once granted, and releases it. New can also build a client that
// is not idempotent under duplicated grants, for the acceptance tests.
package client

import "code.eitanity.com/merilo/fsm"

// State is the client's control state.
type State uint8

// State values.
const (
	stateNone State = iota
	Idle
	Waiting
	Holding
	numStates
)

// Event is an input to Fire.
type Event uint8

// Event values.
const (
	eventNone Event = iota
	EvWant
	EvGrant
	EvDone
	numEvents
)

// Mode is derived from State. A client has one mode.
type Mode uint8

// Mode values.
const (
	modeNone Mode = iota
	Normal
	numModes
)

// Actions is the set of things the holder must do after a transition, one
// bit each. The holder performs them in declaration order. The zero value
// is the empty set.
type Actions uint64

// Actions values.
const (
	ActRequest Actions = 1 << iota
	ActRelease
	ActModeChanged
)

// Machine is one client. Build it with New and change it only with Fire.
type Machine struct {
	state       State
	acceptStale bool
}

// New returns a client in Idle. With acceptStale it is the seeded bug: it
// takes any grant, even when not waiting, so a duplicated grant makes it
// hold the lock again.
func New(acceptStale bool) Machine { return Machine{state: Idle, acceptStale: acceptStale} }

// State returns the control state.
func (m Machine) State() State { return m.state }

// Mode derives the mode from the state.
func (m Machine) Mode() Mode {
	switch m.state {
	case Idle, Waiting, Holding:
		return Normal
	default:
		return modeNone
	}
}

// Fire applies e and returns the next machine, its actions and what happened.
func (m Machine) Fire(e Event) (Machine, Actions, fsm.Outcome) {
	switch m.state {
	case Idle:
		return m.fireIdle(e)
	case Waiting:
		return m.fireWaiting(e)
	case Holding:
		return m.fireHolding(e)
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireIdle(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvWant:
		m.state = Waiting
		return m, ActRequest, fsm.Moved
	case EvGrant:
		if m.acceptStale {
			m.state = Holding
			return m, 0, fsm.Moved
		}
		return m, 0, fsm.Ignored
	case EvDone:
		return m, 0, fsm.Rejected
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireWaiting(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvWant, EvDone:
		return m, 0, fsm.Rejected
	case EvGrant:
		m.state = Holding
		return m, 0, fsm.Moved
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireHolding(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvWant:
		return m, 0, fsm.Rejected
	case EvGrant:
		return m, 0, fsm.Ignored
	case EvDone:
		m.state = Idle
		return m, ActRelease, fsm.Moved
	default:
		return m, 0, fsm.Invalid
	}
}

func (s State) String() string {
	switch s {
	case Idle:
		return "Idle"
	case Waiting:
		return "Waiting"
	case Holding:
		return "Holding"
	default:
		return "invalid"
	}
}

func (m Mode) String() string {
	switch m {
	case Normal:
		return "Normal"
	default:
		return "invalid"
	}
}
