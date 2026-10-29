package sh

import (
	"fixture/s"

	"code.eitanity.com/merilo/fsm"
)

type Gate struct {
	t     s.Machine
	moves int
}

func NewGate() *Gate { return &Gate{t: s.New()} }

func (g *Gate) Handle(e s.Event) {
	var out fsm.Outcome
	g.t, out = g.t.Fire(e)
	if out == fsm.Moved {
		g.moves++
	}
}
