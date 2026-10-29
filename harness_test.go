package merilo

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"code.eitanity.com/merilo/fsm"
)

// toy is a counter from 0 to limit with switchable faults. Positions
// stored in the fault fields are counter values; 0 means off.
type toy struct {
	n, limit   int
	deadlockAt int    // no step is enabled at this value
	settledAt  int    // Settled is also true at this value
	failAt     int    // Check fails at this value
	errAt      int    // Apply returns an error reaching this value
	modeErrAt  int    // Apply returns ErrModeChange reaching this value
	impure     *int   // a counter outside the path that Apply reads
	noInv      bool   // Invariants is empty
	label      string // Describe prefix

	kinds int // Kinds(); 2 when zero
}

func (s *toy) Reset() { s.n = 0 }
func (s *toy) Kinds() int {
	if s.kinds != 0 {
		return s.kinds
	}
	return 2
}

func (s *toy) Invariants() []string {
	if s.noInv {
		return nil
	}
	return []string{"n is never failAt"}
}

// Enabled offers kind 0 alone, until the limit or the deadlock value.
func (s *toy) Enabled(k int) bool {
	return k == 0 && (s.n != s.deadlockAt || s.n == 0) && s.n < s.limit
}

func (s *toy) Apply(k int) error {
	s.n++
	if s.impure != nil {
		*s.impure++
		s.n += *s.impure % 3
	}
	switch s.n {
	case s.errAt:
		return errors.New("overflow")
	case s.modeErrAt:
		return fmt.Errorf("%w: low -> high", ErrModeChange)
	}
	return nil
}

func (s *toy) World() any { return s.n }

func (s *toy) Check() error {
	if s.failAt != 0 && s.n == s.failAt {
		return errors.New("n is never failAt")
	}
	return nil
}

func (s *toy) Tuple() string { return fmt.Sprint(s.n) }
func (s *toy) Settled() bool { return s.n >= s.limit || (s.settledAt != 0 && s.n == s.settledAt) }
func (s *toy) Describe(k int) string {
	return fmt.Sprintf("%sstep %d from %d", s.label, k, s.n)
}

var _ System = (*toy)(nil)

// rec is a testing.TB that records instead of failing.
type rec struct {
	testing.TB
	errs, logs []string
	fatal      bool
}

func (r *rec) Helper()                        {}
func (r *rec) Errorf(format string, a ...any) { r.errs = append(r.errs, fmt.Sprintf(format, a...)) }
func (r *rec) Logf(format string, a ...any)   { r.logs = append(r.logs, fmt.Sprintf(format, a...)) }
func (r *rec) Fatalf(format string, a ...any) {
	r.Errorf(format, a...)
	r.fatal = true
	runtime.Goexit() // as testing does: stop this test's goroutine
}
func (r *rec) has(list []string, s string) bool { return strings.Contains(strings.Join(list, "\n"), s) }

