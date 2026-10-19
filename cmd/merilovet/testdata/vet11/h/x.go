package h

import "fixture/m"

func Decode(b byte) m.Event { return m.Event(b) }
