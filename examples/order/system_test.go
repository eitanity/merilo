package order_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/eitanity/merilo"
	"github.com/eitanity/merilo/examples/order"
)

// Step kinds.
const (
	kSubmit merilo.Kind = iota
	kCancel
	kDeliver
	kDrop
	kDup
	kTick
	numKinds
)

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
		panic("invalid SysMode")
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
	drops, dups uint8
}

func newOrderSystem() *orderSys {
	s := &orderSys{m: order.New()}
	s.Reset()
	return s
}

func (s *orderSys) Reset() {
	*s = orderSys{m: order.New(), drops: 1, dups: 1}
}

func (s *orderSys) Kinds() int { return int(numKinds) }

func (s *orderSys) Invariants() []string {
	return []string{"paid only after the provider charged", "timer runs only while pending"}
}

func (s *orderSys) Steps(buf []merilo.Step) []merilo.Step {
	if !s.submitted {
		buf = append(buf, merilo.Step{Kind: kSubmit})
	}
	if !s.cancelled {
		buf = append(buf, merilo.Step{Kind: kCancel})
	}
	for _, k := range []merilo.Kind{kDeliver, kDrop, kDup} {
		for i := range s.n {
			if (k == kDrop && s.drops == 0) || (k == kDup && (s.dups == 0 || s.n == maxInflight)) {
				continue
			}
			buf = append(buf, merilo.Step{Kind: k, Arg: i})
		}
	}
	if s.timer {
		buf = append(buf, merilo.Step{Kind: kTick})
	}
	return buf
}

func (s *orderSys) Apply(st merilo.Step) error {
	before := s.sysMode()
	if err := s.apply(st); err != nil {
		return err
	}
	if after := s.sysMode(); before != after && !sysModeAllowed(before, after) {
		return fmt.Errorf("%w: %v -> %v", merilo.ErrModeChange, before, after)
	}
	return nil
}

func (s *orderSys) apply(st merilo.Step) error {
	switch st.Kind {
	case kSubmit:
		s.submitted = true
		return s.fire(order.EvSubmit)
	case kCancel:
		s.cancelled = true
		return s.fire(order.EvCancel)
	case kDeliver:
		msg := s.take(st.Arg)
		if msg == msgAck {
			return s.fire(order.EvAck)
		}
		s.charged = true // the provider charges once per order, however often asked
		return s.send(msgAck)
	case kDrop:
		s.drops--
		s.take(st.Arg)
		return nil
	case kDup:
		s.dups--
		return s.send(s.inflight[st.Arg])
	case kTick:
		s.timer = false
		return s.fire(order.EvTimeout)
	default:
		return fmt.Errorf("unknown step kind %d", st.Kind)
	}
}

// fire drives the machine exactly as production code does and performs
// its actions.
func (s *orderSys) fire(e order.Event) error {
	var acts order.Actions
	var out order.Outcome
	s.m, acts, out = s.m.Fire(e)
	if out == order.Rejected && e != order.EvCancel && e != order.EvSubmit {
		return fmt.Errorf("order rejected %v", e)
	}
	for i := range acts.Len() {
		switch acts.At(i) {
		case order.ActSend, order.ActResend:
			if err := s.send(msgCharge); err != nil {
				return err
			}
		case order.ActStartTimer:
			s.timer = true
		case order.ActCancelTimer:
			s.timer = false
		case order.ActModeChanged, order.ActNone:
		}
	}
	return nil
}

var errOverflow = errors.New("network overflow: more messages in flight than maxInflight")

// send adds an envelope, keeping the array sorted so Steps lists slots in
// the order of their bytes (NET-02).
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

func (s *orderSys) Key(buf []byte) []byte {
	buf = s.m.AppendKey(buf)
	buf = append(buf, b(s.timer), b(s.charged), b(s.submitted), b(s.cancelled), s.drops, s.dups, byte(s.n))
	for _, k := range s.inflight {
		buf = append(buf, byte(k))
	}
	return buf
}

