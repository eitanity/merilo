// Package order is a payment-order state machine: submit a charge to a
// provider, resend on timeout up to MaxRetries, and finish as Paid, Failed
// or Cancelled.
package order

// State is an order's control state.
type State uint8

const (
	stateNone State = iota
	Idle
	Pending
	Paid
	Failed
	Cancelled
	numStates
)

// Event is an input to Fire.
type Event uint8

const (
	eventNone Event = iota
	EvSubmit
	EvAck
	EvTimeout
	EvCancel
	numEvents
)

// Mode is derived from State.
type Mode uint8

const (
	modeNone Mode = iota
	Normal
	Closed
	numModes
)

// Action is something the holder must do after a transition.
type Action uint8

const (
	ActNone Action = iota
	ActSend
	ActResend
	ActStartTimer
	ActCancelTimer
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

// MaxRetries is the protocol's resend budget before giving up.
const MaxRetries = 3

const maxActions = 3 // most actions any one transition emits, incl. ActModeChanged

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
		panic("order: action index out of range")
	}
	return a.list[i]
}

func one(x Action) Actions { return Actions{}.with(x) }

func (a Actions) with(x Action) Actions {
	if int(a.n) >= maxActions {
		panic("order: more than maxActions actions")
	}
	a.list[a.n] = x
	a.n++
	return a
}

// Machine is one order. Build it with New and change it only with Fire.
type Machine struct {
	state   State
	retries uint8
}

// New returns an order in Idle.
func New() Machine { return Machine{state: Idle} }

// State returns the control state.
func (m Machine) State() State { return m.state }

// Retries returns how many resends have been made.
func (m Machine) Retries() uint8 { return m.retries }

// Mode derives the mode from the state.
func (m Machine) Mode() Mode {
	switch m.state {
	case Idle, Pending:
		return Normal
	case Paid, Failed, Cancelled:
		return Closed
	default:
		panic("order: invalid state; machine not built with New")
	}
}

// AppendKey appends every field, in declaration order.
func (m Machine) AppendKey(b []byte) []byte {
	return append(b, byte(m.state), m.retries)
}

func modeAllowed(from, to Mode) bool {
	switch from {
	case Normal:
		return to == Closed
	case Closed:
		return false
	default:
		panic("order: invalid mode")
	}
}

// Fire applies e and returns the next machine, the actions to perform and
// what happened.
func (m Machine) Fire(e Event) (Machine, Actions, Outcome) {
	var next Machine
	var acts Actions
	var out Outcome
	switch m.state {
	case Idle:
		next, acts, out = m.fireIdle(e)
	case Pending:
		next, acts, out = m.firePending(e)
	case Paid:
		next, acts, out = m.firePaid(e)
	case Failed:
		next, acts, out = m.fireFailed(e)
	case Cancelled:
		next, acts, out = m.fireCancelled(e)
	default:
		panic("order: invalid state; machine not built with New")
	}
	if next.Mode() != m.Mode() {
		if !modeAllowed(m.Mode(), next.Mode()) {
			panic("order: undeclared mode change")
		}
		acts = acts.with(ActModeChanged)
	}
	return next, acts, out
}

func (m Machine) fireIdle(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvSubmit:
		m.state = Pending
		return m, one(ActSend).with(ActStartTimer), Moved
	case EvAck, EvTimeout:
		return m, Actions{}, Ignored
	case EvCancel:
		m.state = Cancelled
		return m, Actions{}, Moved
	default:
		panic("order: undeclared event in Idle")
	}
}

func (m Machine) firePending(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvSubmit:
		return m, Actions{}, Rejected
	case EvAck:
		m.state = Paid
		return m, one(ActCancelTimer), Moved
	case EvTimeout:
		if m.retries >= MaxRetries {
			m.state = Failed
			return m, Actions{}, Moved
		}
		m.retries++
		return m, one(ActResend).with(ActStartTimer), Moved
	case EvCancel:
		m.state = Cancelled
		return m, one(ActCancelTimer), Moved
	default:
		panic("order: undeclared event in Pending")
	}
}

func (m Machine) firePaid(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvSubmit, EvCancel:
		return m, Actions{}, Rejected
	case EvAck, EvTimeout:
		return m, Actions{}, Ignored
	default:
		panic("order: undeclared event in Paid")
	}
}

func (m Machine) fireFailed(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvSubmit, EvCancel:
		return m, Actions{}, Rejected
	case EvAck, EvTimeout:
		return m, Actions{}, Ignored
	default:
		panic("order: undeclared event in Failed")
	}
}

func (m Machine) fireCancelled(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvSubmit, EvCancel:
		return m, Actions{}, Rejected
	case EvAck, EvTimeout:
		return m, Actions{}, Ignored
	default:
		panic("order: undeclared event in Cancelled")
	}
}

func (s State) String() string {
	switch s {
	case Idle:
		return "Idle"
	case Pending:
		return "Pending"
	case Paid:
		return "Paid"
	case Failed:
		return "Failed"
	case Cancelled:
		return "Cancelled"
	default:
		panic("order: invalid state")
	}
}

func (e Event) String() string {
	switch e {
	case EvSubmit:
		return "Submit"
	case EvAck:
		return "Ack"
	case EvTimeout:
		return "Timeout"
	case EvCancel:
		return "Cancel"
	default:
		panic("order: invalid event")
	}
}

func (m Mode) String() string {
	switch m {
	case Normal:
		return "Normal"
	case Closed:
		return "Closed"
	default:
		panic("order: invalid mode")
	}
}
