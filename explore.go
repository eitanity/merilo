package merilo

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// node is one world in the visited set, reached from parent by the step
// at position pos.
type node struct {
	parent int // -1 for the initial world
	pos    int
	depth  int
	key    string
}

// edge records where a mode change was first seen: the step at pos from
// the world parent.
type edge struct {
	parent int
	pos    int
}

type search struct {
	d      *driver
	o      Options
	nodes  []node
	seen   map[string]int
	tuples map[string]int
	modes  map[string]int
	edges  map[[2]string]edge
	succ   []Step
	key    []byte
	trunc  bool
	res    Result
}

// Explore runs a bounded breadth-first search of sys, logs a summary
// line, and fails t on a violation or, with RequireComplete, on truncation.
func Explore(t testing.TB, sys System, o Options) Result {
	t.Helper()
	if err := validate(sys, o); err != nil {
		t.Fatalf("%v", err)
	}
	assert(o.MaxDepth > 0 && o.MaxStates > 0, "validated options out of range")
	r := runSearch(sys, o)
	assert(r.States <= o.MaxStates, "more worlds than MaxStates")
	assert(!r.Complete || (r.Cut == 0 && !r.Violation.Found()), "complete result with a cut or violation")
	t.Logf("%s", summary(sys, r))
	if r.Violation.Found() {
		report(t, sys, o.Regressions, r.Violation)
	}
	if o.RequireComplete && !r.Complete {
		t.Errorf("merilo: search incomplete with RequireComplete: states=%d depth=%d cut=%d", r.States, r.Depth, r.Cut)
	}
	return r
}

func validate(sys System, o Options) error {
	switch {
	case sys == nil:
		return fmt.Errorf("merilo: nil System")
	case o.MaxDepth <= 0:
		return fmt.Errorf("merilo: Options.MaxDepth must be set; it has no default")
	case o.MaxStates <= 0:
		return fmt.Errorf("merilo: Options.MaxStates must be set; it has no default")
	case sys.Kinds() <= 0:
		return fmt.Errorf("merilo: Kinds() must be positive")
	case len(sys.Invariants()) == 0:
		return fmt.Errorf("merilo: refusing to explore: Invariants() is empty, so a pass would prove nothing")
	}
	assert(o.MaxDepth > 0 && o.MaxStates > 0, "validated options out of range")
	return nil
}

func summary(sys System, r Result) string {
	names := sys.Invariants()
	assert(len(names) > 0, "summary without invariants")
	assert(r.States >= 0 && r.Cut >= 0, "negative counts")
	return fmt.Sprintf("merilo: complete=%t states=%d depth=%d cut=%d modes=%d modeEdges=%d invariants=%d [%s]",
		r.Complete, r.States, r.Depth, r.Cut, len(r.Modes), len(r.ModeEdges), len(names), strings.Join(names, ", "))
}

// runSearch is the search without a testing.TB, shared with CheckDeterminism.
func runSearch(sys System, o Options) Result {
	assert(o.MaxDepth > 0 && o.MaxStates > 0, "unvalidated options")
	s := &search{
		d: newDriver(sys), o: o,
		seen: map[string]int{}, tuples: map[string]int{}, modes: map[string]int{}, edges: map[[2]string]edge{},
	}
	sys.Reset()
	if a := s.d.check(); a.kind != kindNone {
		s.res.Violation = Violation{Kind: a.kind, Trace: Trace{}, Detail: a.detail}
		return s.finish()
	}
	s.discover(-1, 0, 0)
	assert(len(s.nodes) == 1 || s.res.Violation.Found(), "initial world not recorded")
	for i := 0; i < len(s.nodes) && !s.res.Violation.Found(); i++ {
		s.expand(i)
	}
	return s.finish()
}

// discover records the live world, reached from parent by pos, if new.
func (s *search) discover(parent, pos, depth int) {
	s.key = s.d.sys.Key(s.key[:0])
	if _, ok := s.seen[string(s.key)]; ok {
		return
	}
	if len(s.nodes) >= s.o.MaxStates {
		s.trunc = true
		s.res.Cut++
		return
	}
	i := len(s.nodes)
	assert(parent < i, "parent recorded after child")
	assert(depth <= s.o.MaxDepth, "world beyond MaxDepth")
	s.nodes = append(s.nodes, node{parent: parent, pos: pos, depth: depth, key: string(s.key)})
	s.seen[string(s.key)] = i
	assert(len(s.seen) == len(s.nodes), "visited set out of step with nodes")
	if _, ok := s.tuples[s.d.sys.Tuple()]; !ok {
		s.tuples[s.d.sys.Tuple()] = i
	}
	if _, ok := s.modes[s.d.sys.Mode().String()]; !ok {
		s.modes[s.d.sys.Mode().String()] = i
	}
	if s.d.stuck() {
		s.res.Violation = Violation{Kind: Deadlock, Trace: s.path(i),
			Detail: "no step is enabled and Settled() is false"}
	}
}