func b(v bool) byte {
	if v {
		return 1
	}
	return 0
}

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
		panic("invalid order mode")
	}
}

func (s *orderSys) Mode() fmt.Stringer { return s.sysMode() }
func (s *orderSys) Tuple() string      { return s.m.State().String() }

// Settled: an order that is Idle, or finished, may stop by design.
func (s *orderSys) Settled() bool {
	switch s.m.State() {
	case order.Idle, order.Paid, order.Failed, order.Cancelled:
		return true
	default:
		return false
	}
}

func (s *orderSys) Describe(st merilo.Step) string {
	switch st.Kind {
	case kSubmit:
		return "submit"
	case kCancel:
		return "cancel"
	case kDeliver, kDrop, kDup:
		names := map[merilo.Kind]string{kDeliver: "deliver", kDrop: "drop", kDup: "dup"}
		what := map[msgKind]string{msgCharge: "charge", msgAck: "ack"}[s.inflight[st.Arg]]
		return names[st.Kind] + " " + what
	case kTick:
		return "tick"
	default:
		return fmt.Sprintf("kind %d", st.Kind)
	}
}

var opts = merilo.Options{MaxDepth: 40, MaxStates: 100000, RequireComplete: true}

func TestExplore(t *testing.T) {
	r := merilo.Explore(t, newOrderSystem(), opts)
	for _, want := range []string{"Idle", "Pending", "Paid", "Failed", "Cancelled"} {
		if !contains(r.Tuples, want) {
			t.Errorf("state %s never reached", want)
		}
	}
	if len(r.ModeEdges) != 1 || r.ModeEdges[0] != [2]string{"Open", "Closed"} {
		t.Errorf("mode edges = %v, want only Open -> Closed", r.ModeEdges)
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

func TestDeterminism(t *testing.T) {
	merilo.CheckDeterminism(t, newOrderSystem(), opts)
}

func TestRegressions(t *testing.T) {
	merilo.Regress(t, newOrderSystem(), "orders")
}

func FuzzOrders(f *testing.F) {
	merilo.Fuzz(f, newOrderSystem(), merilo.FuzzOptions{MaxDepth: 60, Seeds: witnessesF(f)})
}

func witnessesF(f *testing.F) []merilo.Trace {
	return merilo.Explore(f, newOrderSystem(), opts).Witnesses
}

func TestCoverage(t *testing.T) {
	r := merilo.Explore(t, newOrderSystem(), opts)
	merilo.Coverage(t, newOrderSystem(), "FuzzOrders", r,
		merilo.FuzzOptions{MaxDepth: 60, Seeds: r.Witnesses},
		merilo.CoverageMin{Modes: 100, ModeEdges: 100, Tuples: 100})
}

// watched records every world the explorer checks, to show which timer
// races it covered (AC-28).
type watched struct {
	*orderSys
	paidRetries map[uint8]bool
	badTick     bool
}

func (w *watched) Check() error {
	if w.m.State() == order.Paid {
		w.paidRetries[w.m.Retries()] = true
	}
	return w.orderSys.Check()
}

func (w *watched) Steps(buf []merilo.Step) []merilo.Step {
	buf = w.orderSys.Steps(buf)
	ticks := 0
	for _, st := range buf {
		if st.Kind == kTick {
			ticks++
		}
	}
	if (ticks == 1) != w.timer {
		w.badTick = true
	}
	return buf
}

func TestTimerRaces(t *testing.T) {
	w := &watched{orderSys: newOrderSystem(), paidRetries: map[uint8]bool{}}
	merilo.Explore(t, w, opts)
	if !w.paidRetries[0] || !w.paidRetries[1] {
		t.Errorf("ack-before-timeout and timeout-before-ack not both reached: paid with retries %v", w.paidRetries)
	}
	if w.badTick {
		t.Error("a tick was offered without a running timer, or withheld from one")
	}
}
