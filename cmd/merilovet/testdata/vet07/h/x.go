package h

import (
	"fixture/m"

	"code.eitanity.com/merilo/fsm"
)

func (n *Node) Cross(o *Node, ev m.Event) bool {
	var acts m.Actions
	var out fsm.Outcome
	n.order, acts, out = o.order.Fire(ev)
	return acts.Len() > 0 && out == fsm.Moved
}
