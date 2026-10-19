package merilo

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"runtime"
	"testing"
)

// Totality exposes one machine's state–event table to Total by position.
// Positions are indexes the test chooses, never domain values.
type Totality interface {
	Machines() int                         // machine values to check; position 0 is the zero machine
	Events() int                           // events to fire; position 0 is the zero event
	Fire(m, e int) (quiet, unchanged bool) // build machine m, fire event e
	Name(m, e int) string                  // a readable name for the pair
}

// Total fires every event at every machine. Position 0 of either must
// panic; any other panic fails, and so does a quiet pair that changed
// the machine or returned actions (M-02 to M-04).
func Total(t testing.TB, tab Totality) {
	t.Helper()
	assert(tab != nil, "nil Totality")
	nm, ne := tab.Machines(), tab.Events()
	assert(nm >= 0 && ne >= 0, "negative table size")
	if nm < 2 || ne < 2 {
		t.Fatalf("merilo: Totality needs the zero machine and event plus at least one real one; got %d machines, %d events", nm, ne)
	}
	c := &totalCall{tab: tab}
	for m := range nm {
		for e := range ne {
			if msg := c.judge(m, e); msg != "" {
				t.Errorf("merilo: %s: %s", tab.Name(m, e), msg)
			}
		}
	}
}

type totalCall struct {
	tab       Totality
	quiet     bool
	unchanged bool
	panicked  bool
	value     string
}

// judge fires one pair and returns why it fails, or "".
func (c *totalCall) judge(m, e int) string {
	assert(m >= 0 && e >= 0, "negative position")
	c.quiet, c.unchanged, c.panicked, c.value = false, false, false, ""
	c.fire(m, e)
	zero := m == 0 || e == 0
	assert(c.value == "" || c.panicked, "panic value without a panic")
	switch {
	case zero && !c.panicked:
		return "the zero machine or zero event was accepted; it must panic"
	case !zero && c.panicked:
		return "panicked: " + c.value
	case !zero && c.quiet && !c.unchanged:
		return "Ignored or Rejected, but the machine changed or actions were returned"
	}
	return ""
}

func (c *totalCall) fire(m, e int) {
	assert(c.tab != nil, "fire without a Totality")
	assert(!c.panicked, "fire with a stale panic")
	defer c.catch()
	c.quiet, c.unchanged = c.tab.Fire(m, e)
}

func (c *totalCall) catch() {
	assert(c.tab != nil, "catch without a Totality")
	assert(!c.panicked, "catch with a stale panic")
	if v := recover(); v != nil {
		c.panicked, c.value = true, fmt.Sprint(v)
	}
}

// CheckDeterminism runs the search at GOMAXPROCS 1 and at NumCPU and fails
// t if the two results differ. It changes a process-wide setting, so it
// must not run alongside parallel tests.
func CheckDeterminism(t testing.TB, sys System, o Options) {
	t.Helper()
	if err := validate(sys, o); err != nil {
		t.Fatalf("%v", err)
	}
	assert(runtime.NumCPU() > 0, "no CPUs")
	prev := runtime.GOMAXPROCS(1)
	a := runSearch(sys, o).Digest()
	runtime.GOMAXPROCS(runtime.NumCPU())
	b := runSearch(sys, o).Digest()
	runtime.GOMAXPROCS(prev)
	assert(runtime.GOMAXPROCS(0) == prev, "GOMAXPROCS not restored")
	if a != b {
		t.Errorf("merilo: two searches of the same system differ; the adapter depends on something outside its steps, such as map iteration order")
	}
}

// Digest is a SHA-256 of every field of r in a fixed encoding.
func (r Result) Digest() [32]byte {
	b := appendBool(nil, r.Complete)
	b = binary.AppendVarint(b, int64(r.States))
	b = binary.AppendVarint(b, int64(r.Depth))
	b = binary.AppendVarint(b, int64(r.Cut))
	b = appendStrings(b, r.Tuples)
	b = appendStrings(b, r.Modes)
	b = binary.AppendUvarint(b, uint64(len(r.ModeEdges)))
	for _, e := range r.ModeEdges {
		b = appendStrings(b, e[:])
	}
	b = binary.AppendUvarint(b, uint64(len(r.Witnesses)))
	for _, w := range r.Witnesses {
		b = appendTrace(b, w)
	}
	b = append(b, byte(r.Violation.Kind))
	b = appendTrace(b, r.Violation.Trace)
	b = appendStrings(b, []string{r.Violation.Detail})
	assert(r.Violation.Kind < numKinds, "invalid violation kind")
	assert(len(b) > 0, "empty digest input")
	return sha256.Sum256(b)
}

func appendBool(b []byte, v bool) []byte {
	n := len(b)
	c := byte(0)
	if v {
		c = 1
	}
	b = append(b, c)
	assert(len(b) == n+1, "bool not written")
	return b
}

func appendStrings(b []byte, ss []string) []byte {
	n := len(b)
	b = binary.AppendUvarint(b, uint64(len(ss)))
	assert(len(b) > n, "length not written")
	for _, s := range ss {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}
	assert(len(b) >= n+len(ss), "strings not written")
	return b
}

func appendTrace(b []byte, tr Trace) []byte {
	n := len(b)
	b = binary.AppendUvarint(b, uint64(len(tr)))
	assert(len(b) > n, "length not written")
	for _, p := range tr {
		assert(p >= 0, "negative trace position")
		b = binary.AppendUvarint(b, uint64(p))
	}
	return b
}
