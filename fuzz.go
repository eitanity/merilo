package merilo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

// FuzzOptions bounds a fuzz run. MaxDepth has no default.
type FuzzOptions struct {
	MaxDepth int     // steps per input, drain included
	Explore  Options // when set, a search with this bound runs first, as Explore does, and seeds the fuzzer
}

// Fuzz turns sys into a native Go fuzz target. Each input decodes into a
// schedule; a violation fails with the readable trace.
// With Explore set, the shortest schedule to each tuple the search reaches
// seeds the fuzzer, so fuzzing starts at the edge of the exhaustive search.
func Fuzz(f *testing.F, sys System, o FuzzOptions) {
	if f == nil {
		return
	}
	f.Helper()
	if sys == nil {
		f.Fatalf("merilo: nil System")
	}
	r, err := newRunner(sys, o.MaxDepth)
	if err != nil {
		f.Fatalf("%v", err)
	}
	if seeded := o.Explore != (Options{}); seeded && !seed(f, sys, o) {
		return
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := r.run(data)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if v.Found() {
			report(t, sys, v)
		}
	})
}

// seed runs the search o.Explore bounds and adds its witnesses to f. It
// reports false when the search failed f, so there is nothing to fuzz.
func seed(f *testing.F, sys System, o FuzzOptions) bool {
	if f == nil || sys == nil {
		return false
	}
	res, s := explore(f, sys, o.Explore)
	if s == nil || res.Violation.Found() {
		return false
	}
	seeds, err := s.witnesses(o.MaxDepth)
	if err != nil {
		f.Fatalf("%v", err)
	}
	for _, b := range seeds {
		f.Add(b)
	}
	return true
}

// runner decodes fuzz inputs into schedules and runs them.
type runner struct {
	d        driver
	maxDepth int
	trace    Trace // the input's schedule so far, MaxDepth steps at most
	viol     Trace // a violation's trace, valid until the next input
	pos      int
	drain    bool
}

func newRunner(sys System, depth int) (*runner, error) {
	if sys == nil {
		return nil, errors.New("merilo: nil System")
	}
	if depth <= 0 || depth > maxDepth {
		return nil, fmt.Errorf("merilo: FuzzOptions.MaxDepth is %d; it must be set, from 1 to %d, and has no default", depth, maxDepth)
	}
	d, err := newDriver(sys)
	if err != nil {
		return nil, err
	}
	return &runner{d: d, maxDepth: depth, trace: make(Trace, 0, depth), viol: make(Trace, 0, depth)}, nil
}

// run executes one input and returns the first violation.
func (r *runner) run(data []byte) (Violation, error) {
	if r == nil {
		return Violation{}, fault("run without a runner")
	}
	maskLen := (r.d.kinds + 7) / 8
	if maskLen > len(data) {
		maskLen = len(data)
	}
	mask := data[:maskLen]
	r.trace, r.pos, r.drain = r.trace[:0], maskLen, false
	r.d.sys.Reset()
	if a := r.d.check(); a.kind != kindNone {
		return Violation{Kind: a.kind, Trace: Trace{}, Detail: a.detail}, nil
	}
	maxDepth := r.maxDepth
	for depth := 0; depth < maxDepth; depth++ {
		k, err := r.choose(data, mask)
		if err != nil {
			return Violation{}, err
		}
		if k < 0 {
			return r.settledOrDeadlock(), nil
		}
		r.trace = append(r.trace, k)
		a := r.d.apply(k)
		if a.kind == kindNone {
			a = r.d.check()
		}
		if a.kind != kindNone {
			return Violation{Kind: a.kind, Trace: append(r.viol[:0], r.trace...), Detail: a.detail}, nil
		}
	}
	return Violation{}, nil
}

// choose takes the kind the input names. When the bytes or the
// enabled, unmasked kinds run out it drains: the lowest enabled kind, mask
// or not. It returns -1 when no kind is enabled.
func (r *runner) choose(data, mask []byte) (int, error) {
	if r == nil {
		return -1, fault("choose without a runner")
	}
	if !r.drain {
		k, err := r.named(data, mask)
		if err != nil || k >= 0 {
			return k, err
		}
		r.drain = true
	}
	kinds := r.d.kinds
	for k := 0; k < kinds; k++ {
		if r.d.enabled(k) {
			return k, nil
		}
	}
	return -1, nil
}

// named reads the next varint v and returns the first enabled, unmasked
// kind from v mod Kinds() on, wrapping round, or -1 when the bytes or such
// kinds have run out.
func (r *runner) named(data, mask []byte) (int, error) {
	if r == nil {
		return -1, fault("named without a runner")
	}
	if r.pos > len(data) {
		return -1, fault("read position %d past the %d-byte input", r.pos, len(data))
	}
	kinds := r.d.kinds
	if kinds <= 0 || kinds > maxKinds {
		return -1, fault("choose with %d step kinds", kinds)
	}
	v, n := binary.Uvarint(data[r.pos:])
	if n <= 0 {
		return -1, nil
	}
	r.pos += n
	m := v % uint64(kinds)
	if m >= maxKinds {
		return -1, fault("chosen kind %d is not below %d", m, maxKinds)
	}
	start := int(m)
	for j := 0; j < kinds; j++ {
		if k := (start + j) % kinds; !masked(mask, k) && r.d.enabled(k) {
			return k, nil
		}
	}
	return -1, nil
}

func masked(mask []byte, k int) bool {
	if k < 0 {
		return true
	}
	i := k / 8
	return i < len(mask) && mask[i]&(1<<(k%8)) != 0
}

// settledOrDeadlock judges an input that ended with no enabled kind.
func (r *runner) settledOrDeadlock() Violation {
	if r == nil || r.d.sys == nil {
		return Violation{Kind: ApplyError, Detail: "internal error: drain without a System"}
	}
	if settled := r.d.sys.Settled(); !settled {
		return Violation{Kind: Deadlock, Trace: append(r.viol[:0], r.trace...), Detail: "no step is enabled and Settled() is false"}
	}
	return Violation{}
}
