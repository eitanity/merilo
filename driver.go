package merilo

import (
	"errors"
	"fmt"
)

// errReentered refuses a call into merilo from an adapter method that
// merilo is running.
const errReentered = constError("merilo: called again from inside a run: an adapter method must not call merilo")

// applied is the outcome of one Apply or Check: kindNone when it passed.
type applied struct {
	kind   ViolationKind
	detail string
}

// driver applies step kinds to a System.
type driver struct {
	sys   System
	kinds int
}

func newDriver(sys System) (driver, error) {
	if sys == nil {
		return driver{}, errors.New("merilo: nil System")
	}
	if again := reentered(); again {
		return driver{}, errReentered
	}
	kinds := sys.Kinds()
	if kinds <= 0 || kinds > maxKinds {
		return driver{}, fmt.Errorf("merilo: Kinds() is %d; it must be from 1 to %d", kinds, maxKinds)
	}
	return driver{sys: sys, kinds: kinds}, nil
}

// enabled reports whether step kind k can be taken in the current world.
func (d *driver) enabled(k int) bool {
	if d == nil || d.sys == nil {
		return false
	}
	if k < 0 || k >= d.kinds {
		return false
	}
	return d.sys.Enabled(k)
}

// stuck reports whether no step kind is enabled in the current world.
func (d *driver) stuck() bool {
	if d == nil || d.sys == nil {
		return true
	}
	kinds := d.kinds
	for k := 0; k < kinds; k++ {
		if d.sys.Enabled(k) {
			return false
		}
	}
	return true
}

// apply takes step kind k and classifies any failure. A kind that is not
// enabled in the current world fails as an apply error.
func (d *driver) apply(k int) applied {
	if d == nil || d.sys == nil {
		return applied{kind: ApplyError, detail: "internal error: apply without a System"}
	}
	if k < 0 || k >= d.kinds {
		return applied{kind: ApplyError, detail: fmt.Sprintf("step kind %d is not below Kinds() = %d", k, d.kinds)}
	}
	if enabled := d.sys.Enabled(k); !enabled {
		return applied{kind: ApplyError, detail: fmt.Sprintf("step kind %d is not enabled in this world", k)}
	}
	err := d.sys.Apply(k)
	if mode := errors.Is(err, ErrModeChange); mode {
		return applied{kind: ModeChange, detail: err.Error()}
	}
	if err != nil {
		return applied{kind: ApplyError, detail: err.Error()}
	}
	return applied{}
}

// check runs the invariants on the current world.
func (d *driver) check() applied {
	if d == nil || d.sys == nil {
		return applied{kind: ApplyError, detail: "internal error: check without a System"}
	}
	if err := d.sys.Check(); err != nil {
		return applied{kind: Invariant, detail: err.Error()}
	}
	return applied{}
}

// replay resets the system and follows tr. It stops at the first failure,
// including a kind the current world does not enable.
func (d *driver) replay(tr Trace) applied {
	if d == nil || d.sys == nil {
		return applied{kind: ApplyError, detail: "internal error: replay without a System"}
	}
	d.sys.Reset()
	for i, k := range tr {
		if a := d.apply(k); a.kind != kindNone {
			a.detail = fmt.Sprintf("step %d: %s", i, a.detail)
			return a
		}
		if a := d.check(); a.kind != kindNone {
			return a
		}
	}
	return applied{}
}
