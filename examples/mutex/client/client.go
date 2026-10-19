// Package client is one client of a lock server: it asks for the lock,
// holds it once granted, and releases it. New can also build a client that
// is not idempotent under duplicated grants, for the acceptance tests.
package client

// State is the client's control state.
type State uint8

const (
	stateNone State = iota
	Idle
	Waiting
	Holding
	numStates
)

// Event is an input to Fire.
type Event uint8

const (
	eventNone Event = iota
	EvWant
	EvGrant
	EvDone
	numEvents
)

// Mode is derived from State. A client has one mode.
type Mode uint8

const (
	modeNone Mode = iota
	Normal
	numModes
)

// Action is something the holder must do after a transition.
type Action uint8

const (
	ActNone Action = iota
	ActRequest
	ActRelease
	ActModeChanged
)

// Outcome reports what Fire did with an event.
type Outcome uint8

const (
	outcomeNone Outcome = iota
	Moved
	Ignored
	Rejected
)

const maxActions = 2

// Actions is a fixed-size list of actions.
type Actions struct {
	list [maxActions]Action
	n    uint8
}

// Len is the number of actions.
func (a Actions) Len() int { return int(a.n) }

// At returns action i, which must be below Len.
func (a Actions) At(i int) Action {
	if i < 0 || i >= int(a.n) {
		panic("client: action index out of range")
	}
	return a.list[i]
}

func one(x Action) Actions {
	return Actions{list: [maxActions]Action{x}, n: 1}
}

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
		panic("client: invalid state; machine not built with New")
	}
}

// AppendKey appends every field, in declaration order.
func (m Machine) AppendKey(b []byte) []byte {
	stale := byte(0)
	if m.acceptStale {
		stale = 1
	}
	return append(b, byte(m.state), stale)
}

// Fire applies e and returns the next machine, its actions and what happened.
func (m Machine) Fire(e Event) (Machine, Actions, Outcome) {
	switch m.state {
	case Idle:
		return m.fireIdle(e)
	case Waiting:
		return m.fireWaiting(e)
	case Holding:
		return m.fireHolding(e)
	default:
		panic("client: invalid state; machine not built with New")
	}
}

func (m Machine) fireIdle(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvWant:
		m.state = Waiting
		return m, one(ActRequest), Moved
	case EvGrant:
		if m.acceptStale {
			m.state = Holding
			return m, Actions{}, Moved
		}
		return m, Actions{}, Ignored
	case EvDone:
		return m, Actions{}, Rejected
	default:
		panic("client: undeclared event in Idle")
	}
}

func (m Machine) fireWaiting(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvWant, EvDone:
		return m, Actions{}, Rejected
	case EvGrant:
		m.state = Holding
		return m, Actions{}, Moved
	default:
		panic("client: undeclared event in Waiting")
	}
}

func (m Machine) fireHolding(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvWant:
		return m, Actions{}, Rejected
	case EvGrant:
		return m, Actions{}, Ignored
	case EvDone:
		m.state = Idle
		return m, one(ActRelease), Moved
	default:
		panic("client: undeclared event in Holding")
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
		panic("client: invalid state")
	}
}

func (m Mode) String() string {
	switch m {
	case Normal:
		return "Normal"
	default:
		panic("client: invalid mode")
	}
}
