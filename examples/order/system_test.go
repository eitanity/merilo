package order_test

import (
	"errors"
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/examples/order"
	"code.eitanity.com/merilo/fsm"
)

// Step kinds. A network step has one kind per envelope slot: kDeliver+i
// delivers the envelope in slot i, and so on.
const (
	kSubmit = iota
	kCancel
	kTick
	kDeliver
	kDrop    = kDeliver + maxInflight
	kDup     = kDrop + maxInflight
	numKinds = kDup + maxInflight
)

// slot splits kind k into its step and, for a network step, the slot.
func slot(k int) (step, i int) {
	switch {
	case k >= kDup:
		return kDup, k - kDup
	case k >= kDrop:
		return kDrop, k - kDrop
	case k >= kDeliver:
		return kDeliver, k - kDeliver
	}
	return k, 0
}

// msgKind is what an envelope carries.
type msgKind uint8

const (
	msgNone   msgKind = iota
	msgCharge         // client to provider
	msgAck            // provider to client
)

// One charge per send and resend, an ack for each, and one duplicate.
const maxInflight = 2*(1+order.MaxRetries) + 1

// SysMode is the system mode, derived from the order's mode.
type SysMode uint8

const (
	sysNone SysMode = iota
	SysOpen
	SysClosed
)

func (m SysMode) String() string {
	switch m {
	case SysOpen:
		return "Open"
	case SysClosed:
		return "Closed"
	default:
		return "invalid"
	}
}

func sysModeAllowed(from, to SysMode) bool { return from == SysOpen && to == SysClosed }

// orderSys is one client order, one idempotent payment provider and a
// lossy, duplicating network between them.
type orderSys struct {
	m           order.Machine
	timer       bool
	charged     bool
	submitted   bool
	cancelled   bool
	inflight    [maxInflight]msgKind
	n           int
	drops, dups int
}

func newOrderSystem() *orderSys {
	s := &orderSys{m: order.New()}
	s.Reset()
	return s
}

func (s *orderSys) Reset() {
	*s = orderSys{m: order.New(), drops: 1, dups: 1}
}

func (s *orderSys) Kinds() int { return numKinds }

func (s *orderSys) Invariants() []string {
	return []string{"paid only after the provider charged", "timer runs only while pending"}
}

func (s *orderSys) Enabled(k int) bool {
	step, i := slot(k)
	switch step {
	case kSubmit:
		return !s.submitted
	case kCancel:
		return !s.cancelled
	case kTick:
		return s.timer
	case kDeliver:
		return i < s.n
	case kDrop:
		return i < s.n && s.drops > 0
	case kDup:
		return i < s.n && s.dups > 0 && s.n < maxInflight
	}
	return false
}

func (s *orderSys) Apply(k int) error {
	before := s.sysMode()
	if err := s.apply(k); err != nil {
		return err
	}
	if after := s.sysMode(); before != after && !sysModeAllowed(before, after) {
		return fmt.Errorf("%w: %v -> %v", merilo.ErrModeChange, before, after)
	}
	return nil
}

func (s *orderSys) apply(k int) error {
	step, i := slot(k)
	switch step {
	case kSubmit:
		s.submitted = true
		return s.fire(order.EvSubmit)
	case kCancel:
		s.cancelled = true
		return s.fire(order.EvCancel)
	case kDeliver:
		msg := s.take(i)
		if msg == msgAck {
			return s.fire(order.EvAck)
		}
		s.charged = true // the provider charges once per order, however often asked
		return s.send(msgAck)
	case kDrop:
		s.drops--
		s.take(i)
		return nil
	case kDup:
		s.dups--
		return s.send(s.inflight[i])
	case kTick:
		s.timer = false
		return s.fire(order.EvTimeout)
	default:
		return fmt.Errorf("unknown step kind %d", k)
	}
}

