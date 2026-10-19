package h

import (
	"net"
	"time"

	"fixture/m"
)

func (n *Node) Serve(l net.Listener, ev m.Event) bool {
	go n.loop(l)
	saved := n.order
	time.Sleep(time.Millisecond)
	n.order = saved
	return n.Handle(ev)
}

func (n *Node) loop(l net.Listener) {
	if l != nil {
		n.sent++
	}
}
