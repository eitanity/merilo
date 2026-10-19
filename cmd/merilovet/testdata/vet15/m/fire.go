package m

func (m Machine) Mode() Mode {
	switch m.state {
	case Idle, Pending:
		return Normal
	case Paid:
		return Closed
	default:
		panic("m: invalid state")
	}
}

func (m Machine) Fire(e Event) (Machine, Actions, Outcome) {
	switch m.state {
	case Idle:
		return m.fireIdle(e)
	case Pending:
		return m.firePending(e)
	case Paid:
		return m, Actions{}, Ignored
	default:
		panic("m: invalid state")
	}
}

func (m Machine) fireIdle(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvSubmit:
		m.state = Pending
		m.Count++
		return m, one(ActSend), Moved
	case EvAck:
		return m, Actions{}, Rejected
	default:
		panic("m: invalid event")
	}
}

func (m Machine) firePending(e Event) (Machine, Actions, Outcome) {
	switch e {
	case EvSubmit:
		return m, Actions{}, Rejected
	case EvAck:
		m.state = Paid
		return m, Actions{}, Moved
	default:
		panic("m: invalid event")
	}
}
