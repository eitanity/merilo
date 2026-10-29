package merilo

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// maxKinds is the most step kinds a System may declare.
const maxKinds = 1 << 16

// maxStates caps Options and maxDepth FuzzOptions: the explorer sizes its
// node table, visited map and path buffers from MaxStates when it starts,
// and a fuzz runner its schedule from MaxDepth, so a larger value is
// refused rather than left to fail inside make.
const (
	maxStates = 1 << 26
	maxDepth  = 1 << 20
)

// node is one world in the visited set, reached from parent by a step of
// the given kind.
type node struct {
	parent int // -1 for the initial world
	kind   int
	depth  int
	world  any
}

// search is one bounded breadth-first search. newSearch allocates all of
// its storage, sized from the Options; the search itself allocates nothing.
type search struct {
	d      driver
	o      Options
	nodes  []node
	seen   map[any]int // World() values, compared by Go's ==
	tuples map[string]int
	names  []string // the tuples, sorted, for the Result
	cur    Trace    // the path to the world being expanded
	viol   Trace    // the violation's trace
	full   bool     // a new world did not fit, so the search stopped
	res    Result
}

// Explore runs a bounded breadth-first search of sys, logs a summary
// line, and fails t on a violation or, with RequireComplete, on truncation.
func Explore(t testing.TB, sys System, o Options) Result {
	if t == nil {
		return Result{}
	}
	t.Helper()
	if sys == nil {
		t.Fatalf("merilo: nil System")
	}
	r, _ := explore(t, sys, o)
	return r
}

// explore is Explore, also returning the search for Fuzz to seed from;
// the search is nil when it could not run.
func explore(t testing.TB, sys System, o Options) (Result, *search) {
	if t == nil {
		return Result{}, nil
	}
	t.Helper()
	if sys == nil {
		t.Fatalf("merilo: nil System")
	}
	s, err := newSearch(sys, o)
	if err != nil {
		t.Fatalf("%v", err)
	}
	r, err := s.run()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Logf("%s", summary(sys, r))
	if r.Violation.Found() {
		report(t, sys, r.Violation)
	}
	if o.RequireComplete && !r.Complete {
		t.Errorf("merilo: search incomplete with RequireComplete: states=%d depth=%d", r.States, r.Depth)
	}
	return r, s
}

func validate(sys System, o Options) error {
	if sys == nil {
		return errors.New("merilo: nil System")
	}
	inv := sys.Invariants()
	switch {
	case o.MaxStates <= 0 || o.MaxStates > maxStates:
		return fmt.Errorf("merilo: Options.MaxStates is %d; it must be set, from 1 to %d, and has no default", o.MaxStates, maxStates)
	case len(inv) == 0:
		return errors.New("merilo: refusing to explore: Invariants() is empty, so a pass would prove nothing")
	}
	for i, name := range inv {
		if blank := strings.TrimSpace(name) == ""; blank {
			return fmt.Errorf("merilo: invariant %d has no name", i)
		}
	}
	return nil
}

func summary(sys System, r Result) string {
	if sys == nil {
		return "merilo: no System"
	}
	inv := sys.Invariants()
	names := make([]string, 0, len(inv)) // a copy: the adapter's own slice is not modified
	for _, n := range inv {
		names = append(names, oneLine(n))
	}
	return fmt.Sprintf("merilo: complete=%t states=%d depth=%d invariants=%d [%s]",
		r.Complete, r.States, r.Depth, len(names), strings.Join(names, ", "))
}

// runSearch is the search without a testing.TB.
func runSearch(sys System, o Options) (Result, error) {
	if sys == nil {
		return Result{}, errors.New("merilo: nil System")
	}
	s, err := newSearch(sys, o)
	if err != nil {
		return Result{}, err
	}
	return s.run()
}

// newSearch validates the bound and allocates every table the search
// uses, sized from it: Rule 3 allows no allocation after this. No path is
// longer than the worlds stored, so MaxStates also sizes the paths.
func newSearch(sys System, o Options) (*search, error) {
	if sys == nil {
		return nil, errors.New("merilo: nil System")
	}
	if err := validate(sys, o); err != nil {
		return nil, err
	}
	d, err := newDriver(sys)
	if err != nil {
		return nil, err
	}
	return &search{d: d, o: o,
		nodes:  make([]node, 0, o.MaxStates),
		seen:   make(map[any]int, o.MaxStates),
		tuples: make(map[string]int, o.MaxStates),
		names:  make([]string, 0, o.MaxStates),
		cur:    make(Trace, 0, o.MaxStates),
		viol:   make(Trace, 0, o.MaxStates+1),
	}, nil
}

