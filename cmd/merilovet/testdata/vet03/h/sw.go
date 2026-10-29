package h

import "code.eitanity.com/merilo/fsm"

func Name(o fsm.Outcome) string {
	switch o {
	case fsm.Moved:
		return "moved"
	case fsm.Ignored:
		return "ignored"
	default:
		return "invalid"
	}
}
