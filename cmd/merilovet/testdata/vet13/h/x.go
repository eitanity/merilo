package h

import "fixture/m"

func (n *Node) Cross(o *Node, ev m.Event) bool {
	var acts m.Actions
	var out m.Outcome
	n.order, acts, out = o.order.Fire(ev)
	return acts.Len() > 0 && out == m.Moved
}