// run searches from the initial world until every world is checked, a
// new world does not fit, or a violation.
func (s *search) run() (Result, error) {
	if s == nil || s.d.sys == nil {
		return Result{}, fault("run without a search")
	}
	s.d.sys.Reset()
	if a := s.d.check(); a.kind != kindNone {
		s.res.Violation = Violation{Kind: a.kind, Trace: s.viol[:0], Detail: a.detail}
		return s.finish()
	}
	if err := s.discover(-1, 0, 0); err != nil {
		return Result{}, err
	}
	limit := s.o.MaxStates
	for i := 0; i < limit && i < len(s.nodes) && !s.full && !s.res.Violation.Found(); i++ {
		if err := s.expand(i); err != nil {
			return Result{}, err
		}
		if s.full {
			// Breadth first, every world nearer than i's successors was
			// stored before i was expanded, and so was checked.
			s.res.Depth = s.nodes[i].depth
		}
	}
	return s.finish()
}

// violate records the first violation, its trace copied into the buffer
// reserved for it: tr, then step kind k when k is not negative.
func (s *search) violate(kind ViolationKind, tr Trace, k int, detail string) error {
	if s == nil {
		return fault("violate without a search")
	}
	if len(tr) >= cap(s.viol) {
		return fault("a violation's trace of %d steps exceeds MaxStates", len(tr)+1)
	}
	s.viol = append(s.viol[:0], tr...)
	if k >= 0 {
		s.viol = append(s.viol, k)
	}
	s.res.Violation = Violation{Kind: kind, Trace: s.viol, Detail: detail}
	return nil
}

// discover records the live world, reached from parent by a step of kind
// k, if new.
func (s *search) discover(parent, k, depth int) error {
	if s == nil {
		return fault("discover without a search")
	}
	w := s.d.sys.World()
	if _, ok := s.seen[w]; ok {
		return nil
	}
	if len(s.nodes) >= s.o.MaxStates {
		s.full = true
		return nil
	}
	i := len(s.nodes)
	s.nodes = append(s.nodes, node{parent: parent, kind: k, depth: depth, world: w})
	s.seen[w] = i
	if len(s.seen) != len(s.nodes) {
		return fault("visited set holds %d worlds for %d nodes", len(s.seen), len(s.nodes))
	}
	if _, ok := s.tuples[s.d.sys.Tuple()]; !ok {
		s.tuples[s.d.sys.Tuple()] = i
	}
	if stuck := s.d.stuck(); !stuck {
		return nil
	}
	if settled := s.d.sys.Settled(); !settled {
		tr, err := s.path(i, s.viol)
		if err != nil {
			return err
		}
		s.res.Violation = Violation{Kind: Deadlock, Trace: tr, Detail: "no step is enabled and Settled() is false"}
	}
	return nil
}

// expand takes every enabled step kind from world i, replaying the path to
// it before each, and discovers each world reached.
func (s *search) expand(i int) error {
	if s == nil {
		return fault("expand without a search")
	}
	if i < 0 || i >= len(s.nodes) {
		return fault("expand world %d of %d", i, len(s.nodes))
	}
	tr, err := s.path(i, s.cur)
	if err != nil {
		return err
	}
	if ok, err := s.reach(i, tr); !ok || err != nil {
		return err
	}
	kinds, moved := s.d.kinds, false
	for k := 0; k < kinds && !s.full && !s.res.Violation.Found(); k++ {
		if moved {
			if ok, err := s.reach(i, tr); !ok || err != nil {
				return err
			}
		}
		if enabled := s.d.enabled(k); !enabled {
			moved = false
			continue
		}
		moved = true
		if err := s.step(i, tr, k); err != nil {
			return err
		}
	}
	return nil
}

