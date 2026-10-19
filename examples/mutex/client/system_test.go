package client_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/eitanity/merilo"
	"github.com/eitanity/merilo/examples/mutex/client"
)

const (
	kWant merilo.Kind = iota
	kDone
	kDeliver
	kDup
	numKinds
)

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
	who  uint8
}

func (e envelope) less(f envelope) bool {
	return e.kind < f.kind || (e.kind == f.kind && e.who < f.who)
}

// One request and one release per client, a grant for each, one duplicate.
const maxInflight = 3*clients + 1

type mode string

func (m mode) String() string { return string(m) }

// sys is two clients, a lock server and a network that may duplicate one
// message.
type sys struct {
	seeded   bool
	c        [clients]client.Machine
	wanted   [clients]bool
	holder   int8 // -1 when free
	waiting  [clients]bool
	served   [clients]bool // each client asks once, so a served request is a duplicate
	inflight [maxInflight]envelope
	n        int
	dups     uint8
}

func newSys(seeded bool) *sys {
	s := &sys{seeded: seeded, c: [clients]client.Machine{client.New(seeded), client.New(seeded)}}
	s.Reset()
	return s
}

func (s *sys) Reset() {
	*s = sys{seeded: s.seeded, c: [clients]client.Machine{client.New(s.seeded), client.New(s.seeded)}, holder: -1, dups: 1}
}

func (s *sys) Kinds() int           { return int(numKinds) }
func (s *sys) Invariants() []string { return []string{"at most one client holds the lock"} }

func (s *sys) Steps(buf []merilo.Step) []merilo.Step {
	for i := range clients {
		if !s.wanted[i] && s.c[i].State() == client.Idle {
			buf = append(buf, merilo.Step{Kind: kWant, Arg: i})
		}
		if s.c[i].State() == client.Holding {
			buf = append(buf, merilo.Step{Kind: kDone, Arg: i})
		}
	}
	for i := range s.n {
		buf = append(buf, merilo.Step{Kind: kDeliver, Arg: i})
	}
	if s.dups > 0 && s.n < maxInflight {
		for i := range s.n {
			buf = append(buf, merilo.Step{Kind: kDup, Arg: i})
		}
	}
	return buf
}

func (s *sys) Apply(st merilo.Step) error {
	switch st.Kind {
	case kWant:
		s.wanted[st.Arg] = true
		return s.fire(st.Arg, client.EvWant)
	case kDone:
		return s.fire(st.Arg, client.EvDone)
	case kDeliver:
		return s.deliver(s.take(st.Arg))
	case kDup:
		s.dups--
		return s.send(s.inflight[st.Arg])
	default:
		return fmt.Errorf("unknown step kind %d", st.Kind)
	}
}

func (s *sys) fire(i int, e client.Event) error {
	var acts client.Actions
	var out client.Outcome
	s.c[i], acts, out = s.c[i].Fire(e)
	if out == client.Rejected {
		return fmt.Errorf("client %d rejected event %d", i, e)
	}
	for k := range acts.Len() {
		switch acts.At(k) {
		case client.ActRequest:
			if err := s.send(envelope{msgRequest, uint8(i)}); err != nil {
				return err
			}
		case client.ActRelease:
			if err := s.send(envelope{msgRelease, uint8(i)}); err != nil {
				return err
			}
		case client.ActModeChanged, client.ActNone:
		}
	}
	return nil
}

// deliver hands a message to the server or to a client.
func (s *sys) deliver(e envelope) error {
	who := int(e.who)
	switch e.kind {
	case msgGrant:
		return s.fire(who, client.EvGrant)
	case msgRequest:
		if s.served[who] || s.waiting[who] || int(s.holder) == who {
			return nil // duplicate request: the server is idempotent
		}
		if s.holder >= 0 {
			s.waiting[who] = true
			return nil
		}
		s.holder, s.served[who] = int8(who), true
		return s.send(envelope{msgGrant, e.who})
	case msgRelease:
		if int(s.holder) != who {
			return nil
		}
		s.holder = -1
		for j := range clients {
			if s.waiting[j] {
				s.waiting[j], s.holder, s.served[j] = false, int8(j), true
				return s.send(envelope{msgGrant, uint8(j)})
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

func (s *sys) Key(buf []byte) []byte {
	for i := range clients {
		buf = s.c[i].AppendKey(buf)
		buf = append(buf, b(s.wanted[i]), b(s.waiting[i]), b(s.served[i]))
	}
	buf = append(buf, byte(s.holder), s.dups, byte(s.n))
	for _, e := range s.inflight {
		buf = append(buf, byte(e.kind), e.who)
	}
	return buf
}

func b(v bool) byte {
	if v {
		return 1
	}
	return 0
}

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

func (s *sys) Mode() fmt.Stringer { return mode("Normal") }
func (s *sys) Tuple() string      { return s.c[0].State().String() + "," + s.c[1].State().String() }

func (s *sys) Settled() bool {
	for i := range clients {
		if s.c[i].State() == client.Waiting {
			return false
		}
	}
	return true
}

func (s *sys) Describe(st merilo.Step) string {
	names := map[msgKind]string{msgRequest: "request", msgRelease: "release", msgGrant: "grant"}
	switch st.Kind {
	case kWant:
		return fmt.Sprintf("c%d wants the lock", st.Arg)
	case kDone:
		return fmt.Sprintf("c%d is done", st.Arg)
	case kDeliver, kDup:
		e := s.inflight[st.Arg]
		verb := map[merilo.Kind]string{kDeliver: "deliver", kDup: "duplicate"}[st.Kind]
		return fmt.Sprintf("%s %s c%d", verb, names[e.kind], e.who)
	default:
		return fmt.Sprintf("kind %d", st.Kind)
	}
}

var opts = merilo.Options{MaxDepth: 40, MaxStates: 100000, RequireComplete: true}

// AC-02: a client that takes a duplicated grant while idle ends up
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

// AC-04: the correct client keeps the lock exclusive.
func TestCorrect(t *testing.T) {
	r := merilo.Explore(t, newSys(false), opts)
	if !r.Complete || r.Violation.Found() {
		t.Fatalf("complete=%t violation=%v", r.Complete, r.Violation.Kind)
	}
	merilo.CheckDeterminism(t, newSys(false), opts)
}
