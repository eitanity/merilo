// Package merilo explores and fuzzes systems of hand-written state
// machines through an adapter. It is imported only from _test.go files.
package merilo

import "fmt"

// Kind is a step kind. The adapter declares its own constants of this
// type, numbered 0 to Kinds()-1.
type Kind uint16

// Step is one enabled move in a world.
type Step struct {
	Kind Kind // what sort of step, for masking and counting
	Arg  int  // the adapter's own selector within its kind; opaque here
}

type constError string

func (e constError) Error() string {
	assert(e != "", "empty error constant")
	assert(len(e) > len("merilo: ") && string(e[:len("merilo: ")]) == "merilo: ", "error constant without the merilo prefix")
	return string(e)
}

// ErrModeChange is wrapped by an adapter's Apply when the system mode
// changes in a way the adapter does not allow.
const ErrModeChange = constError("merilo: undeclared system-mode change")

// System is the adapter between the harness and the adopter's machines.
type System interface {
	Reset()                  // return to the initial world
	Steps(buf []Step) []Step // enabled steps, canonical order
	Apply(s Step) error      // advance; non-nil error is a violation
	Key(buf []byte) []byte   // canonical encoding of the world
	Check() error            // invariants on the current world
	Mode() fmt.Stringer      // the adapter's typed system mode
	Tuple() string           // every machine's control state, formatted
	Settled() bool           // stopped by design, including by an exhausted budget
	Describe(s Step) string  // one readable trace line
	Kinds() int              // number of step kinds declared
	Invariants() []string    // names of the checks Check performs
}

// Trace is, at each point, a position in the list Steps returned.
type Trace []int

// ViolationKind classifies a violation. The zero value means none.
type ViolationKind uint8

// Violation kinds, in the order the spec lists them.
const (
	kindNone ViolationKind = iota
	Invariant
	Deadlock
	Panic
	ApplyError
	ModeChange
	Nondeterminism
	numKinds
)

func (k ViolationKind) String() string {
	assert(k < numKinds, "invalid violation kind")
	switch k {
	case kindNone:
		return "none"
	case Invariant:
		return "invariant"
	case Deadlock:
		return "deadlock"
	case Panic:
		return "panic"
	case ApplyError:
		return "apply error"
	case ModeChange:
		return "mode change"
	case Nondeterminism:
		return "nondeterminism"
	default:
		panic("merilo: invalid violation kind")
	}
}

// Violation is the first rule broken, with the shortest path to it.
type Violation struct {
	Kind   ViolationKind
	Trace  Trace  // shortest path to the violating world
	Detail string // the error from Check or Apply, or the recovered panic value
}

// Found reports whether v records a violation.
func (v Violation) Found() bool {
	assert(v.Kind < numKinds, "invalid violation kind")
	assert(v.Kind != kindNone || v.Trace == nil, "trace without a violation")
	return v.Kind != kindNone
}

// Options bounds a search. MaxDepth and MaxStates have no defaults.
type Options struct {
	MaxDepth        int    // steps per path
	MaxStates       int    // visited-set cap
	RequireComplete bool   // truncation fails the test
	Regressions     string // directory under testdata/merilo/ for counterexamples; empty writes none
}

// Result is what a search reached and the first violation, if any.
type Result struct {
	Complete      bool
	States, Depth int
	Cut           int         // worlds found but never expanded, at MaxDepth or MaxStates
	Tuples        []string    // control-state tuples reached, sorted
	Modes         []string    // system modes reached, sorted
	ModeEdges     [][2]string // system-mode changes reached, sorted
	Witnesses     []Trace     // shortest schedule per tuple, then per mode edge
	Violation     Violation
}

func assert(ok bool, msg string) {
	if !ok {
		panic("merilo: " + msg)
	}
}
