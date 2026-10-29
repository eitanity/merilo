package h

import "fixture/m"

func (n *Node) Drop(ev m.Event) m.Action {
	var acts m.Actions
	n.order, acts, _ = n.order.Fire(ev)
	return acts.At(0)
}
