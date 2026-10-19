package merilo

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// toy is a counter from 0 to limit with switchable faults. Positions
// stored in the fault fields are counter values; 0 means off.
type toy struct {
	n, limit   int
	deadlockAt int    // no step is enabled at this value
	settledAt  int    // Settled is also true at this value
	failAt     int    // Check fails at this value
	errAt      int    // Apply returns an error reaching this value
	panicAt    int    // Apply panics reaching this value
	modeErrAt  int    // Apply returns ErrModeChange reaching this value
	highAt     int    // mode is "high" from this value on
	badKind    bool   // Steps offers a kind beyond Kinds()
	twoWays    bool   // two steps per world, offered in map order
	impure     *int   // a counter outside the path that Apply reads
	noInv      bool   // Invariants is empty
	label      string // Describe prefix
}

type toyMode string

func (m toyMode) String() string { return string(m) }

func (s *toy) Reset()     { s.n = 0 }
func (s *toy) Kinds() int { return 2 }

func (s *toy) Invariants() []string {
	if s.noInv {
		return nil
	}
	return []string{"n is never failAt"}
}

func (s *toy) Steps(buf []Step) []Step {
	if s.n == s.deadlockAt && s.n != 0 || s.n >= s.limit {
		return buf
	}
	if s.badKind {
		return append(buf, Step{Kind: 7})
	}
	if !s.twoWays {
		return append(buf, Step{Kind: 0})
	}
	for _, st := range map[int]Step{0: {Kind: 0}, 1: {Kind: 1, Arg: 2}} {
		buf = append(buf, st)
	}
	return buf
}

func (s *toy) Apply(st Step) error {
	s.n += 1 + st.Arg
	if s.impure != nil {
		*s.impure++
		s.n += *s.impure % 2
	}
	switch s.n {
	case s.errAt:
		return errors.New("overflow")
	case s.panicAt:
		panic("toy panic")
	case s.modeErrAt:
		return fmt.Errorf("%w: low -> high", ErrModeChange)
	}
	return nil
}

func (s *toy) Key(buf []byte) []byte { return append(buf, byte(s.n)) }

func (s *toy) Check() error {
	if s.failAt != 0 && s.n == s.failAt {
		return errors.New("n is never failAt")
	}
	return nil
}

func (s *toy) Mode() fmt.Stringer {
	if s.highAt != 0 && s.n >= s.highAt {
		return toyMode("high")
	}
	return toyMode("low")
}

func (s *toy) Tuple() string { return fmt.Sprint(s.n) }
func (s *toy) Settled() bool { return s.n >= s.limit || (s.settledAt != 0 && s.n == s.settledAt) }
func (s *toy) Describe(st Step) string {
	return fmt.Sprintf("%sstep %d+%d from %d", s.label, st.Kind, st.Arg, s.n)
}

var _ System = (*toy)(nil)

// rec is a testing.TB that records instead of failing.
type rec struct {
	testing.TB
	errs, logs []string
	fatal      bool
}

type fatalStop struct{}

func (r *rec) Helper()                        {}
func (r *rec) Errorf(format string, a ...any) { r.errs = append(r.errs, fmt.Sprintf(format, a...)) }
func (r *rec) Logf(format string, a ...any)   { r.logs = append(r.logs, fmt.Sprintf(format, a...)) }
func (r *rec) Fatalf(format string, a ...any) {
	r.Errorf(format, a...)
	r.fatal = true
	panic(fatalStop{})
}
func (r *rec) has(list []string, s string) bool { return strings.Contains(strings.Join(list, "\n"), s) }

func capture(t *testing.T, fn func(tb testing.TB)) *rec {
	r := &rec{TB: t}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if _, ok := v.(fatalStop); !ok {
					panic(v)
				}
			}
		}()
		fn(r)
	}()
	return r
}

var small = Options{MaxDepth: 50, MaxStates: 1000}

