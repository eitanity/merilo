package merilo

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// FuzzOptions bounds a fuzz run. MaxDepth has no default.
type FuzzOptions struct {
	MaxDepth int     // steps per input, drain included
	Seeds    []Trace // witness schedules, usually Result.Witnesses
}

// Fuzz turns sys into a native Go fuzz target. Each input decodes into a
// schedule (FZ-01 to FZ-04); a violation fails with the readable trace.
func Fuzz(f *testing.F, sys System, o FuzzOptions) {
	f.Helper()
	if err := validateFuzz(sys, o); err != nil {
		f.Fatalf("%v", err)
	}
	for _, s := range o.Seeds {
		assert(len(s) <= o.MaxDepth, "seed longer than MaxDepth")
		f.Add(s.FuzzBytes(sys))
	}
	r := newRunner(sys, o.MaxDepth)
	assert(r.d.kinds == sys.Kinds(), "runner kinds differ from the System")
	f.Fuzz(func(t *testing.T, data []byte) {
		if v := r.run(data, nil); v.Found() {
			report(t, sys, "", v)
		}
	})
}

func validateFuzz(sys System, o FuzzOptions) error {
	switch {
	case sys == nil:
		return fmt.Errorf("merilo: nil System")
	case o.MaxDepth <= 0:
		return fmt.Errorf("merilo: FuzzOptions.MaxDepth must be set; it has no default")
	case sys.Kinds() <= 0:
		return fmt.Errorf("merilo: Kinds() must be positive")
	}
	assert(o.MaxDepth > 0, "validated MaxDepth out of range")
	return nil
}

// runner decodes fuzz inputs into schedules and runs them.
type runner struct {
	d        *driver
	maxDepth int
	trace    Trace
	pos      int
	drain    bool
}

func newRunner(sys System, maxDepth int) *runner {
	assert(maxDepth > 0, "runner without MaxDepth")
	assert(sys != nil, "runner without System")
	return &runner{d: newDriver(sys), maxDepth: maxDepth}
}

// run executes one input, reporting what it reached to obs when obs is
// not nil, and returns the first violation.
func (r *runner) run(data []byte, obs *observer) Violation {
	assert(r.maxDepth > 0, "runner without MaxDepth")
	maskLen := (r.d.kinds + 7) / 8
	if maskLen > len(data) {
		maskLen = len(data)
	}
	mask := data[:maskLen]
	r.trace, r.pos, r.drain = r.trace[:0], maskLen, false
	r.d.sys.Reset()
	if a := r.d.check(); a.kind != kindNone {
		return r.violation(a)
	}
	obs.see(r.d.sys)
	for depth := 0; depth < r.maxDepth; depth++ {
		steps := r.d.enabled()
		p := -1
		if !r.drain {
			p = r.choose(steps, data, mask)
		}
		if r.drain {
			if len(steps) == 0 {
				return r.settledOrDeadlock()
			}
			p = 0
		}
		assert(p >= 0 && p < len(steps), "chosen position outside the enabled steps")
		if a := r.step(steps[p], p, obs); a.kind != kindNone {
			return r.violation(a)
		}
	}
	assert(len(r.trace) == r.maxDepth, "run ended early without a verdict")
	return Violation{}
}

// step applies one chosen step and checks invariants after it.
func (r *runner) step(s Step, p int, obs *observer) applied {
	assert(p >= 0, "no step chosen")
	from := obs.mode(r.d.sys)
	r.trace = append(r.trace, p)
	assert(len(r.trace) <= r.maxDepth, "trace longer than MaxDepth")
	a := r.d.apply(s)
	if a.kind == kindNone {
		a = r.d.check()
	}
	if a.kind == kindNone {
		obs.see(r.d.sys)
		obs.edge(from, obs.mode(r.d.sys))
	}
	return a
}

// choose reads the next varint and picks among the unmasked enabled steps
// (FZ-02). It switches to draining when bytes or unmasked steps run out.
func (r *runner) choose(steps []Step, data, mask []byte) int {
	assert(r.pos <= len(data), "read position past the input")
	v, n := binary.Uvarint(data[r.pos:])
	unmasked := 0
	for _, s := range steps {
		if !masked(mask, s.Kind) {
			unmasked++
		}
	}
	if n <= 0 || unmasked == 0 {
		r.drain = true
		return -1
	}
	r.pos += n
	assert(r.pos <= len(data), "read past the input")
	want := int(v % uint64(unmasked))
	for i, s := range steps {
		if masked(mask, s.Kind) {
			continue
		}
		if want == 0 {
			return i
		}
		want--
	}
	panic("merilo: chosen step not found among unmasked steps")
}

func masked(mask []byte, k Kind) bool {
	i := int(k) / 8
	assert(i >= 0, "negative mask index")
	assert(len(mask) <= 1<<13, "mask longer than 2^16 kinds need")
	return i < len(mask) && mask[i]&(1<<(k%8)) != 0
}

// settledOrDeadlock judges a drain that ended with no enabled step (FZ-03).
func (r *runner) settledOrDeadlock() Violation {
	assert(len(r.d.enabled()) == 0, "deadlock check with steps enabled")
	assert(r.drain, "deadlock check outside the drain")
	if r.d.sys.Settled() {
		return Violation{}
	}
	return r.violation(applied{kind: Deadlock, detail: "no step is enabled and Settled() is false"})
}

func (r *runner) violation(a applied) Violation {
	assert(a.kind != kindNone, "violation without a kind")
	assert(len(r.trace) <= r.maxDepth, "trace longer than MaxDepth")
	return Violation{Kind: a.kind, Trace: append(Trace(nil), r.trace...), Detail: a.detail}
}

// observer records what runs reached, for Coverage. A nil observer
// records nothing.
type observer struct {
	tuples map[string]bool
	modes  map[string]bool
	edges  map[[2]string]bool
}

func (o *observer) see(sys System) {
	if o == nil {
		return
	}
	assert(sys != nil, "see a nil System")
	assert(o.tuples != nil && o.modes != nil, "observer without sets")
	o.tuples[sys.Tuple()] = true
	o.modes[sys.Mode().String()] = true
}

func (o *observer) mode(sys System) string {
	if o == nil {
		return ""
	}
	assert(sys != nil, "mode of a nil System")
	return sys.Mode().String()
}

func (o *observer) edge(from, to string) {
	if o == nil || from == to {
		return
	}
	assert(!strings.Contains(from+to, "\x00"), "mode name holds a NUL byte")
	assert(o.edges != nil, "observer without edges")
	o.edges[[2]string{from, to}] = true
}
