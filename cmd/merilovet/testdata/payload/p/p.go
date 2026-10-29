package p

import (
	"encoding/binary"

	"code.eitanity.com/merilo/fsm"
)

type State uint8

const (
	stateNone State = iota
	Idle
	Waiting
	Fired
)

type EventKind uint8

const (
	kindNone EventKind = iota
	EvArm
	EvTimeout
)

type Event struct {
	Kind EventKind
	Gen  uint64
}

type Machine struct {
	state State
	gen   uint64
}

func New() Machine { return Machine{state: Idle} }

func (m Machine) State() State { return m.state }

func (m Machine) Gen() uint64 { return m.gen }

func (m Machine) AppendKey(b []byte) []byte {
	return binary.AppendUvarint(append(b, byte(m.state)), m.gen)
}

func (m Machine) Fire(e Event) (Machine, fsm.Outcome) {
	switch e.Kind {
	case EvArm:
		if m.gen == maxGen {
			return m, fsm.Ignored
		}
		m.state = Waiting
		m.gen++
		return m, fsm.Moved
	case EvTimeout:
		if m.state != Waiting || e.Gen != m.gen {
			return m, fsm.Ignored
		}
		m.state = Fired
		return m, fsm.Moved
	default:
		return m, fsm.Invalid
	}
}

// maxGen is the last generation; arming past it is refused, never wrapped.
const maxGen = ^uint64(0)
