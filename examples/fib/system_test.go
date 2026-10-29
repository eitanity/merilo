package fib_test

import (
	"fmt"
	"math/big"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/examples/fib"
	"code.eitanity.com/merilo/fsm"
)

// maxN is the largest input the search tries: past 1475, the last n
// whose call fits in fib.Bits bits, so the search also reaches TooLarge.
const maxN = 1478

// numStarts is how many inputs the search tries.
const numStarts = 14

// starts returns the inputs the search tries: the smallest, the edge of
// one word and of two, and each side of the last n that fits. Trying
// every n up to maxN would replay a million calls.
func starts() [numStarts]int64 {
	return [numStarts]int64{0, 1, 2, 3, 10, 91, 92, 93, 185, 186, 1474, 1475, 1476, maxN}
}

// Step kinds: kCall evaluates the call once, kNegative starts fib(-1),
// and kStart+i starts fib(starts()[i]).
const (
	kCall     = 0
	kNegative = 1
	kStart    = 2
	numKinds  = kStart + numStarts
)

// reference computes fib(0) to fib(maxN+2) by fast doubling, a different
// algorithm from the machine's, so the check is not the code checking
// itself: fib(2k) = fib(k)(2fib(k+1) - fib(k)), fib(2k+1) = fib(k)² + fib(k+1)².
func reference() [maxN + 3]*big.Int {
	var out [maxN + 3]*big.Int
	for n := range maxN + 3 {
		a, b := big.NewInt(0), big.NewInt(1) // fib(k), fib(k+1), k = 0
		for bit := 62; bit >= 0; bit-- {
			t := new(big.Int).Lsh(b, 1)
			t.Sub(t, a)
			c := new(big.Int).Mul(a, t)                                           // fib(2k)
			d := new(big.Int).Add(new(big.Int).Mul(a, a), new(big.Int).Mul(b, b)) // fib(2k+1)
			if n>>bit&1 == 0 {
				a, b = c, d
			} else {
				a, b = d, c.Add(c, d)
			}
		}
		out[n] = a
	}
	return out
}

// sys is one call: the test picks n, then evaluates the call until it
// returns, counting the tail calls.
type sys struct {
	ref     [maxN + 3]*big.Int
	starts  [numStarts]int64
	started bool
	n0      int64
	calls   int64 // tail calls made
	m       fib.Machine
}

// world is what the explorer compares: everything but the reference.
type world struct {
	started bool
	n0      int64
	calls   int64
	m       fib.Machine
}

func newSys() *sys {
	s := &sys{ref: reference(), starts: starts()}
	s.Reset()
	return s
}

func (s *sys) Reset()     { *s = sys{ref: s.ref, starts: s.starts} }
func (s *sys) Kinds() int { return numKinds }

func (s *sys) Invariants() []string {
	return []string{
		"after k tail calls, fib(n) is the call fib(n-k, fib(k), fib(k+1))",
		"a call returns fib(n) after exactly n tail calls",
		"a call is TooLarge only when its tail call's b, fib(k+2), would not fit in fib.Bits bits",
		"a negative n is refused, never computed",
	}
}

func (s *sys) Enabled(k int) bool {
	if k == kCall {
		return s.started && s.m.State() == fib.Calling
	}
	return !s.started && k >= kNegative && k < numKinds
}

func (s *sys) Apply(k int) error {
	if k != kCall {
		s.n0 = -1
		if k >= kStart {
			s.n0 = s.starts[k-kStart]
		}
		s.started, s.m = true, fib.New(s.n0)
		return nil
	}
	var out fsm.Outcome
	s.m, out = s.m.Fire(fib.EvCall)
	if out != fsm.Moved {
		return fmt.Errorf("a call while Calling gave outcome %v", out)
	}
	if s.m.State() == fib.Calling {
		s.calls++
	}
	return nil
}

func (s *sys) World() any {
	return world{started: s.started, n0: s.n0, calls: s.calls, m: s.m}
}

func (s *sys) Check() error {
	if !s.started {
		return nil
	}
	if s.n0 < 0 {
		if s.m.State() != fib.BadInput {
			return fmt.Errorf("fib(%d) was not refused", s.n0)
		}
		return nil
	}
	n, a, b := s.m.Args()
	if n != s.n0-s.calls || s.ref[s.calls].Cmp(a) != 0 || s.ref[s.calls+1].Cmp(b) != 0 {
		return fmt.Errorf("after %d tail calls from fib(%d) the call is fib(%d, %d, %d)", s.calls, s.n0, n, a, b)
	}
	switch s.m.State() {
	case fib.Returned:
		if v, ok := s.m.Result(); !ok || s.calls != s.n0 || s.ref[s.n0].Cmp(v) != 0 {
			return fmt.Errorf("fib(%d) returned %d after %d tail calls", s.n0, v, s.calls)
		}
	case fib.TooLarge:
		if n == 0 || s.ref[s.calls+2].BitLen() <= fib.Bits {
			return fmt.Errorf("fib(%d) is TooLarge after %d tail calls, and fib(%d) fits", s.n0, s.calls, s.calls+2)
		}
	case fib.Calling, fib.BadInput:
	}
	return nil
}

func (s *sys) Tuple() string {
	if !s.started {
		return "Unstarted"
	}
	return s.m.State().String()
}

// Settled: before a start, and once the call has returned or been refused.
// A machine Calling can always be evaluated, so it is never stuck.
func (s *sys) Settled() bool { return !s.started || s.m.State() != fib.Calling }

func (s *sys) Describe(k int) string {
	switch k {
	case kCall:
		return "evaluate the call"
	case kNegative:
		return "start fib(-1)"
	}
	return fmt.Sprintf("start fib(%d)", s.starts[k-kStart])
}

var opts = merilo.Options{MaxStates: 20000, RequireComplete: true}

// TestExplore checks each start, and a negative one, at every tail call.
func TestExplore(t *testing.T) {
	r := merilo.Explore(t, newSys(), opts)
	if len(r.Tuples) != 5 {
		t.Errorf("tuples %v; want Unstarted, Calling, Returned, TooLarge and BadInput", r.Tuples)
	}
}

func FuzzFib(f *testing.F) {
	merilo.Fuzz(f, newSys(), merilo.FuzzOptions{MaxDepth: maxN + 3, Explore: opts})
}
