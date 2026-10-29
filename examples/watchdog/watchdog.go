// Package watchdog fires when it is not kicked in time. Its timeout event
// carries the generation of the timer that expired, so Fire itself tells a
// stale timeout from the current one: the check is in verified code, not
// in the holder.
package watchdog

import "code.eitanity.com/merilo/fsm"

// State is the watchdog's control state.
type State uint8

// State values. The zero value is an unexported sentinel.
const (
	stateNone State = iota
	Idle
	Armed
	Fired
	numStates
)

// EventKind says what an event is.
type EventKind uint8

// EventKind values.
const (
	kindNone EventKind = iota
	EvArm
	EvKick
	EvTimeout
	EvStop
	numKinds
)

// Event is an input to Fire. Gen is meaningful for EvTimeout: the
// generation of the timer that expired.
type Event struct {
	Kind EventKind
	Gen  uint64
}

// Actions is the set of things the holder must do after a transition, one
// bit each. The holder performs them in declaration order. The zero value
// is the empty set.
type Actions uint64

// Actions values.
const (
	ActStartTimer Actions = 1 << iota // start a timer tagged with Gen()
	ActCancelTimer
)

// Machine is one watchdog. Build it with New and change it only with Fire.
type Machine struct {
	state State
	gen   uint64 // generation of the timer that may fire it; 64 bits never wrap
}

// MaxGen is the last generation. Arming or kicking past it is refused,
// never wrapped: a wrapped generation could match a timer still in flight.
const MaxGen = ^uint64(0)

// New returns an idle watchdog whose generations continue from start. A
// holder that restarts passes a start past every generation it handed out
// before, such as a persisted epoch, so a timer left over from the last run
// cannot match one from this run.
func New(start uint64) Machine { return Machine{state: Idle, gen: start} }

// State returns the control state.
func (m Machine) State() State { return m.state }

// Gen returns the generation to tag a newly started timer with.
func (m Machine) Gen() uint64 { return m.gen }

// Fire applies e and returns the next machine, its actions and what happened.
func (m Machine) Fire(e Event) (Machine, Actions, fsm.Outcome) {
	switch m.state {
	case Idle:
		return m.fireIdle(e)
	case Armed:
		return m.fireArmed(e)
	case Fired:
		return m.fireFired(e)
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireIdle(e Event) (Machine, Actions, fsm.Outcome) {
	switch e.Kind {
	case EvArm:
		if m.gen == MaxGen {
			return m, 0, fsm.Rejected // generations exhausted: restart from a new epoch
		}
		m.state = Armed
		m.gen++
		return m, ActStartTimer, fsm.Moved
	case EvKick, EvTimeout, EvStop:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireArmed(e Event) (Machine, Actions, fsm.Outcome) {
	switch e.Kind {
	case EvArm:
		return m, 0, fsm.Rejected
	case EvKick:
		if m.gen == MaxGen {
			return m, 0, fsm.Rejected // generations exhausted: restart from a new epoch
		}
		m.gen++
		return m, ActStartTimer, fsm.Moved
	case EvTimeout:
		if e.Gen != m.gen {
			return m, 0, fsm.Ignored // a timer replaced by a kick or a re-arm
		}
		m.state = Fired
		return m, 0, fsm.Moved
	case EvStop:
		m.state = Idle
		return m, ActCancelTimer, fsm.Moved
	default:
		return m, 0, fsm.Invalid
	}
}

func (m Machine) fireFired(e Event) (Machine, Actions, fsm.Outcome) {
	switch e.Kind {
	case EvArm, EvKick:
		return m, 0, fsm.Rejected
	case EvTimeout, EvStop:
		return m, 0, fsm.Ignored
	default:
		return m, 0, fsm.Invalid
	}
}

func (s State) String() string {
	switch s {
	case Idle:
		return "Idle"
	case Armed:
		return "Armed"
	case Fired:
		return "Fired"
	default:
		return "invalid"
	}
}
