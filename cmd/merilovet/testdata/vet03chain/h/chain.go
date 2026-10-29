package h

import "code.eitanity.com/merilo/fsm"

func Verdict(out fsm.Outcome) string {
	if out == fsm.Moved {
		return "moved"
	} else if out == fsm.Rejected {
		return "rejected"
	}
	return "other"
}

func Rejected(out fsm.Outcome) bool {
	if out == fsm.Rejected {
		return true
	} else {
		return false
	}
}
