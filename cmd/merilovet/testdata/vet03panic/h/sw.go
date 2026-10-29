package h

import "code.eitanity.com/merilo/fsm"

func Name(o fsm.Outcome) string {
	switch o {
	case fsm.Moved:
		return "moved"
	case fsm.Ignored:
		return "ignored"
	case fsm.Rejected:
		return "rejected"
	case fsm.Invalid:
		return "invalid"
	default:
		panic("h: unknown outcome")
	}
}