// capture runs fn against a recorder on its own goroutine, so a Fatalf
// stops fn alone, as it would stop a test.
func capture(t *testing.T, fn func(tb testing.TB)) *rec {
	r := &rec{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return r
}

var small = Options{MaxStates: 1000}

func TestViolationKinds(t *testing.T) {
	cases := []struct {
		name string
		sys  *toy
		want ViolationKind
		len  int
	}{
		{"deadlock", &toy{limit: 5, deadlockAt: 3}, Deadlock, 3},
		{"settled by design", &toy{limit: 5, deadlockAt: 3, settledAt: 3}, kindNone, 0},
		{"invariant", &toy{limit: 5, failAt: 4}, Invariant, 4},
		{"apply error", &toy{limit: 5, errAt: 2}, ApplyError, 2},
		{"mode change", &toy{limit: 5, modeErrAt: 2}, ModeChange, 2},
		{"nondeterminism", &toy{limit: 5, impure: new(int)}, Nondeterminism, -1},
	}
	for _, c := range cases {
		r := search1(t, c.sys, small)
		if r.Violation.Kind != c.want || (c.len >= 0 && len(r.Violation.Trace) != c.len) {
			t.Errorf("%s: got %v with trace %v (%s), want %v of length %d",
				c.name, r.Violation.Kind, r.Violation.Trace, r.Violation.Detail, c.want, c.len)
		}
	}
}

func TestRefusesEmptyInvariants(t *testing.T) {
	r := capture(t, func(tb testing.TB) { Explore(tb, &toy{limit: 3, noInv: true}, small) })
	if !r.fatal || !r.has(r.errs, "Invariants() is empty") {
		t.Errorf("Explore ran with no invariants: errs=%v", r.errs)
	}
	for _, sys := range []*toy{{limit: 3}, {limit: 3, failAt: 2}} {
		r := capture(t, func(tb testing.TB) { Explore(tb, sys, small) })
		if !r.has(r.logs, "merilo: complete=") || !r.has(r.logs, "[n is never failAt]") {
			t.Errorf("no summary line: logs=%v", r.logs)
		}
	}
}

func TestRequireComplete(t *testing.T) {
	o := Options{MaxStates: 4, RequireComplete: true}
	r := capture(t, func(tb testing.TB) { Explore(tb, &toy{limit: 10}, o) })
	if !r.has(r.errs, "search incomplete") || !r.has(r.errs, "states=4") {
		t.Errorf("truncation not reported: %v", r.errs)
	}
	o.RequireComplete = false
	var res Result
	r = capture(t, func(tb testing.TB) { res = Explore(tb, &toy{limit: 10}, o) })
	if len(r.errs) != 0 || res.Complete || res.States != 4 || res.Depth != 3 {
		t.Errorf("without RequireComplete: errs=%v complete=%t states=%d depth=%d", r.errs, res.Complete, res.States, res.Depth)
	}
}

func TestMaskNeverDeadlocks(t *testing.T) {
	ok := runner1(t, &toy{limit: 5}, 20)
	for _, mask := range []byte{0, 1, 2, 3} {
		if v := run1(t, ok, []byte{mask, 0, 0}); v.Found() {
			t.Errorf("mask %02b: false %v", mask, v.Kind)
		}
	}
	stuck := runner1(t, &toy{limit: 5, deadlockAt: 3}, 20)
	for _, mask := range []byte{0, 1, 2, 3} {
		if v := run1(t, stuck, []byte{mask}); v.Kind != Deadlock {
			t.Errorf("mask %02b: real deadlock reported as %v", mask, v.Kind)
		}
	}
}

func TestFuzzBytesFollowTrace(t *testing.T) {
	sys := &toy{limit: 6, failAt: 5}
	v := search1(t, sys, small).Violation
	got := run1(t, runner1(t, sys, 20), bytes1(t, v.Trace, sys))
	if got.Kind != Invariant || fmt.Sprint(got.Trace) != fmt.Sprint(v.Trace) {
		t.Errorf("fuzz replay of %v gave %v %v", v.Trace, got.Kind, got.Trace)
	}
}

// pairs is a Totality over a small hand-written table of outcomes.
type pairs struct {
	fail map[[2]int]string // "missing", "changed", "accept-zero"
}

func (p pairs) Machines() int { return 3 }
func (p pairs) Events() int   { return 3 }
func (p pairs) Name(m, e int) string {
	return fmt.Sprintf("m%d/e%d", m, e)
}
func (p pairs) Fire(m, e int) (fsm.Outcome, bool) {
	switch p.fail[[2]int{m, e}] {
	case "missing":
		return fsm.Invalid, true // a real pair the machine refuses as Invalid
	case "unset":
		var none fsm.Outcome // an outcome Fire never set
		return none, true
	case "changed":
		return fsm.Ignored, false
	case "accept-zero":
		return fsm.Rejected, true
	}
	if m == 0 || e == 0 {
		return fsm.Invalid, true
	}
	if e == 2 {
		return fsm.Ignored, true
	}
	return fsm.Moved, false
}

func TestTotal(t *testing.T) {
	cases := []struct {
		fail map[[2]int]string
		want string
	}{
		{nil, ""},
		{map[[2]int]string{{1, 2}: "missing"}, "m1/e2: refused as Invalid"},
		{map[[2]int]string{{2, 1}: "unset"}, "m2/e1: refused as Invalid"},
		{map[[2]int]string{{2, 2}: "changed"}, "m2/e2: quiet outcome (Ignored or Rejected), but the machine changed"},
		{map[[2]int]string{{0, 1}: "accept-zero"}, "m0/e1: the zero machine or zero event was not refused"},
	}
	for _, c := range cases {
		r := capture(t, func(tb testing.TB) { Total(tb, pairs{fail: c.fail}) })
		if (c.want == "") != (len(r.errs) == 0) || !r.has(r.errs, c.want) {
			t.Errorf("fail=%v: errs=%v, want %q", c.fail, r.errs, c.want)
		}
	}
}

// TestBadAdapterValuesAreErrors: a value from the adapter that merilo
// cannot use is an error at every entry point, never a panic.
func TestBadAdapterValuesAreErrors(t *testing.T) {
	for _, k := range []int{-1, maxKinds + 1} {
		sys := &toy{limit: 3, kinds: k}
		if _, err := runSearch(sys, small); err == nil {
			t.Errorf("Kinds() = %d accepted by the search", k)
		}
		if _, err := newRunner(sys, 3); err == nil {
			t.Errorf("Kinds() = %d accepted by the fuzzer", k)
		}
		if err := Replay(sys, Trace{0}); err == nil {
			t.Errorf("Kinds() = %d accepted by Replay", k)
		}
		if _, err := (Trace{0}).FuzzBytes(sys); err == nil {
			t.Errorf("Kinds() = %d accepted by FuzzBytes", k)
		}
	}
	for _, o := range []Options{{MaxStates: 0}, {MaxStates: maxStates + 1}} {
		if _, err := runSearch(&toy{limit: 3}, o); err == nil {
			t.Errorf("Options %+v accepted; the node table would be sized from it", o)
		}
	}
	if _, err := newRunner(&toy{limit: 3}, maxDepth+1); err == nil {
		t.Error("a fuzz depth past maxDepth was accepted")
	}
	if _, err := (Trace{-1}).FuzzBytes(&toy{limit: 3}); err == nil {
		t.Error("a negative trace kind was encoded")
	}
	if err := Replay(&toy{limit: 3}, Trace{1}); err == nil {
		t.Error("Replay took a kind the world does not enable")
	}
}

func TestOneLineEscapesControls(t *testing.T) { // item 7
	got := oneLine("a\tb\nc\rd\x1b[2Je\x00f")
	if got != `a b c\rd\x1b[2Je\x00f` {
		t.Errorf("oneLine = %q", got)
	}
}

func search1(t *testing.T, sys System, o Options) Result {
	t.Helper()
	r, err := runSearch(sys, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func runner1(t *testing.T, sys System, depth int) *runner {
	t.Helper()
	r, err := newRunner(sys, depth)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func run1(t *testing.T, r *runner, data []byte) Violation {
	t.Helper()
	v, err := r.run(data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func bytes1(t *testing.T, tr Trace, sys System) []byte {
	t.Helper()
	b, err := tr.FuzzBytes(sys)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// counter is an adapter that allocates nothing itself: its world is an int
// below 256, which Go boxes without allocating, and its strings are
// constants. Kind 0 counts by one and kind 1, at even values, by two.
type counter struct{ n, limit int }

var counterInvariants = []string{"nothing"}

func (c *counter) Reset()               { c.n = 0 }
func (c *counter) Kinds() int           { return 2 }
func (c *counter) Enabled(k int) bool   { return c.n < c.limit && (k == 0 || k == 1 && c.n%2 == 0) }
func (c *counter) World() any           { return c.n }
func (c *counter) Check() error         { return nil }
func (c *counter) Tuple() string        { return "counting" }
func (c *counter) Settled() bool        { return true }
func (c *counter) Describe(int) string  { return "step" }
func (c *counter) Invariants() []string { return counterInvariants }

func (c *counter) Apply(k int) error {
	c.n = min(c.n+1+k, c.limit)
	return nil
}

// TestStopsWhenFull: the search stops at the first world that
// does not fit, and Depth is the distance within which every world was
// stored. The counter reaches 2d-1 and 2d in d steps. Four worlds hold 3,
// two steps away, but not 4, so only depth 1 was searched in full.
func TestStopsWhenFull(t *testing.T) {
	r, err := runSearch(&counter{limit: 100}, Options{MaxStates: 4})
	if err != nil || r.Complete || r.States != 4 || r.Depth != 1 {
		t.Errorf("complete=%t states=%d depth=%d, %v; want an incomplete search of 4 worlds to depth 1", r.Complete, r.States, r.Depth, err)
	}
	r, err = runSearch(&counter{limit: 4}, Options{MaxStates: 5})
	if err != nil || !r.Complete || r.States != 5 || r.Depth != 2 {
		t.Errorf("complete=%t states=%d depth=%d, %v; want all 5 worlds, the farthest 2 steps away", r.Complete, r.States, r.Depth, err)
	}
}

// TestSeedLongerThanFuzzDepth: a seed the fuzz depth would cut short is
// refused, not truncated.
func TestSeedLongerThanFuzzDepth(t *testing.T) {
	s, err := newSearch(&toy{limit: 5}, small)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.run(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.witnesses(5); err != nil {
		t.Errorf("seeds of 5 steps refused at depth 5: %v", err)
	}
	if _, err := s.witnesses(4); err == nil || !strings.Contains(err.Error(), "more than FuzzOptions.MaxDepth 4") {
		t.Errorf("a seed of 5 steps accepted at depth 4: %v", err)
	}
}

// TestSearchAllocatesOnlyAtSetup: under the same
// bounds, ten times as many worlds cost no more allocations, so the search
// allocates nothing per world after newSearch.
func TestSearchAllocatesOnlyAtSetup(t *testing.T) {
	o := Options{MaxStates: 1000}
	allocs := func(limit int) float64 {
		return testing.AllocsPerRun(5, func() {
			r, err := runSearch(&counter{limit: limit}, o)
			if err != nil || r.States != limit+1 {
				t.Fatalf("limit %d: %d states, %v", limit, r.States, err)
			}
		})
	}
	if small, large := allocs(20), allocs(200); small != large {
		t.Errorf("a search of 21 worlds allocates %v times and one of 201 worlds %v times", small, large)
	}
}

// TestFuzzInputAllocatesNothing: once newRunner
// has run, an input allocates nothing.
func TestFuzzInputAllocatesNothing(t *testing.T) {
	r := runner1(t, &counter{limit: 200}, 100)
	data := []byte{0, 1, 0, 1, 1, 0, 0, 1, 1, 1, 0}
	if n := testing.AllocsPerRun(100, func() { run1(t, r, data) }); n != 0 {
		t.Errorf("a fuzz input allocates %v times", n)
	}
}

// TestTotalAllocatesNothing: on a table that passes, it
// allocates nothing.
func TestTotalAllocatesNothing(t *testing.T) {
	r := &rec{TB: t}
	if n := testing.AllocsPerRun(10, func() { Total(r, pairs{}) }); n != 0 || len(r.errs) != 0 {
		t.Errorf("Total allocates %v times; errors %v", n, r.errs)
	}
}

// reentrant is a toy whose Apply calls back into merilo, which merilo
// itself is running: recursion through adapter code (Rule 1).
type reentrant struct {
	toy
	via string // "replay" or "explore"
	tb  testing.TB
}

func (s *reentrant) Apply(k int) error {
	if err := s.toy.Apply(k); err != nil {
		return err
	}
	inner := &toy{limit: 2}
	switch s.via {
	case "replay":
		return Replay(inner, Trace{0})
	case "explore":
		Explore(s.tb, inner, small)
	}
	return nil
}

// reentrantTable is a Totality whose Fire calls Total.
type reentrantTable struct {
	pairs
	tb testing.TB
}

func (p reentrantTable) Fire(m, e int) (fsm.Outcome, bool) {
	Total(p.tb, pairs{})
	return p.pairs.Fire(m, e)
}

// TestReentryRefused: an adapter that calls merilo from inside a run is
// refused, not followed, whichever entry point it calls back into. The
// same calls made from the test itself are not re-entry.
func TestReentryRefused(t *testing.T) {
	if err := Replay(&reentrant{toy: toy{limit: 3}, via: "replay"}, Trace{0}); err == nil || !strings.Contains(err.Error(), "called again from inside a run") {
		t.Errorf("Replay re-entered from Apply: err = %v", err)
	}
	r := capture(t, func(tb testing.TB) {
		Explore(tb, &reentrant{toy: toy{limit: 3}, via: "explore", tb: tb}, small)
	})
	if !r.has(r.errs, "called again from inside a run") {
		t.Errorf("Explore re-entered from Apply: errs = %v", r.errs)
	}
	r = capture(t, func(tb testing.TB) { Total(tb, reentrantTable{tb: tb}) })
	if !r.has(r.errs, "called again from inside a run") {
		t.Errorf("Total re-entered from Fire: errs = %v", r.errs)
	}
	if err := Replay(&toy{limit: 3}, Trace{0, 0}); err != nil {
		t.Errorf("a plain Replay from a test: %v", err)
	}
}
