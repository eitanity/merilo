package m

import "code.eitanity.com/merilo/fsm"

func (m Machine) Mode() Mode {
	switch m.state {
	case Idle, Pending:
		return Normal
	case Paid:
		return Closed
	default:
		return modeNone
	}
}

func (m Machine) Fire(e Event) (Machine, Actions, fsm.Outcome) {
	switch m.state {
	case Idle:
		return m.fireIdle(e)
	case Pending:
		return m.firePending(e)
	case Paid:
		return m, Actions{}, fsm.Ignored
	default:
		return m, Actions{}, fsm.Invalid
	}
}

func (m Machine) fireIdle(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvSubmit:
		if m.Count >= maxCount {
			return m, Actions{}, fsm.Rejected
		}
		m.state = Pending
		m.Count++
		return m, one(ActSend), fsm.Moved
	case EvAck:
		return m, Actions{}, fsm.Rejected
	default:
		return m, Actions{}, fsm.Invalid
	}
}

func (m Machine) firePending(e Event) (Machine, Actions, fsm.Outcome) {
	switch e {
	case EvSubmit:
		return m, Actions{}, fsm.Rejected
	case EvAck:
		m.state = Paid
		return m, Actions{}, fsm.Moved
	default:
		return m, Actions{}, fsm.Invalid
	}
}

const maxCount = 8
