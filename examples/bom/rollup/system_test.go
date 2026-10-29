package rollup

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/examples/bom"
)

// Parts of the test bills: a is the system, b and c go into it.
const (
	a = iota
	b
	c
)

// scenario is one bill and the calls it is walked within.
type scenario struct {
	name   string
	bill   Bill
	calls  int64
	cyclic bool // a part contains itself
}

const roomy = 64

// scenarios lists every bill of the three parts whose lines go a to b, a
// to c and b to c, each absent or of quantity 1 or 3, so c is reached by
// two paths in some; bills where a part contains itself; a bill walked
// within too few calls; and bills whose numbers do not fit in 64 bits.
func scenarios() []scenario {
	var out []scenario
	qs := []int64{0, 1, 3}
	for _, ab := range qs {
		for _, ac := range qs {
			for _, bc := range qs {
				out = append(out, scenario{
					name: fmt.Sprintf("a-b %d, a-c %d, b-c %d", ab, ac, bc),
					bill: bill(lines(b, ab, c, ac), lines(c, bc), nil), calls: roomy,
				})
			}
		}
	}
	return append(out,
		scenario{name: "c contains a", bill: bill(lines(b, 1), lines(c, 1), lines(a, 1)), calls: roomy, cyclic: true},
		scenario{name: "b contains b", bill: bill(lines(b, 1), lines(b, 2), nil), calls: roomy, cyclic: true},
		scenario{name: "a contains a", bill: bill(lines(a, 1), nil, nil), calls: roomy, cyclic: true},
		scenario{name: "too few calls", bill: bill(lines(b, 1, c, 1), lines(c, 1), nil), calls: 7},
		scenario{name: "just enough calls", bill: bill(lines(b, 1, c, 1), lines(c, 1), nil), calls: 8},
		// Each overflow below is caught by one check alone: with every
		// value zero, the subtotals stay zero.
		scenario{name: "a line's times overflows", bill: valued(0, 0, 0, lines(b, 1<<62), lines(c, 4), nil), calls: roomy},
		scenario{name: "need summed over paths overflows", bill: valued(0, 0, 0, lines(b, 1<<62, c, 1<<62), lines(c, 1), nil), calls: roomy},
		scenario{name: "need at the limit", bill: valued(0, 0, 0, lines(b, 1<<62, c, 1<<62-1), lines(c, 1), nil), calls: roomy},
		scenario{name: "a line's cost overflows", bill: valued(5, 7, 1<<62, lines(c, 3), nil, nil), calls: roomy},
		scenario{name: "a subtotal's sum overflows", bill: valued(5, 7, math.MaxInt64-10, lines(b, 1, c, 1), nil, nil), calls: roomy},
		scenario{name: "subtotal at the limit", bill: valued(5, 7, math.MaxInt64-12, lines(b, 1, c, 1), nil, nil), calls: roomy},
	)
}

// lines makes the lines from pairs of part and quantity, leaving out a
// quantity of zero.
func lines(pq ...int64) []Line {
	var out []Line
	for i := 0; i+1 < len(pq); i += 2 {
		if pq[i+1] != 0 {
			out = append(out, Line{Part: int(pq[i]), Quantity: pq[i+1]})
		}
	}
	return out
}

// bill makes the bill of a, b and c, valued 5, 7 and 11.
func bill(la, lb, lc []Line) Bill { return valued(5, 7, 11, la, lb, lc) }

// valued makes the bill of a, b and c with the given values.
func valued(va, vb, vc int64, la, lb, lc []Line) Bill {
	return Bill{Top: a, Parts: []Part{
		{Name: "a", Value: va, Lines: la},
		{Name: "b", Value: vb, Lines: lb},
		{Name: "c", Value: vc, Lines: lc},
	}}
}

// want is the rollup by plain recursion in math/big, as the definition
// reads: a different shape from the walk, so the check is not the code
// checking itself. paths counts the paths from the top part.
type want struct {
	subtotal []*big.Int
	need     []*big.Int
	paths    int64
}

func reference(bl Bill) want {
	w := want{subtotal: make([]*big.Int, len(bl.Parts)), need: make([]*big.Int, len(bl.Parts))}
	for i := range w.need {
		w.need[i], w.subtotal[i] = new(big.Int), new(big.Int)
	}
	var cost func(p int) *big.Int
	cost = func(p int) *big.Int {
		sub := big.NewInt(bl.Parts[p].Value)
		for _, ln := range bl.Parts[p].Lines {
			sub.Add(sub, new(big.Int).Mul(big.NewInt(ln.Quantity), cost(ln.Part)))
		}
		w.subtotal[p] = sub
		return sub
	}
	var need func(p int, times *big.Int)
	need = func(p int, times *big.Int) {
		w.paths++
		w.need[p].Add(w.need[p], times)
		for _, ln := range bl.Parts[p].Lines {
			need(ln.Part, new(big.Int).Mul(times, big.NewInt(ln.Quantity)))
		}
	}
	cost(bl.Top)
	need(bl.Top, big.NewInt(1))
	return w
}

// fits reports whether every number of w fits in an int64.
func (w want) fits() bool {
	for p := range w.need {
		if !w.need[p].IsInt64() || !w.subtotal[p].IsInt64() {
			return false
		}
	}
	return true
}

// Step kinds: kStep is one call, and kStart+i starts scenario i.
const (
	kStep  = 0
	kStart = 1
)

