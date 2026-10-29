// Package order is a payment-order state machine: submit a charge to a
// provider, resend on timeout up to MaxRetries, and finish as Paid, Failed
// or Cancelled.
package order

import "code.eitanity.com/merilo/fsm"

// State is an order's control state.
type State uint8

// State values.
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

// Event values.
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

// Mode values.
const (
	modeNone Mode = iota
	Normal
	Closed
	numModes
)

// Actions is the set of things the holder must do after a transition, one
// bit each. The holder performs them in declaration order. The zero value
// is the empty set.
type Actions uint64

// Actions values.
const (
	ActSend Actions = 1 << iota
	ActResend
	ActStartTimer
	ActCancelTimer
	ActModeChanged
)

// invalidName is what String returns for a value outside the declared constants.
const invalidName = "invalid"

// MaxRetries is the protocol's resend budget before giving up.
const MaxRetries = 3

// Machine is one order. Build it with New and change it only with Fire.
type Machine struct {
	state   State
	retries fsm.Counter // resends made, up to MaxRetries
}

// New returns an order in Idle.
func New() Machine { return Machine{state: Idle, retries: fsm.NewCounter(MaxRetries)} }

// State returns the control state.
func (m Machine) State() State { return m.state }

// Retries returns how many resends have been made.
func (m Machine) Retries() int64 { return m.retries.Value() }

// Mode derives the mode from the state.
func (m Machine) Mode() Mode {
	switch m.state {
	case Idle, Pending:
		return Normal
	case Paid, Failed, Cancelled:
		return Closed
	default:
		return modeNone
	}
}

func modeAllowed(from, to Mode) bool {
	switch from {
	case Normal:
		return to == Closed
	case Closed:
		return false
	default:
		return false
	}
}

// Fire applies e and returns the next machine, the actions to perform and
// what happened.
func (m Machine) Fire(e Event) (Machine, Actions, fsm.Outcome) {
	var next Machine
	var acts Actions
	var out fsm.Outcome
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
		return m, 0, fsm.Invalid
	}
	if next.Mode() != m.Mode() {
		if !modeAllowed(m.Mode(), next.Mode()) {
			return m, 0, fsm.Invalid
		}
		acts |= ActModeChanged
	}
	return next, acts, out
}

func (m Machine) fireIdle(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvSubmit:
		m.state = Pending
		return m, ActSend | ActStartTimer, fsm.Moved
	case EvAck, EvTimeout:
		return m, 0, fsm.Ignored
	case EvCancel:
		m.state = Cancelled
		return m, 0, fsm.Moved
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) firePending(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvSubmit:
		return m, 0, fsm.Rejected
	case EvAck:
		m.state = Paid
		return m, ActCancelTimer, fsm.Moved
	case EvTimeout:
		retries, ok := m.retries.Inc()
		if !ok {
			m.state = Failed
			return m, 0, fsm.Moved
		}
		m.retries = retries
		return m, ActResend | ActStartTimer, fsm.Moved
	case EvCancel:
		m.state = Cancelled
		return m, ActCancelTimer, fsm.Moved
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) firePaid(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvSubmit, EvCancel:
		return m, 0, fsm.Rejected
	case EvAck, EvTimeout:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireFailed(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvSubmit, EvCancel:
		return m, 0, fsm.Rejected
	case EvAck, EvTimeout:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireCancelled(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvSubmit, EvCancel:
		return m, 0, fsm.Rejected
	case EvAck, EvTimeout:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
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
		return invalidName
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
		return invalidName
	}
}

func (m Mode) String() string {
	switch m {
	case Normal:
		return "Normal"
	case Closed:
		return "Closed"
	default:
		return invalidName
	}
}
