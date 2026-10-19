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
	EvSubmit Event = iota
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

type Outcome uint8

const (
	outcomeNone Outcome = iota
	Moved
	Ignored
	Rejected
)

const maxActions = 2

type Actions struct {
	list [maxActions]Action
	n    uint8
}

func (a Actions) Len() int { return int(a.n) }

func (a Actions) At(i int) Action {
	if i < 0 || i >= int(a.n) {
		panic("m: action index out of range")
	}
	return a.list[i]
}

func one(x Action) Actions {
	var a Actions
	a.list[0] = x
	a.n = 1
	return a
}
