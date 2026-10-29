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

	"code.eitanity.com/merilo/examples/order"
	"code.eitanity.com/merilo/fsm"
)

const timeout = 2 * time.Second

// maxLine is the longest command line accepted. A longer one is an error,
// not the end of input.
const maxLine = 4096

// client holds one order and the real timer its actions ask for.
type client struct {
	m      order.Machine
	timer  *time.Timer
	expire chan struct{} // the timer expired
	quit   chan struct{} // closed when the owner loop stops, releasing every sender
}

func main() {
	c := &client{m: order.New(), expire: make(chan struct{}, 1), quit: make(chan struct{})}
	lines := make(chan string)
	readErr := make(chan error, 1)
	go read(lines, readErr, c.quit)
	err := c.loop(lines, readErr)
	close(c.quit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orderd:", err)
		os.Exit(1)
	}
}

// read sends each input line until input ends, a line is too long, or the
// owner loop stops. Its error, nil at a clean end, is sent before lines is
// closed.
func read(lines chan<- string, readErr chan<- error, quit <-chan struct{}) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 256), maxLine)
	for sc.Scan() {
		select {
		case lines <- sc.Text():
		case <-quit:
			return
		}
	}
	readErr <- sc.Err()
	close(lines)
}

// loop is the owner loop: the only goroutine that touches the machine.
func (c *client) loop(lines <-chan string, readErr <-chan error) error {
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return c.finish(readErr)
			}
			c.command(line)
		case <-c.expire:
			// A timer stopped after it fired can still deliver its expiry.
			// The order only starts a timer when none is running and only
			// stops one as it leaves Pending, so a late expiry always finds
			// it in a state that ignores EvTimeout.
			if err := c.handle(order.EvTimeout); err != nil {
				return err
			}
		}
	}
}

// decode turns wire input into an event through a switch.
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
	var out fsm.Outcome
	c.m, acts, out = c.m.Fire(e)
	if err := out.Err(); err != nil {
		return fmt.Errorf("%v in %v: %w", e, c.m.State(), err)
	}
	for a := order.Actions(1); a != 0; a <<= 1 {
		if acts&a == 0 {
			continue
		}
		if err := c.do(a); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) do(a order.Actions) error {
	switch a {
	case order.ActSend, order.ActResend:
		fmt.Println("send charge request")
	case order.ActStartTimer:
		c.stop()
		c.timer = time.AfterFunc(timeout, func() {
			select {
			case c.expire <- struct{}{}:
			case <-c.quit:
			}
		})
	case order.ActCancelTimer:
		c.stop()
	case order.ActModeChanged:
		fmt.Println("mode:", c.m.Mode())
	default:
		return fmt.Errorf("unknown action %d", a)
	}
	return nil
}

// stop cancels any running timer.
func (c *client) stop() {
	if c.timer != nil {
		c.timer.Stop()
	}
}

// finish reports how input ended: a read error, or the final state.
func (c *client) finish(readErr <-chan error) error {
	if err := <-readErr; err != nil {
		return fmt.Errorf("reading commands: %w", err)
	}
	fmt.Println("final state:", c.m.State())
	return nil
}

// command decodes one input line and fires it, reporting a bad line or a
// rejected event without stopping.
func (c *client) command(line string) {
	e, err := decode(line)
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := c.handle(e); err != nil {
		fmt.Println(err)
	}
}
