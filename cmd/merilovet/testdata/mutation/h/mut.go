package h

import "fixture/m"

type Pool struct {
	nodes    [4]Node
	sessions map[int]m.Machine
	ch       chan m.Machine
	lastOut  m.Outcome
}

func (p *Pool) passInPlace(i int, ev m.Event) bool {
	var acts m.Actions
	var out m.Outcome
	p.nodes[i].order, acts, out = p.nodes[i].order.Fire(ev)
	return acts.Len() > 0 && out == m.Moved
}

func (p *Pool) passFresh(id int) *Node {
	p.sessions[id] = m.New()
	return &Node{order: m.New()}
}

func (p *Pool) passIfInit(n *Node, ev m.Event) bool {
	var acts m.Actions
	var out m.Outcome
	if n.order, acts, out = n.order.Fire(ev); out == m.Rejected {
		return acts.Len() == 0
	}
	return true
}

func (p *Pool) failImplicit() int {
	var a Node
	b := Node{}
	c := new(Node)
	d := make([]Node, 3)
	return a.sent + b.sent + c.sent + len(d)
}

func (p *Pool) failCopy(n *Node) m.State {
	saved := n.order
	snapshot := *n
	p.ch <- n.order
	for _, nd := range p.nodes {
		p.lastOut = m.Outcome(nd.sent)
	}
	return saved.State() + snapshot.order.State()
}

func (n Node) Peek() int { return n.sent }

func (p *Pool) failLiteral(n *Node) *Node {
	return &Node{order: n.order}
}

func (p *Pool) failReset(n *Node) {
	n.order = m.New()
	*n = Node{order: m.New()}
}

func (p *Pool) failCross(i, j int, n, other *Node, ev m.Event) bool {
	var acts m.Actions
	var out m.Outcome
	p.nodes[i].order, acts, out = p.nodes[j].order.Fire(ev)
	ok := acts.Len() > 0 && out == m.Moved
	n.order, acts, out = other.order.Fire(ev)
	return ok && acts.Len() > 0 && out == m.Moved
}

func (p *Pool) next() *Node { return &p.nodes[0] }

func (p *Pool) failSideEffect(ev m.Event) bool {
	var acts m.Actions
	var out m.Outcome
	p.next().order, acts, out = p.next().order.Fire(ev)
	return acts.Len() > 0 && out == m.Moved
}

func (p *Pool) failDiscard(n *Node, ev m.Event) int {
	var acts m.Actions
	n.order, acts, _ = n.order.Fire(ev)
	return acts.Len()
}

func (p *Pool) failField(n *Node, ev m.Event) int {
	var acts m.Actions
	n.order, acts, p.lastOut = n.order.Fire(ev)
	return acts.Len()
}

func (p *Pool) failOverwrite(n *Node, ev1, ev2 m.Event) int {
	var acts m.Actions
	var out m.Outcome
	n.order, acts, out = n.order.Fire(ev1)
	n.order, acts, out = n.order.Fire(ev2)
	if out == m.Moved {
		return acts.Len()
	}
	return 0
}

func (p *Pool) failBlank(n *Node, ev m.Event) int {
	var acts m.Actions
	var out m.Outcome
	n.order, acts, out = n.order.Fire(ev)
	_ = out
	return acts.Len()
}
