// Package coord is a two-phase-commit coordinator for two participants.
// It asks for votes, retries once on timeout, and commits only when both
// participants voted yes. New can also build it with one of two known bugs,
// for the acceptance tests.
package coord

import "code.eitanity.com/merilo/fsm"

// Participants is the number of votes a commit needs.
const Participants = 2

// State is the coordinator's control state.
type State uint8

// State values.
const (
	stateNone State = iota
	Init
	Collecting
	Retrying
	Recovering
	Committed
	Aborted
	numStates
)

// Event is an input to Fire.
type Event uint8

// Event values.
const (
	eventNone Event = iota
	EvStart
	EvYes
	EvNo
	EvTimeout
	EvResume
	numEvents
)

// Mode is derived from State.
type Mode uint8

// Mode values.
const (
	modeNone Mode = iota
	Normal
	Degraded
	Recovery
	Closed
	numModes
)

// Actions is the set of things the holder must do after a transition, one
// bit each. The holder performs them in declaration order. The zero value
// is the empty set.
type Actions uint64

// Actions values.
const (
	ActPrepare Actions = 1 << iota
	ActCommit
	ActAbort
	ActStartTimer
	ActCancelTimer
	ActModeChanged
)

// Bug selects a seeded defect. NoBug is the correct coordinator.
type Bug uint8

// Bug values.
const (
	bugNone Bug = iota
	NoBug
	CommitOnTimeout // commits on a timeout after one yes vote
	SkipRecovery    // returns from Retrying straight to Collecting
)

// Machine is one coordinator. Build it with New and change it only with Fire.
type Machine struct {
	state State
	yes   fsm.Counter // yes votes this round, up to Participants
	bug   Bug
}

// New returns a coordinator in Init. Pass NoBug for the correct one, or a
// seeded bug for the acceptance tests.
func New(b Bug) Machine {
	switch b {
	case NoBug, CommitOnTimeout, SkipRecovery:
		return Machine{state: Init, yes: fsm.NewCounter(Participants), bug: b}
	default:
		return Machine{}
	}
}

// State returns the control state.
func (m Machine) State() State { return m.state }

// Yes returns the yes votes counted in this round.
func (m Machine) Yes() int64 { return m.yes.Value() }

// Mode derives the mode from the state.
func (m Machine) Mode() Mode {
	switch m.state {
	case Init, Collecting:
		return Normal
	case Retrying:
		return Degraded
	case Recovering:
		return Recovery
	case Committed, Aborted:
		return Closed
	default:
		return modeNone
	}
}

func modeAllowed(from, to Mode) bool {
	switch from {
	case Normal:
		return to == Degraded || to == Closed
	case Degraded:
		return to == Recovery || to == Closed
	case Recovery:
		return to == Normal
	case Closed:
		return false
	default:
		return false
	}
}

// Fire applies e and returns the next machine, its actions and what happened.
func (m Machine) Fire(e Event) (Machine, Actions, fsm.Outcome) {
	var next Machine
	var acts Actions
	var out fsm.Outcome
	switch m.state {
	case Init:
		next, acts, out = m.fireInit(e)
	case Collecting:
		next, acts, out = m.fireCollecting(e)
	case Retrying:
		next, acts, out = m.fireRetrying(e)
	case Recovering:
		next, acts, out = m.fireRecovering(e)
	case Committed, Aborted:
		next, acts, out = m.fireClosed(e)
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

func (m Machine) fireInit(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvStart:
		m.state = Collecting
		return m, ActPrepare | ActStartTimer, fsm.Moved
	case EvYes, EvNo, EvTimeout, EvResume:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireCollecting(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvStart:
		return m, 0, fsm.Rejected
	case EvYes:
		yes, ok := m.yes.Inc()
		if !ok {
			return m, 0, fsm.Invalid // more yes votes than participants
		}
		m.yes = yes
		if yes.Full() {
			m.state = Committed
			return m, ActCommit | ActCancelTimer, fsm.Moved
		}
		return m, 0, fsm.Moved
	case EvNo:
		m.state = Aborted
		return m, ActAbort | ActCancelTimer, fsm.Moved
	case EvTimeout:
		if m.bug == CommitOnTimeout && m.yes.Value() > 0 {
			m.state = Committed
			return m, ActCommit, fsm.Moved
		}
		m.state = Retrying
		return m, ActPrepare | ActStartTimer, fsm.Moved
	case EvResume:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireRetrying(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvStart:
		return m, 0, fsm.Rejected
	case EvYes:
		yes, ok := m.yes.Inc()
		if !ok {
			return m, 0, fsm.Invalid // more yes votes than participants
		}
		m.yes = yes
		if yes.Full() {
			m.state = Committed
			return m, ActCommit | ActCancelTimer, fsm.Moved
		}
		if m.bug == SkipRecovery {
			m.state = Collecting
			return m, 0, fsm.Moved
		}
		m.state = Recovering
		return m, ActCancelTimer, fsm.Moved
	case EvNo, EvTimeout:
		m.state = Aborted
		return m, ActAbort | ActCancelTimer, fsm.Moved
	case EvResume:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireRecovering(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvStart:
		return m, 0, fsm.Rejected
	case EvYes, EvNo, EvTimeout:
		return m, 0, fsm.Ignored
	case EvResume:
		m.state = Collecting
		return m, ActStartTimer, fsm.Moved
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireClosed(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvStart:
		return m, 0, fsm.Rejected
	case EvYes, EvNo, EvTimeout, EvResume:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (s State) String() string {
	switch s {
	case Init:
		return "Init"
	case Collecting:
		return "Collecting"
	case Retrying:
		return "Retrying"
	case Recovering:
		return "Recovering"
	case Committed:
		return "Committed"
	case Aborted:
		return "Aborted"
	default:
		return "invalid"
	}
}

func (m Mode) String() string {
	switch m {
	case Normal:
		return "Normal"
	case Degraded:
		return "Degraded"
	case Recovery:
		return "Recovering"
	case Closed:
		return "Closed"
	default:
		return "invalid"
	}
}