// expand replays world i once per successor and discovers each one.
func (s *search) expand(i int) {
	n := s.nodes[i]
	if n.depth >= s.o.MaxDepth {
		s.res.Cut++
		return
	}
	tr := s.path(i)
	assert(len(tr) == n.depth, "path length differs from depth")
	if !s.reach(i, tr) {
		return
	}
	s.succ = append(s.succ[:0], s.d.enabled()...)
	assert(len(s.succ) > 0 || !s.d.stuck(), "a stuck world was expanded")
	from := s.d.sys.Mode().String()
	for j := 0; j < len(s.succ) && !s.res.Violation.Found(); j++ {
		if j > 0 && !s.reach(i, tr) {
			return
		}
		if a := s.d.apply(s.succ[j]); a.kind != kindNone {
			s.res.Violation = Violation{Kind: a.kind, Trace: append(tr[:len(tr):len(tr)], j), Detail: a.detail}
			return
		}
		if a := s.d.check(); a.kind != kindNone {
			s.res.Violation = Violation{Kind: a.kind, Trace: append(tr[:len(tr):len(tr)], j), Detail: a.detail}
			return
		}
		if to := s.d.sys.Mode().String(); to != from {
			if _, ok := s.edges[[2]string{from, to}]; !ok {
				s.edges[[2]string{from, to}] = edge{parent: i, pos: j}
			}
		}
		s.discover(i, j, n.depth+1)
	}
}

// reach replays tr and confirms it lands on world i (EXP-05). A failure
// or a different key is a Nondeterminism violation.
func (s *search) reach(i int, tr Trace) bool {
	assert(s.nodes[i].depth == len(tr), "path length differs from world depth")
	assert(!s.res.Violation.Found(), "replay after a violation")
	a := s.d.replay(tr)
	if a.kind == kindNone {
		s.key = s.d.sys.Key(s.key[:0])
		if string(s.key) == s.nodes[i].key {
			return true
		}
		a.detail = "replaying the same path reached a different Key"
	} else {
		a.detail = "replaying a path that once passed now fails: " + a.kind.String() + ": " + a.detail
	}
	s.res.Violation = Violation{Kind: Nondeterminism, Trace: tr, Detail: a.detail}
	return false
}

// path returns the steps from the initial world to world i.
func (s *search) path(i int) Trace {
	assert(i >= 0 && i < len(s.nodes), "path to an unknown world")
	tr := make(Trace, s.nodes[i].depth)
	for k := len(tr) - 1; k >= 0; k-- {
		tr[k] = s.nodes[i].pos
		i = s.nodes[i].parent
	}
	assert(s.nodes[i].parent == -1, "path did not end at the initial world")
	return tr
}

// finish fills the Result from the visited set, in sorted order (DET-02).
func (s *search) finish() Result {
	r := s.res
	assert(len(s.seen) == len(s.nodes), "visited set out of step with nodes")
	r.States = len(s.nodes)
	for _, n := range s.nodes {
		if n.depth > r.Depth {
			r.Depth = n.depth
		}
	}
	r.Complete = !s.trunc && r.Cut == 0 && !r.Violation.Found()
	r.Tuples = sortedKeys(s.tuples)
	for _, t := range r.Tuples {
		r.Witnesses = append(r.Witnesses, s.path(s.tuples[t]))
	}
	r.Modes = sortedKeys(s.modes)
	pairs := make([]string, 0, len(s.edges))
	for e := range s.edges {
		pairs = append(pairs, e[0]+"\x00"+e[1])
	}
	sort.Strings(pairs)
	assert(len(pairs) == len(s.edges), "lost a mode edge")
	for _, p := range pairs {
		from, to, ok := strings.Cut(p, "\x00")
		assert(ok, "mode edge without separator")
		e := s.edges[[2]string{from, to}]
		r.ModeEdges = append(r.ModeEdges, [2]string{from, to})
		r.Witnesses = append(r.Witnesses, append(s.path(e.parent), e.pos))
	}
	return r
}

func sortedKeys(m map[string]int) []string {
	assert(m != nil, "sortedKeys of a nil map")
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert(len(keys) == len(m), "lost a key")
	return keys
}
