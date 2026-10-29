package watchdog_test

import (
	"errors"
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/examples/watchdog"
	"code.eitanity.com/merilo/fsm"
)

// Step kinds. An expiry has one kind per timer slot: kExpire+i is the
// timer in slot i expiring.
const (
	kArm = iota
	kKick
	kStop
	kRestart
	kExpire
	numKinds = kExpire + maxTimers
)

// Two arms and two kicks across both runs each start a timer.
const maxTimers = 4

// expiry is a timer on its way to the holder: the generation it was
// started with, and the run of the holder that started it.
type expiry struct {
	gen uint64
	run int64
}

func (e expiry) less(f expiry) bool { return e.run < f.run || (e.run == f.run && e.gen < f.gen) }

// sys models the holder's timers as expiries in flight. A cancel does not
// recall an expiry already on its way, and a restart does not recall the
// last run's, so stale timeouts reach Fire.
type sys struct {
	start       uint64 // the first run's starting generation
	fromZero    bool   // the seeded bug: a restart starts again at 0
	m           watchdog.Machine
	arms, kicks int
	stops       int
	restarts    int
	run         int64
	expiries    [maxTimers]expiry
	n           int
	fired       expiry // the timer that fired the watchdog
	latest      uint64 // generation of the most recently started timer
}

func newSys(start uint64, fromZero bool) *sys {
	s := &sys{start: start, fromZero: fromZero, m: watchdog.New(start)}
	s.Reset()
	return s
}

func (s *sys) Reset() {
	*s = sys{start: s.start, fromZero: s.fromZero, m: watchdog.New(s.start),
		arms: 2, kicks: 2, stops: 1, restarts: 1, latest: s.start}
}

func (s *sys) Kinds() int { return numKinds }

func (s *sys) Invariants() []string {
	return []string{"fired only by this run's latest timer", "generations only grow"}
}

func (s *sys) Enabled(k int) bool {
	switch {
	case k == kArm:
		return s.arms > 0
	case k == kKick:
		return s.kicks > 0
	case k == kStop:
		return s.stops > 0
	case k == kRestart:
		return s.restarts > 0
	case k >= kExpire && k < numKinds:
		return k-kExpire < s.n
	}
	return false
}

func (s *sys) Apply(k int) error {
	if k >= kExpire {
		return s.expire(k - kExpire)
	}
	switch k {
	case kArm:
		s.arms--
		return s.fire(watchdog.Event{Kind: watchdog.EvArm})
	case kKick:
		s.kicks--
		return s.fire(watchdog.Event{Kind: watchdog.EvKick})
	case kStop:
		s.stops--
		return s.fire(watchdog.Event{Kind: watchdog.EvStop})
	case kRestart:
		// The holder restarts. A correct one continues past every
		// generation it handed out; the seeded one starts again at 0.
		s.restarts--
		s.run++
		next := s.latest
		if s.fromZero {
			next = 0
		}
		s.m, s.latest = watchdog.New(next), next
		return nil
	default:
		return fmt.Errorf("unknown step kind %d", k)
	}
}

func (s *sys) expire(i int) error {
	e := s.expiries[i]
	copy(s.expiries[i:s.n], s.expiries[i+1:s.n])
	s.n--
	s.expiries[s.n] = expiry{}
	was := s.m.State()
	if err := s.fire(watchdog.Event{Kind: watchdog.EvTimeout, Gen: e.gen}); err != nil {
		return err
	}
	if was != watchdog.Fired && s.m.State() == watchdog.Fired {
		s.fired = e
	}
	return nil
}

func (s *sys) fire(e watchdog.Event) error {
	var acts watchdog.Actions
	var out fsm.Outcome
	s.m, acts, out = s.m.Fire(e)
	if out == fsm.Invalid || out == fsm.Rejected && e.Kind != watchdog.EvArm && e.Kind != watchdog.EvKick {
		return fmt.Errorf("rejected %+v", e)
	}
	for a := watchdog.Actions(1); a != 0; a <<= 1 {
		switch acts & a {
		case watchdog.ActStartTimer:
			if s.n == maxTimers {
				return errors.New("more timers in flight than maxTimers")
			}
			s.latest = s.m.Gen()
			s.add(expiry{gen: s.latest, run: s.run})
		case watchdog.ActCancelTimer:
		}
	}
	return nil
}

// add keeps the expiries sorted, so worlds that differ only in the order
// timers were started are one world.
func (s *sys) add(e expiry) {
	i := s.n
	for ; i > 0 && e.less(s.expiries[i-1]); i-- {
		s.expiries[i] = s.expiries[i-1]
	}
	s.expiries[i] = e
	s.n++
}

// World is the whole adapter: the machine, the holder's budgets and the
// timers in flight, which sit in a fixed array.
func (s *sys) World() any { return *s }

func (s *sys) Check() error {
	if s.m.State() == watchdog.Fired && (s.fired.run != s.run || s.fired.gen != s.latest) {
		return errors.New("fired only by this run's latest timer")
	}
	if s.m.Gen() < s.latest {
		return errors.New("generations only grow")
	}
	return nil
}

func (s *sys) Tuple() string { return s.m.State().String() }
func (s *sys) Settled() bool { return true }

func (s *sys) Describe(k int) string {
	if k >= kExpire && k < numKinds {
		e := s.expiries[k-kExpire]
		return fmt.Sprintf("timer gen %d from run %d expires", e.gen, e.run)
	}
	switch k {
	case kArm:
		return "arm"
	case kKick:
		return "kick"
	case kStop:
		return "stop"
	case kRestart:
		return "holder restarts"
	default:
		return fmt.Sprintf("kind %d", k)
	}
}

var opts = merilo.Options{MaxStates: 200000, RequireComplete: true}

// Stale timeouts, from this run or the last, reach Fire, which
// ignores them by generation. Starting anywhere, including next to the
// last generation, no stale timer fires the watchdog and no generation
// wraps.
func TestStaleTimeoutsIgnored(t *testing.T) {
	for _, start := range []uint64{0, 1 << 40, watchdog.MaxGen - 2} {
		r := merilo.Explore(t, newSys(start, false), opts)
		if !r.Complete || r.Violation.Found() {
			t.Fatalf("start %d: complete=%t violation=%v", start, r.Complete, r.Violation.Kind)
		}
	}
}

// A holder that restarts its generations at 0 lets a timer from the last
// run match one from this run; the explorer finds it.
func TestRestartFromZeroIsCaught(t *testing.T) {
	r := merilo.Explore(&expectFail{T: t}, newSys(0, true), opts)
	if r.Violation.Kind != merilo.Invariant {
		t.Fatalf("got %v, want the stale-run invariant", r.Violation.Kind)
	}
}

type expectFail struct{ *testing.T }

func (e *expectFail) Errorf(format string, args ...any) { e.Logf("expected: "+format, args...) }