func TestViolationKinds(t *testing.T) { // AC-21, AC-22, AC-23
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
		{"undeclared kind", &toy{limit: 5, badKind: true}, ApplyError, 1},
		{"panic", &toy{limit: 5, panicAt: 3}, Panic, 3},
		{"mode change", &toy{limit: 5, modeErrAt: 2}, ModeChange, 2},
		{"nondeterminism", &toy{limit: 5, impure: new(int)}, Nondeterminism, -1},
	}
	for _, c := range cases {
		r := runSearch(c.sys, small)
		if r.Violation.Kind != c.want || (c.len >= 0 && len(r.Violation.Trace) != c.len) {
			t.Errorf("%s: got %v with trace %v (%s), want %v of length %d",
				c.name, r.Violation.Kind, r.Violation.Trace, r.Violation.Detail, c.want, c.len)
		}
	}
}

func TestRefusesEmptyInvariants(t *testing.T) { // AC-24
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

func TestRequireComplete(t *testing.T) { // AC-25
	o := Options{MaxDepth: 50, MaxStates: 4, RequireComplete: true}
	r := capture(t, func(tb testing.TB) { Explore(tb, &toy{limit: 10}, o) })
	if !r.has(r.errs, "search incomplete") || !r.has(r.errs, "states=4") {
		t.Errorf("truncation not reported: %v", r.errs)
	}
	o.RequireComplete = false
	var res Result
	r = capture(t, func(tb testing.TB) { res = Explore(tb, &toy{limit: 10}, o) })
	if len(r.errs) != 0 || res.Complete || res.Cut == 0 {
		t.Errorf("without RequireComplete: errs=%v complete=%t cut=%d", r.errs, res.Complete, res.Cut)
	}
}

func TestDigestStable(t *testing.T) { // AC-08
	sys := &toy{limit: 20, highAt: 7}
	want := runSearch(sys, small).Digest()
	for range 100 {
		if runSearch(sys, small).Digest() != want {
			t.Fatal("digest changed between runs")
		}
	}
	for _, p := range []int{1, 8} {
		prev := runtime.GOMAXPROCS(p)
		got := runSearch(sys, small).Digest()
		runtime.GOMAXPROCS(prev)
		if got != want {
			t.Fatalf("digest differs at GOMAXPROCS %d", p)
		}
	}
}

func TestCheckDeterminism(t *testing.T) { // AC-27
	r := capture(t, func(tb testing.TB) { CheckDeterminism(tb, &toy{limit: 30, twoWays: true}, small) })
	if !r.has(r.errs, "differ") {
		t.Errorf("map-ordered Steps not caught: %v", r.errs)
	}
	r = capture(t, func(tb testing.TB) { CheckDeterminism(tb, &toy{limit: 30}, small) })
	if len(r.errs) != 0 {
		t.Errorf("deterministic system flagged: %v", r.errs)
	}
}

func TestMaskNeverDeadlocks(t *testing.T) { // AC-17
	ok := newRunner(&toy{limit: 5}, 20)
	for _, mask := range []byte{0, 1, 2, 3} {
		if v := ok.run([]byte{mask, 0, 0}, nil); v.Found() {
			t.Errorf("mask %02b: false %v", mask, v.Kind)
		}
	}
	stuck := newRunner(&toy{limit: 5, deadlockAt: 3}, 20)
	for _, mask := range []byte{0, 1, 2, 3} {
		if v := stuck.run([]byte{mask}, nil); v.Kind != Deadlock {
			t.Errorf("mask %02b: real deadlock reported as %v", mask, v.Kind)
		}
	}
}

func TestFuzzBytesFollowTrace(t *testing.T) { // TR-01
	sys := &toy{limit: 6, failAt: 5}
	v := runSearch(sys, small).Violation
	got := newRunner(sys, 20).run(v.Trace.FuzzBytes(sys), nil)
	if got.Kind != Invariant || fmt.Sprint(got.Trace) != fmt.Sprint(v.Trace) {
		t.Errorf("fuzz replay of %v gave %v %v", v.Trace, got.Kind, got.Trace)
	}
}

func TestRegressions(t *testing.T) { // AC-20
	chdir(t, t.TempDir())
	o := small
	o.Regressions = "toy"
	r := capture(t, func(tb testing.TB) { Explore(tb, &toy{limit: 6, failAt: 4}, o) })
	if !r.has(r.logs, "counterexample written") {
		t.Fatalf("no regression written: %v %v", r.errs, r.logs)
	}
	if r := capture(t, func(tb testing.TB) { Regress(tb, &toy{limit: 6, failAt: 4}, "toy") }); !r.has(r.errs, "the bug is back") {
		t.Errorf("unfixed bug passed its regression: %v", r.errs)
	}
	if r := capture(t, func(tb testing.TB) { Regress(tb, &toy{limit: 6}, "toy") }); len(r.errs) != 0 {
		t.Errorf("fixed bug failed its regression: %v", r.errs)
	}
	if r := capture(t, func(tb testing.TB) { Regress(tb, &toy{limit: 6, label: "v2 "}, "toy") }); !r.has(r.errs, "no longer fits") {
		t.Errorf("shifted steps not detected: %v", r.errs)
	}
	if r := capture(t, func(tb testing.TB) { Regress(tb, &toy{limit: 2}, "toy") }); !r.has(r.errs, "no longer fits") {
		t.Errorf("missing step not detected: %v", r.errs)
	}
}

func TestRegressionReadOnly(t *testing.T) { // TR-05
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.Mkdir("testdata", 0o555); err != nil {
		t.Fatal(err)
	}
	o := small
	o.Regressions = "toy"
	r := capture(t, func(tb testing.TB) { Explore(tb, &toy{limit: 6, failAt: 4}, o) })
	if os.Getuid() != 0 && !r.has(r.logs, "its contents are:\n"+traceHeader) {
		t.Errorf("unwritable regression not logged in full: %v", r.logs)
	}
}

func TestCoverageNamesMissingEdge(t *testing.T) { // AC-26
	chdir(t, t.TempDir())
	sys := &toy{limit: 6, highAt: 4}
	explored := runSearch(sys, small)
	r := capture(t, func(tb testing.TB) {
		Coverage(tb, sys, "FuzzToy", explored, FuzzOptions{MaxDepth: 2},
			CoverageMin{Modes: 100, ModeEdges: 100, Tuples: 1})
	})
	if !r.has(r.errs, "low -> high") {
		t.Errorf("missing edge not named: %v", r.errs)
	}
	r = capture(t, func(tb testing.TB) {
		Coverage(tb, sys, "FuzzToy", explored, FuzzOptions{MaxDepth: 20, Seeds: explored.Witnesses},
			CoverageMin{Modes: 100, ModeEdges: 100, Tuples: 100})
	})
	if len(r.errs) != 0 {
		t.Errorf("seeds alone should reach everything: %v", r.errs)
	}
}

// pairs is a Totality over a small hand-written table of outcomes.
type pairs struct {
	fail map[[2]int]string // "panic", "changed", "accept-zero"
}

func (p pairs) Machines() int { return 3 }
func (p pairs) Events() int   { return 3 }
func (p pairs) Name(m, e int) string {
	return fmt.Sprintf("m%d/e%d", m, e)
}
func (p pairs) Fire(m, e int) (quiet, unchanged bool) {
	switch p.fail[[2]int{m, e}] {
	case "panic":
		panic("undeclared event")
	case "changed":
		return true, false
	case "accept-zero":
		return true, true
	}
	if m == 0 || e == 0 {
		panic("zero value")
	}
	return e == 2, true
}

func TestTotal(t *testing.T) { // AC-07, AC-18
	cases := []struct {
		fail map[[2]int]string
		want string
	}{
		{nil, ""},
		{map[[2]int]string{{1, 2}: "panic"}, "m1/e2: panicked"},
		{map[[2]int]string{{2, 2}: "changed"}, "m2/e2: Ignored or Rejected, but the machine changed"},
		{map[[2]int]string{{0, 1}: "accept-zero"}, "m0/e1: the zero machine or zero event was accepted"},
	}
	for _, c := range cases {
		r := capture(t, func(tb testing.TB) { Total(tb, pairs{fail: c.fail}) })
		if (c.want == "") != (len(r.errs) == 0) || !r.has(r.errs, c.want) {
			t.Errorf("fail=%v: errs=%v, want %q", c.fail, r.errs, c.want)
		}
	}
}

// chdir changes directory for one test; the module targets Go 1.22, which
// lacks testing.T.Chdir.
func chdir(t *testing.T, dir string) {
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
	})
}
