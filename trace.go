package merilo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

// Replay resets sys and follows tr, checking invariants after each step.
// It fails at a step whose kind is not enabled.
func Replay(sys System, tr Trace) error {
	if sys == nil {
		return errors.New("merilo: nil System")
	}
	d, err := newDriver(sys)
	if err != nil {
		return err
	}
	if a := d.replay(tr); a.kind != kindNone {
		return fmt.Errorf("merilo: %s: %s", a.kind, a.detail)
	}
	return nil
}

// Format replays tr, writing one line per step: its description and every
// machine's control state after it.
func (tr Trace) Format(sys System, w io.Writer) error {
	if w == nil {
		return errors.New("merilo: Format needs a writer")
	}
	if sys == nil {
		return errors.New("merilo: nil System")
	}
	d, err := newDriver(sys)
	if err != nil {
		return err
	}
	return walk(&d, tr, w)
}

// FuzzBytes encodes tr as a fuzz input: an all-zero swarm mask, then each
// kind as an unsigned varint, which the fuzzer takes as it is whenever the
// kind is enabled.
func (tr Trace) FuzzBytes(sys System) ([]byte, error) {
	if sys == nil {
		return nil, errors.New("merilo: nil System")
	}
	d, err := newDriver(sys)
	if err != nil {
		return nil, err
	}
	b := make([]byte, (d.kinds+7)/8, (d.kinds+7)/8+len(tr)*2)
	for i, k := range tr {
		if k < 0 || k >= d.kinds {
			return nil, fmt.Errorf("merilo: trace step %d is kind %d; kinds run from 0 to %d", i, k, d.kinds-1)
		}
		b = binary.AppendUvarint(b, uint64(k))
	}
	return b, nil
}

// walk replays tr, writing one display line per step. It stops after the
// first failing step.
func walk(d *driver, tr Trace, w io.Writer) error {
	if d == nil || d.sys == nil {
		return fault("walk without a System")
	}
	if w == nil {
		return fault("walk without a writer")
	}
	d.sys.Reset()
	for i, k := range tr {
		if enabled := d.enabled(k); !enabled {
			return fmt.Errorf("merilo: step %d: kind %d is not enabled; the trace no longer fits the system", i, k)
		}
		desc := oneLine(d.sys.Describe(k))
		a := d.apply(k)
		if a.kind == kindNone {
			a = d.check()
		}
		if _, err := fmt.Fprintf(w, "#%02d %s | %s\n", i, desc, oneLine(d.sys.Tuple())); err != nil {
			return fmt.Errorf("merilo: writing trace: %w", err)
		}
		if a.kind != kindNone {
			return nil
		}
	}
	return nil
}

// oneLine makes an adapter's string safe for one line of a test log:
// newlines and tabs become spaces, and every character that is not
// graphic, such as a carriage return or a terminal escape, is written as a
// Go escape, so it cannot rewrite what a terminal shows.
func oneLine(s string) string {
	q := strconv.QuoteToGraphic(strings.NewReplacer("\n", " ", "\t", " ").Replace(s))
	return q[1 : len(q)-1]
}

// report fails t with the readable trace of a violation.
func report(t testing.TB, sys System, v Violation) {
	if t == nil {
		return
	}
	t.Helper()
	if sys == nil {
		t.Fatalf("%v", fault("report without a System"))
	}
	if found := v.Found(); !found {
		t.Fatalf("%v", fault("report without a violation"))
	}
	var b strings.Builder
	if err := v.Trace.Format(sys, &b); err != nil {
		t.Logf("merilo: trace could not be formatted: %v", err)
	}
	at := "in the initial world"
	if len(v.Trace) > 0 {
		at = fmt.Sprintf("after step #%02d", len(v.Trace)-1)
	}
	t.Errorf("merilo: %s violation %s: %s\n%sVIOLATION %s %q %s",
		v.Kind, at, oneLine(v.Detail), b.String(), v.Kind, v.Detail, at)
}
