// Command orderd drives one order from commands on standard input
// ("submit", "ack", "cancel"), with a real timer for timeouts. It is the
// production holder for package order.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/eitanity/merilo/examples/order"
)

const timeout = 2 * time.Second

// expiry is a timer firing, tagged with the generation it was started in.
type expiry struct{ gen uint64 }

// client holds one order and the real timer its actions ask for.
type client struct {
	m      order.Machine
	timer  *time.Timer
	gen    uint64
	expire chan expiry
}

func main() {
	c := &client{m: order.New(), expire: make(chan expiry, 1)}
	lines := make(chan string)
	go read(lines)
	if err := c.loop(lines); err != nil {
		fmt.Fprintln(os.Stderr, "orderd:", err)
		os.Exit(1)
	}
}

func read(lines chan<- string) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		lines <- sc.Text()
	}
	close(lines)
}

// loop is the owner loop: the only goroutine that touches the machine.
func (c *client) loop(lines <-chan string) error {
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				fmt.Println("final state:", c.m.State())
				return nil
			}
			e, err := decode(line)
			if err != nil {
				fmt.Println(err)
				continue
			}
			if err := c.handle(e); err != nil {
				fmt.Println(err)
			}
		case ex := <-c.expire:
			if ex.gen != c.gen {
				continue // stale expiry from a timer already replaced or cancelled (TM-05)
			}
			if err := c.handle(order.EvTimeout); err != nil {
				return err
			}
		}
	}
}

// decode turns wire input into an event through a switch (VET-11).
func decode(line string) (order.Event, error) {
	switch line {
	case "submit":
		return order.EvSubmit, nil
	case "ack":
		return order.EvAck, nil
	case "cancel":
		return order.EvCancel, nil
	default:
		return order.EvSubmit, errors.New("unknown command " + line)
	}
}

func (c *client) handle(e order.Event) error {
	var acts order.Actions
	var out order.Outcome
	c.m, acts, out = c.m.Fire(e)
	switch out {
	case order.Moved, order.Ignored:
	case order.Rejected:
		return fmt.Errorf("%v rejected in %v", e, c.m.State())
	default:
		panic("orderd: invalid outcome")
	}
	for i := range acts.Len() {
		c.do(acts.At(i))
	}
	return nil
}

func (c *client) do(a order.Action) {
	switch a {
	case order.ActSend, order.ActResend:
		fmt.Println("send charge request")
	case order.ActStartTimer:
		c.stop()
		gen := c.gen
		c.timer = time.AfterFunc(timeout, func() { c.expire <- expiry{gen: gen} })
	case order.ActCancelTimer:
		c.stop()
	case order.ActModeChanged:
		fmt.Println("mode:", c.m.Mode())
	case order.ActNone:
	default:
		panic("orderd: invalid action")
	}
}

// stop cancels any running timer and moves to a new generation, so an
// expiry already in flight is recognised as stale.
func (c *client) stop() {
	if c.timer != nil {
		c.timer.Stop()
	}
	c.gen++
}