// step takes kind k from world i, reached by tr, and discovers the world
// it leads to unless it breaks a rule.
func (s *search) step(i int, tr Trace, k int) error {
	if s == nil {
		return fault("step without a search")
	}
	if i < 0 || i >= len(s.nodes) {
		return fault("step from world %d of %d", i, len(s.nodes))
	}
	if a := s.d.apply(k); a.kind != kindNone {
		return s.violate(a.kind, tr, k, a.detail)
	}
	if a := s.d.check(); a.kind != kindNone {
		return s.violate(a.kind, tr, k, a.detail)
	}
	return s.discover(i, k, s.nodes[i].depth+1)
}

// reach replays tr and confirms it lands on world i. A failure
// or a different key is a Nondeterminism violation, and reach reports
// false.
func (s *search) reach(i int, tr Trace) (bool, error) {
	if s == nil {
		return false, fault("reach without a search")
	}
	if i < 0 || i >= len(s.nodes) {
		return false, fault("reach world %d of %d", i, len(s.nodes))
	}
	if s.nodes[i].depth != len(tr) {
		return false, fault("path of %d steps to a world at depth %d", len(tr), s.nodes[i].depth)
	}
	if found := s.res.Violation.Found(); found {
		return false, fault("replay after a violation was recorded")
	}
	if a := s.d.replay(tr); a.kind != kindNone {
		return false, s.violate(Nondeterminism, tr, -1,
			"replaying a path that once passed now fails: "+a.kind.String()+": "+a.detail)
	}
	if w := s.d.sys.World(); w != s.nodes[i].world {
		return false, s.violate(Nondeterminism, tr, -1, "replaying the same path reached a different World")
	}
	return true, nil
}

// path writes the steps from the initial world to world i into buf, which
// holds MaxStates steps, and returns them.
func (s *search) path(i int, buf Trace) (Trace, error) {
	if s == nil {
		return nil, fault("path without a search")
	}
	if i < 0 || i >= len(s.nodes) {
		return nil, fault("path to world %d of %d", i, len(s.nodes))
	}
	if depth := s.nodes[i].depth; depth > cap(buf) {
		return nil, fault("path of %d steps does not fit its buffer of %d", depth, cap(buf))
	}
	tr := buf[:s.nodes[i].depth]
	for k := len(tr) - 1; k >= 0; k-- {
		if i < 0 || i >= len(s.nodes) {
			return nil, fault("path step %d reaches world %d of %d", k, i, len(s.nodes))
		}
		tr[k] = s.nodes[i].kind
		i = s.nodes[i].parent
	}
	if i < 0 || i >= len(s.nodes) || s.nodes[i].parent != -1 {
		return nil, fault("path to world %d does not start at the initial world", i)
	}
	return tr, nil
}

// finish fills the Result from the visited set, in sorted order.
func (s *search) finish() (Result, error) {
	if s == nil {
		return Result{}, fault("finish without a search")
	}
	r := s.res
	r.States = len(s.nodes)
	if !s.full {
		for _, n := range s.nodes {
			if n.depth > r.Depth {
				r.Depth = n.depth
			}
		}
	}
	r.Complete = !s.full && !r.Violation.Found()
	if len(s.tuples) > cap(s.names) {
		return Result{}, fault("%d tuples for %d worlds", len(s.tuples), cap(s.names))
	}
	names := s.names[:0]
	for t := range s.tuples {
		names = append(names, t)
	}
	sort.Strings(names)
	r.Tuples = names
	s.res = r
	return r, nil
}

// witnesses encodes, as fuzz inputs, the shortest path to each tuple the
// search reached, in tuple order: Fuzz's seeds. It runs when Fuzz sets up,
// and refuses a path longer than depth, the steps a fuzz input may take.
func (s *search) witnesses(depth int) ([][]byte, error) {
	if s == nil {
		return nil, fault("witnesses without a search")
	}
	out := make([][]byte, 0, len(s.res.Tuples))
	for _, t := range s.res.Tuples {
		tr, err := s.path(s.tuples[t], s.cur)
		if err != nil {
			return nil, err
		}
		if len(tr) > depth {
			return nil, fmt.Errorf("merilo: the seed for %s takes %d steps, more than FuzzOptions.MaxDepth %d", t, len(tr), depth)
		}
		b, err := tr.FuzzBytes(s.d.sys)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