// fire drives the machine exactly as production code does and performs
// its actions.
func (s *orderSys) fire(e order.Event) error {
	var acts order.Actions
	var out fsm.Outcome
	s.m, acts, out = s.m.Fire(e)
	if out == fsm.Invalid || out == fsm.Rejected && e != order.EvCancel && e != order.EvSubmit {
		return fmt.Errorf("order rejected %v", e)
	}
	for a := order.Actions(1); a != 0; a <<= 1 {
		switch acts & a {
		case order.ActSend, order.ActResend:
			if err := s.send(msgCharge); err != nil {
				return err
			}
		case order.ActStartTimer:
			s.timer = true
		case order.ActCancelTimer:
			s.timer = false
		case order.ActModeChanged:
		}
	}
	return nil
}

var errOverflow = errors.New("network overflow: more messages in flight than maxInflight")

// send adds an envelope, keeping the array sorted, so worlds that differ
// only in the order envelopes were sent are one world.
func (s *orderSys) send(k msgKind) error {
	if s.n == maxInflight {
		return errOverflow
	}
	i := s.n
	for ; i > 0 && s.inflight[i-1] > k; i-- {
		s.inflight[i] = s.inflight[i-1]
	}
	s.inflight[i] = k
	s.n++
	return nil
}

func (s *orderSys) take(i int) msgKind {
	k := s.inflight[i]
	copy(s.inflight[i:s.n], s.inflight[i+1:s.n])
	s.n--
	s.inflight[s.n] = msgNone
	return k
}

// World is the whole adapter: the order, the provider's flags and the
// network, whose envelopes sit in a fixed array.
func (s *orderSys) World() any { return *s }

func (s *orderSys) Check() error {
	if s.m.State() == order.Paid && !s.charged {
		return errors.New("paid only after the provider charged")
	}
	if s.timer && s.m.State() != order.Pending {
		return errors.New("timer runs only while pending")
	}
	return nil
}

func (s *orderSys) sysMode() SysMode {
	switch s.m.Mode() {
	case order.Normal:
		return SysOpen
	case order.Closed:
		return SysClosed
	default:
		return sysNone
	}
}

func (s *orderSys) Tuple() string { return s.m.State().String() }

// Settled: an order that is Idle, or finished, may stop by design.
func (s *orderSys) Settled() bool {
	switch s.m.State() {
	case order.Idle, order.Paid, order.Failed, order.Cancelled:
		return true
	default:
		return false
	}
}

func (s *orderSys) Describe(k int) string {
	step, i := slot(k)
	switch step {
	case kSubmit:
		return "submit"
	case kCancel:
		return "cancel"
	case kDeliver, kDrop, kDup:
		names := map[int]string{kDeliver: "deliver", kDrop: "drop", kDup: "dup"}
		what := map[msgKind]string{msgCharge: "charge", msgAck: "ack"}[s.inflight[i]]
		return names[step] + " " + what
	case kTick:
		return "tick"
	default:
		return fmt.Sprintf("kind %d", k)
	}
}

var opts = merilo.Options{MaxStates: 100000, RequireComplete: true}

func TestExplore(t *testing.T) {
	r := merilo.Explore(t, newOrderSystem(), opts)
	for _, want := range []string{"Idle", "Pending", "Paid", "Failed", "Cancelled"} {
		if !contains(r.Tuples, want) {
			t.Errorf("state %s never reached", want)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func FuzzOrders(f *testing.F) {
	merilo.Fuzz(f, newOrderSystem(), merilo.FuzzOptions{MaxDepth: 60, Explore: opts})
}

// watched records every world the explorer checks, to show which timer
// races it covered.
type watched struct {
	*orderSys
	paidRetries map[int64]bool
}

func (w *watched) Check() error {
	if w.m.State() == order.Paid {
		w.paidRetries[w.m.Retries()] = true
	}
	return w.orderSys.Check()
}

func TestTimerRaces(t *testing.T) {
	w := &watched{orderSys: newOrderSystem(), paidRetries: map[int64]bool{}}
	merilo.Explore(t, w, opts)
	if !w.paidRetries[0] || !w.paidRetries[1] {
		t.Errorf("ack-before-timeout and timeout-before-ack not both reached: paid with retries %v", w.paidRetries)
	}
}
