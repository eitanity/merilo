package merilo

import (
	"errors"
	"fmt"
)

// applied is the outcome of one Apply or Check: kindNone when it passed.
type applied struct {
	kind   ViolationKind
	detail string
}

// driver applies steps to a System and recovers panics from Apply.
type driver struct {
	sys   System
	kinds int
	steps []Step
	last  applied
}

func newDriver(sys System) *driver {
	assert(sys != nil, "nil System")
	kinds := sys.Kinds()
	assert(kinds > 0, "Kinds() must be positive")
	return &driver{sys: sys, kinds: kinds, steps: make([]Step, 0, 16)}
}

// enabled returns the enabled steps. The next call reuses the slice.
func (d *driver) enabled() []Step {
	assert(d.sys != nil, "driver without System")
	d.steps = d.sys.Steps(d.steps[:0])
	assert(len(d.steps) < 1<<20, "Steps returned an implausible number of steps")
	return d.steps
}

// apply applies s and classifies any failure.
func (d *driver) apply(s Step) applied {
	assert(d.sys != nil, "driver without System")
	assert(d.kinds > 0, "driver without kinds")
	if int(s.Kind) >= d.kinds {
		return applied{kind: ApplyError, detail: fmt.Sprintf("step kind %d is not below Kinds() = %d", s.Kind, d.kinds)}
	}
	d.last = applied{}
	d.run(s)
	return d.last
}

func (d *driver) run(s Step) {
	assert(d.last.kind == kindNone, "run with a stale outcome")
	assert(int(s.Kind) < d.kinds, "run with an undeclared kind")
	defer d.catch()
	err := d.sys.Apply(s)
	switch {
	case err == nil:
	case errors.Is(err, ErrModeChange):
		d.last = applied{kind: ModeChange, detail: err.Error()}
	default:
		d.last = applied{kind: ApplyError, detail: err.Error()}
	}
}

func (d *driver) catch() {
	assert(d.sys != nil, "catch without System")
	assert(d.kinds > 0, "catch without kinds")
	if v := recover(); v != nil {
		d.last = applied{kind: Panic, detail: fmt.Sprint(v)}
	}
}

// check runs the invariants on the current world.
func (d *driver) check() applied {
	assert(d.sys != nil, "driver without System")
	if err := d.sys.Check(); err != nil {
		assert(err.Error() != "", "Check returned an error with no text")
		return applied{kind: Invariant, detail: err.Error()}
	}
	return applied{}
}

// stuck reports whether the current world has no enabled step and is
// not settled by design.
func (d *driver) stuck() bool {
	assert(d.sys != nil, "driver without System")
	assert(d.kinds > 0, "driver without kinds")
	return len(d.enabled()) == 0 && !d.sys.Settled()
}

// replay resets the system and follows tr. It stops at the first failure,
// including a position the current world does not have.
func (d *driver) replay(tr Trace) applied {
	assert(d.sys != nil, "driver without System")
	assert(d.kinds > 0, "driver without kinds")
	d.sys.Reset()
	for i, p := range tr {
		steps := d.enabled()
		if p < 0 || p >= len(steps) {
			return applied{kind: ApplyError, detail: fmt.Sprintf(
				"step %d: position %d is not below the %d enabled steps; the trace no longer fits the system", i, p, len(steps))}
		}
		if a := d.apply(steps[p]); a.kind != kindNone {
			return a
		}
		if a := d.check(); a.kind != kindNone {
			return a
		}
	}
	return applied{}
}
