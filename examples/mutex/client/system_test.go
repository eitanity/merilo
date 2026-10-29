package client_test

import (
	"errors"
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/examples/mutex/client"
	"code.eitanity.com/merilo/fsm"
)

// Step kinds. A client step has one kind per client and a network step
// one per envelope slot: kWant+i is client i wanting the lock, kDeliver+i
// delivers the envelope in slot i, and so on.
const (
	kWant    = 0
	kDone    = kWant + clients
	kDeliver = kDone + clients
	kDup     = kDeliver + maxInflight
	numKinds = kDup + maxInflight
)

// slot splits kind k into its step and the client or slot it names.
func slot(k int) (step, i int) {
	switch {
	case k >= kDup:
		return kDup, k - kDup
	case k >= kDeliver:
		return kDeliver, k - kDeliver
	case k >= kDone:
		return kDone, k - kDone
	}
	return kWant, k - kWant
}

const clients = 2

type msgKind uint8

const (
	msgNone msgKind = iota
	msgRequest
	msgRelease
	msgGrant
)

type envelope struct {
	kind msgKind
	who  int
}

func (e envelope) less(f envelope) bool {
	return e.kind < f.kind || (e.kind == f.kind && e.who < f.who)
}

// One request and one release per client, a grant for each, one duplicate.
const maxInflight = 3*clients + 1

// sys is two clients, a lock server and a network that may duplicate one
// message.
type sys struct {
	seeded   bool
	c        [clients]client.Machine
	wanted   [clients]bool
	holder   int // -1 when free
	waiting  [clients]bool
	served   [clients]bool // each client asks once, so a served request is a duplicate
	inflight [maxInflight]envelope
	n        int
	dups     int
}

func newSys(seeded bool) *sys {
	s := &sys{seeded: seeded, c: [clients]client.Machine{client.New(seeded), client.New(seeded)}}
	s.Reset()
	return s
}

func (s *sys) Reset() {
	*s = sys{seeded: s.seeded, c: [clients]client.Machine{client.New(s.seeded), client.New(s.seeded)}, holder: -1, dups: 1}
}

func (s *sys) Kinds() int           { return numKinds }
func (s *sys) Invariants() []string { return []string{"at most one client holds the lock"} }

func (s *sys) Enabled(k int) bool {
	if k < 0 || k >= numKinds {
		return false
	}
	step, i := slot(k)
	switch step {
	case kWant:
		return !s.wanted[i] && s.c[i].State() == client.Idle
	case kDone:
		return s.c[i].State() == client.Holding
	case kDeliver:
		return i < s.n
	case kDup:
		return i < s.n && s.dups > 0 && s.n < maxInflight
	}
	return false
}

func (s *sys) Apply(k int) error {
	step, i := slot(k)
	switch step {
	case kWant:
		s.wanted[i] = true
		return s.fire(i, client.EvWant)
	case kDone:
		return s.fire(i, client.EvDone)
	case kDeliver:
		return s.deliver(s.take(i))
	case kDup:
		s.dups--
		return s.send(s.inflight[i])
	default:
		return fmt.Errorf("unknown step kind %d", k)
	}
}

func (s *sys) fire(i int, e client.Event) error {
	var acts client.Actions
	var out fsm.Outcome
	s.c[i], acts, out = s.c[i].Fire(e)
	if out == fsm.Rejected || out == fsm.Invalid {
		return fmt.Errorf("client %d rejected event %d", i, e)
	}
	for a := client.Actions(1); a != 0; a <<= 1 {
		switch acts & a {
		case client.ActRequest:
			if err := s.send(envelope{msgRequest, i}); err != nil {
				return err
			}
		case client.ActRelease:
			if err := s.send(envelope{msgRelease, i}); err != nil {
				return err
			}
		case client.ActModeChanged:
		}
	}
	return nil
}

// deliver hands a message to the server or to a client.
func (s *sys) deliver(e envelope) error {
	who := e.who
	switch e.kind {
	case msgGrant:
		return s.fire(who, client.EvGrant)
	case msgRequest:
		if s.served[who] || s.waiting[who] || s.holder == who {
			return nil // duplicate request: the server is idempotent
		}
		if s.holder >= 0 {
			s.waiting[who] = true
			return nil
		}
		s.holder, s.served[who] = who, true
		return s.send(envelope{msgGrant, e.who})
	case msgRelease:
		if s.holder != who {
			return nil
		}
		s.holder = -1
		for j := range clients {
			if s.waiting[j] {
				s.waiting[j], s.holder, s.served[j] = false, j, true
				return s.send(envelope{msgGrant, j})
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown message %d", e.kind)
	}
}

func (s *sys) send(e envelope) error {
	if s.n == maxInflight {
		return errors.New("network overflow")
	}
	i := s.n
	for ; i > 0 && e.less(s.inflight[i-1]); i-- {
		s.inflight[i] = s.inflight[i-1]
	}
	s.inflight[i] = e
	s.n++
	return nil
}

func (s *sys) take(i int) envelope {
	e := s.inflight[i]
	copy(s.inflight[i:s.n], s.inflight[i+1:s.n])
	s.n--
	s.inflight[s.n] = envelope{}
	return e
}

// World is the whole adapter: both clients, the server's state and the
// network, whose envelopes sit in a fixed array.
func (s *sys) World() any { return *s }

func (s *sys) Check() error {
	holding := 0
	for i := range clients {
		if s.c[i].State() == client.Holding {
			holding++
		}
	}
	if holding > 1 {
		return errors.New("at most one client holds the lock")
	}
	return nil
}

func (s *sys) Tuple() string { return s.c[0].State().String() + "," + s.c[1].State().String() }

func (s *sys) Settled() bool {
	for i := range clients {
		if s.c[i].State() == client.Waiting {
			return false
		}
	}
	return true
}

func (s *sys) Describe(k int) string {
	names := map[msgKind]string{msgRequest: "request", msgRelease: "release", msgGrant: "grant"}
	step, i := slot(k)
	switch step {
	case kWant:
		return fmt.Sprintf("c%d wants the lock", i)
	case kDone:
		return fmt.Sprintf("c%d is done", i)
	case kDeliver, kDup:
		e := s.inflight[i]
		verb := map[int]string{kDeliver: "deliver", kDup: "duplicate"}[step]
		return fmt.Sprintf("%s %s c%d", verb, names[e.kind], e.who)
	default:
		return fmt.Sprintf("kind %d", k)
	}
}

var opts = merilo.Options{MaxStates: 100000, RequireComplete: true}

// A client that takes a duplicated grant while idle ends up
// holding the lock alongside the rightful holder.
func TestSeededDuplicateGrant(t *testing.T) {
	r := merilo.Explore(&expectFail{T: t}, newSys(true), opts)
	if r.Violation.Kind != merilo.Invariant {
		t.Fatalf("got %v, want the two-holders invariant", r.Violation.Kind)
	}
	t.Logf("two holders after %d steps", len(r.Violation.Trace))
}

type expectFail struct{ *testing.T }

func (e *expectFail) Errorf(format string, args ...any) { e.Logf("expected: "+format, args...) }

// The correct client keeps the lock exclusive.
func TestCorrect(t *testing.T) {
	r := merilo.Explore(t, newSys(false), opts)
	if !r.Complete || r.Violation.Found() {
		t.Fatalf("complete=%t violation=%v", r.Complete, r.Violation.Kind)
	}
}
