package s

import (
	"encoding/binary"

	"code.eitanity.com/merilo/fsm"
)

type State uint8

const (
	stateNone State = iota
	Locked
	Unlocked
)

type Event uint8

const (
	eventNone Event = iota
	EvCoin
	EvPush
)

type Machine struct {
	state State
	coins int64
}

func New() Machine { return Machine{state: Locked} }

func (m Machine) State() State { return m.state }

func (m Machine) AppendKey(b []byte) []byte {
	return binary.AppendVarint(append(b, byte(m.state)), int64(m.coins))
}

func (m Machine) Fire(e Event) (Machine, fsm.Outcome) {
	switch m.state {
	case Locked:
		return m.fireLocked(e)
	case Unlocked:
		return m.fireUnlocked(e)
	default:
		return m, fsm.Invalid
	}
}

func (m Machine) fireLocked(e Event) (Machine, fsm.Outcome) {
	switch e {
	case EvCoin:
		if m.coins >= maxCoins {
			return m, fsm.Ignored
		}
		m.state = Unlocked
		m.coins++
		return m, fsm.Moved
	case EvPush:
		return m, fsm.Ignored
	default:
		return m, fsm.Invalid
	}
}

func (m Machine) fireUnlocked(e Event) (Machine, fsm.Outcome) {
	switch e {
	case EvCoin:
		return m, fsm.Ignored
	case EvPush:
		m.state = Locked
		return m, fsm.Moved
	default:
		return m, fsm.Invalid
	}
}

const maxCoins = 8
