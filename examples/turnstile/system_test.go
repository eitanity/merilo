package turnstile_test

import (
	"errors"
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/examples/turnstile"
	"code.eitanity.com/merilo/fsm"
)

const (
	kCoin = iota
	kPush
	numKinds
)

const budget = 3

// sys is one turnstile and a crowd with a few coins and a few pushes. It
// has one mode.
type sys struct {
	m             turnstile.Machine
	coins, pushes int
	paid, passed  int
}

func newSys() *sys {
	s := &sys{m: turnstile.New()}
	s.Reset()
	return s
}

func (s *sys) Reset()               { *s = sys{m: turnstile.New(), coins: budget, pushes: budget} }
func (s *sys) Kinds() int           { return numKinds }
func (s *sys) Invariants() []string { return []string{"no one passes without paying"} }

func (s *sys) Enabled(k int) bool {
	switch k {
	case kCoin:
		return s.coins > 0
	case kPush:
		return s.pushes > 0
	}
	return false
}

func (s *sys) Apply(k int) error {
	var out fsm.Outcome
	switch k {
	case kCoin:
		s.coins--
		s.m, out = s.m.Fire(turnstile.EvCoin)
		if out == fsm.Moved {
			s.paid++
		}
	case kPush:
		s.pushes--
		s.m, out = s.m.Fire(turnstile.EvPush)
		if out == fsm.Moved {
			s.passed++
		}
	default:
		return fmt.Errorf("unknown step kind %d", k)
	}
	return nil
}

// World is the whole adapter: the machine and the crowd's counters.
func (s *sys) World() any { return *s }

func (s *sys) Check() error {
	if s.passed > s.paid {
		return errors.New("no one passes without paying")
	}
	return nil
}

func (s *sys) Tuple() string { return s.m.State().String() }
func (s *sys) Settled() bool { return true }

func (s *sys) Describe(k int) string {
	if k == kCoin {
		return "coin"
	}
	return "push"
}

var opts = merilo.Options{MaxStates: 1000, RequireComplete: true}

// A one-mode machine with no actions, and a system without modes,
// verify with Explore.
func TestExplore(t *testing.T) {
	r := merilo.Explore(t, newSys(), opts)
	if !r.Complete {
		t.Fatal("search incomplete")
	}
}

func FuzzTurnstile(f *testing.F) {
	merilo.Fuzz(f, newSys(), merilo.FuzzOptions{MaxDepth: 10, Explore: opts})
}