type sys struct {
	scen    []scenario
	refs    []want // the reference for each acyclic scenario
	started bool
	i       int
	w       *Walker
	ended   bool
	refused bool
	stopped bom.State // the state a refused walk stopped in
}

// world is what the explorer compares: the scenario and the call, which
// holds the stack and the numbers.
type world struct {
	started bool
	i       int
	m       bom.Machine
	ended   bool
	refused bool
	stopped bom.State
}

func newSys() *sys {
	s := &sys{scen: scenarios()}
	for _, sc := range s.scen {
		var r want
		if !sc.cyclic {
			r = reference(sc.bill)
		}
		s.refs = append(s.refs, r)
	}
	s.Reset()
	return s
}

func (s *sys) Reset()     { *s = sys{scen: s.scen, refs: s.refs} }
func (s *sys) Kinds() int { return kStart + len(s.scen) }

func (s *sys) Invariants() []string {
	return []string{
		"a returned rollup matches the recursive definition",
		"the total is the top's subtotal and the sum of need times value",
		"a returned walk made two calls for each path from the top part",
		"a bill where a part contains itself is refused as Cyclic, never returned",
		"a bill whose numbers do not fit in 64 bits, or that needs more calls than its limit, is refused as TooLarge",
		"a bill within its limits is never refused",
	}
}

func (s *sys) Enabled(k int) bool {
	if k == kStep {
		return s.started && !s.ended
	}
	return !s.started && k >= kStart && k < s.Kinds()
}

func (s *sys) Apply(k int) error {
	if k != kStep {
		s.started, s.i = true, k-kStart
		w, err := New(s.scen[s.i].bill, s.scen[s.i].calls)
		if err != nil {
			return err
		}
		s.w = w
		return nil
	}
	err := s.w.Step()
	var r Refused
	switch {
	case errors.As(err, &r):
		s.ended, s.refused, s.stopped = true, true, r.State
	case err != nil:
		return err
	case s.w.m.State() == bom.Returned:
		s.ended = true
	}
	return nil
}

func (s *sys) World() any {
	if !s.started {
		return world{}
	}
	return world{started: true, i: s.i, m: s.w.m, ended: s.ended, refused: s.refused, stopped: s.stopped}
}

// expected is the state scenario i's walk must end in.
func (s *sys) expected(i int) bom.State {
	sc, ref := s.scen[i], s.refs[i]
	switch {
	case sc.cyclic:
		return bom.Cyclic
	case !ref.fits() || 2*ref.paths > sc.calls:
		return bom.TooLarge
	default:
		return bom.Returned
	}
}

func (s *sys) Check() error {
	if !s.started || !s.ended {
		return nil
	}
	sc, want := s.scen[s.i], s.expected(s.i)
	if s.refused {
		if s.stopped != want {
			return fmt.Errorf("%s: refused as %v, want %v", sc.name, s.stopped, want)
		}
		return nil
	}
	if want != bom.Returned {
		return fmt.Errorf("%s: returned a rollup, want %v", sc.name, want)
	}
	return s.matches(sc, s.refs[s.i])
}

// matches compares the result with the reference, the total with the sum
// of each part's need times its value, and the calls with the paths.
func (s *sys) matches(sc scenario, ref want) error {
	r, err := s.w.Result()
	if err != nil {
		return fmt.Errorf("%s: %w", sc.name, err)
	}
	sum := new(big.Int)
	for p, part := range sc.bill.Parts {
		if ref.need[p].Cmp(big.NewInt(r.Need[p])) != 0 || ref.subtotal[p].Cmp(big.NewInt(r.Subtotal[p])) != 0 {
			return fmt.Errorf("%s: part %s needs %d at %d, want %v at %v", sc.name, part.Name, r.Need[p], r.Subtotal[p], ref.need[p], ref.subtotal[p])
		}
		sum.Add(sum, new(big.Int).Mul(big.NewInt(r.Need[p]), big.NewInt(part.Value)))
	}
	if r.Total != r.Subtotal[sc.bill.Top] || sum.Cmp(big.NewInt(r.Total)) != 0 {
		return fmt.Errorf("%s: total %d, top's subtotal %d, sum of need times value %v", sc.name, r.Total, r.Subtotal[sc.bill.Top], sum)
	}
	if calls := s.w.m.Calls(); calls != 2*ref.paths {
		return fmt.Errorf("%s: %d calls for %d paths", sc.name, calls, ref.paths)
	}
	return nil
}

func (s *sys) Tuple() string {
	if !s.started {
		return "Unstarted"
	}
	return s.w.m.State().String()
}

// Settled: before a start, and once the walk has returned or been refused.
func (s *sys) Settled() bool { return !s.started || s.ended }

func (s *sys) Describe(k int) string {
	if k == kStep {
		return "evaluate the call"
	}
	return "start " + s.scen[k-kStart].name
}

var opts = merilo.Options{MaxStates: 10000, RequireComplete: true}

// TestExplore walks every scenario to its end, checking at each call.
func TestExplore(t *testing.T) {
	r := merilo.Explore(t, newSys(), opts)
	want := []string{"Calling", "Cyclic", "Returned", "TooLarge", "Unstarted"}
	if strings.Join(r.Tuples, " ") != strings.Join(want, " ") {
		t.Errorf("tuples %v; want %v", r.Tuples, want)
	}
}

func FuzzRollup(f *testing.F) {
	merilo.Fuzz(f, newSys(), merilo.FuzzOptions{MaxDepth: 80, Explore: opts})
}
