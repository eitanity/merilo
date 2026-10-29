package h

import (
	"fixture/m"

	"code.eitanity.com/merilo/fsm"
)

type Node struct {
	order m.Machine
	sent  int
}

func NewNode() *Node { return &Node{order: m.New()} }

func (n *Node) Handle(ev m.Event) bool {
	var acts m.Actions
	var out fsm.Outcome
	n.order, acts, out = n.order.Fire(ev)
	if out == fsm.Rejected {
		return false
	}
	for i := range acts.Len() {
		n.do(acts.At(i))
	}
	return true
}

func (n *Node) do(a m.Action) {
	if a == m.ActSend {
		n.sent++
	}
}

func (n *Node) State() m.State { return n.order.State() }
