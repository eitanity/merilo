// Package merilo explores and fuzzes systems of hand-written state
// machines through an adapter. It is imported only from _test.go files.
package merilo

import "fmt"

type constError string

func (e constError) Error() string {
	return string(e)
}

// ErrModeChange is wrapped by an adapter's Apply when the system mode
// changes in a way the adapter does not allow.
const ErrModeChange = constError("merilo: undeclared system-mode change")

// System is the adapter between the harness and the adopter's machines.
// Steps are kinds, numbered 0 to Kinds()-1; the harness asks which are
// enabled one at a time, so nothing but integers crosses the interface.
type System interface {
	Reset()                // return to the initial world
	Kinds() int            // number of step kinds
	Enabled(k int) bool    // whether step kind k can be taken in the current world
	Apply(k int) error     // take step kind k; non-nil error is a violation
	World() any            // the current world as a strictly comparable value
	Check() error          // invariants on the current world
	Tuple() string         // every machine's control state, formatted
	Settled() bool         // stopped by design, including by an exhausted budget
	Describe(k int) string // one readable trace line for step kind k
	Invariants() []string  // names of the checks Check performs
}

// Trace is the step kinds taken from the initial world, in order.
type Trace []int

// ViolationKind classifies a violation. The zero value means none.
type ViolationKind uint8

// Violation kinds.
const (
	kindNone ViolationKind = iota
	Invariant
	Deadlock
	ApplyError
	ModeChange
	Nondeterminism
	numKinds
)

func (k ViolationKind) String() string {
	names := [numKinds]string{"none", "invariant", "deadlock", "apply error", "mode change", "nondeterminism"}
	if i := int(k); i < len(names) {
		return names[i]
	}
	return fmt.Sprintf("ViolationKind(%d)", uint8(k))
}

// Violation is the first rule broken, with the shortest path to it.
type Violation struct {
	Kind   ViolationKind
	Trace  Trace  // shortest path to the violating world
	Detail string // the error from Check or Apply
}

// Found reports whether v records a violation.
func (v Violation) Found() bool { return v.Kind != kindNone }

// Options bounds a search. MaxStates has no default.
type Options struct {
	MaxStates       int  // the most worlds the search stores
	RequireComplete bool // stopping at MaxStates fails the test
}

// Result is what a search reached and the first violation, if any.
type Result struct {
	Complete  bool     // every reachable world was checked
	States    int      // worlds stored
	Depth     int      // every world within this many steps of the start was checked
	Tuples    []string // control-state tuples reached, sorted
	Violation Violation
}

// fault is an internal error: an invariant of merilo itself was broken.
// It is returned to the entry point, which fails the one test.
func fault(format string, args ...any) error {
	return fmt.Errorf("merilo: internal error: "+format, args...)
}
