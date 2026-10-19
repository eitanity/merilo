// Package coord is a two-phase-commit coordinator for two participants.
// It asks for votes, retries once on timeout, and commits only when both
// participants voted yes. New can also build it with one of two known bugs,
// for the acceptance tests.
package coord

// Participants is the number of votes a commit needs.
const Participants = 2

// State is the coordinator's control state.
type State uint8

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

const (
	modeNone Mode = iota
	Normal
	Degraded
	Recovery
	Closed
	numModes
)

// Action is something the holder must do after a transition.
type Action uint8

const (
	ActNone Action = iota
	ActPrepare
	ActCommit
	ActAbort
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

// Bug selects a seeded defect. NoBug is the correct coordinator.
type Bug uint8

const (
	bugNone Bug = iota
	NoBug
	CommitOnTimeout // commits on a timeout after one yes vote
	SkipRecovery    // returns from Retrying straight to Collecting
)

const maxActions = 3

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
		panic("coord: action index out of range")
	}
	return a.list[i]
}

func one(x Action) Actions { return Actions{}.with(x) }

func (a Actions) with(x Action) Actions {
	if int(a.n) >= maxActions {
		panic("coord: more than maxActions actions")
	}
	a.list[a.n] = x
	a.n++
	return a
}

// Machine is one coordinator. Build it with New and change it only with Fire.
type Machine struct {
	state State
	yes   uint8
	bug   Bug
}

// New returns a coordinator in Init. Pass NoBug for the correct one, or a
// seeded bug for the acceptance tests.
func New(b Bug) Machine {
	switch b {
	case NoBug, CommitOnTimeout, SkipRecovery:
		return Machine{state: Init, bug: b}
	default:
		panic("coord: invalid bug")
	}
}

// State returns the control state.
func (m Machine) State() State { return m.state }

// Yes returns the yes votes counted in this round.
func (m Machine) Yes() uint8 { return m.yes }

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
		panic("coord: invalid state; machine not built with New")
	}
}

// AppendKey appends every field, in declaration order.
func (m Machine) AppendKey(b []byte) []byte {
	return append(b, byte(m.state), m.yes, byte(m.bug))
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
		panic("coord: invalid mode")
	}
}

// Fire applies e and returns the next machine, its actions and what happened.
func (m Machine) Fire(e Event) (Machine, Actions, Outcome) {
	var next Machine
	var acts Actions
	var out Outcome
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
		panic("coord: invalid state; machine not built with New")
	}
	if next.Mode() != m.Mode() {
		if !modeAllowed(m.Mode(), next.Mode()) {
			panic("coord: undeclared mode change")
		}
		acts = acts.with(ActModeChanged)
	}
	return next, acts, out
}

func (m Machine) fireInit(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvStart:
		m.state = Collecting
		return m, one(ActPrepare).with(ActStartTimer), Moved
	case EvYes, EvNo, EvTimeout, EvResume:
		return m, Actions{}, Ignored
	default:
		panic("coord: undeclared event in Init")
	}
}

func (m Machine) fireCollecting(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvStart:
		return m, Actions{}, Rejected
	case EvYes:
		m.yes++
		if m.yes == Participants {
			m.state = Committed
			return m, one(ActCommit).with(ActCancelTimer), Moved
		}
		return m, Actions{}, Moved
	case EvNo:
		m.state = Aborted
		return m, one(ActAbort).with(ActCancelTimer), Moved
	case EvTimeout:
		if m.bug == CommitOnTimeout && m.yes > 0 {
			m.state = Committed
			return m, one(ActCommit), Moved
		}
		m.state = Retrying
		return m, one(ActPrepare).with(ActStartTimer), Moved
	case EvResume:
		return m, Actions{}, Ignored
	default:
		panic("coord: undeclared event in Collecting")
	}
}

func (m Machine) fireRetrying(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvStart:
		return m, Actions{}, Rejected
	case EvYes:
		m.yes++
		switch {
		case m.yes == Participants:
			m.state = Committed
			return m, one(ActCommit).with(ActCancelTimer), Moved
		case m.bug == SkipRecovery:
			m.state = Collecting
			return m, Actions{}, Moved
		}
		m.state = Recovering
		return m, one(ActCancelTimer), Moved
	case EvNo, EvTimeout:
		m.state = Aborted
		return m, one(ActAbort).with(ActCancelTimer), Moved
	case EvResume:
		return m, Actions{}, Ignored
	default:
		panic("coord: undeclared event in Retrying")
	}
}

func (m Machine) fireRecovering(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvStart:
		return m, Actions{}, Rejected
	case EvYes, EvNo, EvTimeout:
		return m, Actions{}, Ignored
	case EvResume:
		m.state = Collecting
		return m, one(ActStartTimer), Moved
	default:
		panic("coord: undeclared event in Recovering")
	}
}

func (m Machine) fireClosed(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvStart:
		return m, Actions{}, Rejected
	case EvYes, EvNo, EvTimeout, EvResume:
		return m, Actions{}, Ignored
	default:
		panic("coord: undeclared event in a closed state")
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
		panic("coord: invalid state")
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
		panic("coord: invalid mode")
	}
}
