package m

type State uint8

const (
	stateNone State = iota
	Idle
	Pending
	Paid
	numStates
)

type Event uint8

const (
	eventNone Event = iota
	EvSubmit
	EvAck
	numEvents
)

type Mode uint8

const (
	modeNone Mode = iota
	Normal
	Closed
	numModes
)

type Action uint8

const (
	ActNone Action = iota
	ActSend
)

const maxActions = 2

type Actions struct {
	list [maxActions]Action
	n    int
}

func (a Actions) Len() int { return a.n }

func (a Actions) At(i int) Action {
	if i < 0 || i >= a.n {
		return ActNone
	}
	return a.list[i]
}

func one(x Action) Actions {
	var a Actions
	a.list[0] = x
	a.n = 1
	return a
}
