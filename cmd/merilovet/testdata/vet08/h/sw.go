package h

import "fixture/m"

func Name(o m.Outcome) string {
	switch o {
	case m.Moved:
		return "moved"
	case m.Ignored:
		return "ignored"
	default:
		panic("h: unknown outcome")
	}
}
