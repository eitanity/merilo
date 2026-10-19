package merilo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const traceHeader = "merilo trace v1"

// Replay resets sys and follows tr, checking invariants after each step.
// It fails if a position is not below the enabled count (TR-03).
func Replay(sys System, tr Trace) error {
	assert(sys != nil, "nil System")
	a := newDriver(sys).replay(tr)
	if a.kind != kindNone {
		assert(a.kind < numKinds, "invalid kind")
		return fmt.Errorf("merilo: %s: %s", a.kind, a.detail)
	}
	return nil
}

// Format replays tr, writing one line per step: its description and every
// machine's control state after it.
func (tr Trace) Format(sys System, w io.Writer) error {
	assert(sys != nil, "Format without System")
	assert(w != nil, "Format without a writer")
	return walk(newDriver(sys), tr, w, false)
}

// FuzzBytes encodes tr as a fuzz input: an all-zero swarm mask, then each
// position as an unsigned varint (TR-01).
func (tr Trace) FuzzBytes(sys System) []byte {
	assert(sys != nil, "nil System")
	kinds := sys.Kinds()
	assert(kinds > 0, "Kinds() must be positive")
	b := make([]byte, (kinds+7)/8, (kinds+7)/8+len(tr)*2)
	for _, p := range tr {
		assert(p >= 0, "negative trace position")
		b = binary.AppendUvarint(b, uint64(p))
	}
	return b
}

// walk replays tr, writing display lines, or regression lines when
// regress is set. It stops after the first failing step.
func walk(d *driver, tr Trace, w io.Writer, regress bool) error {
	assert(w != nil, "nil writer")
	assert(d.sys != nil, "walk without System")
	d.sys.Reset()
	for i, p := range tr {
		steps := d.enabled()
		if p < 0 || p >= len(steps) {
			return fmt.Errorf("merilo: step %d: position %d is not below the %d enabled steps", i, p, len(steps))
		}
		desc := oneLine(d.sys.Describe(steps[p]))
		a := d.apply(steps[p])
		if a.kind == kindNone {
			a = d.check()
		}
		var err error
		if regress {
			_, err = fmt.Fprintf(w, "%d\t%s\n", p, desc)
		} else {
			_, err = fmt.Fprintf(w, "#%02d %s | %s\n", i, desc, oneLine(d.sys.Tuple()))
		}
		if err != nil {
			return err
		}
		if a.kind != kindNone {
			return nil
		}
	}
	return nil
}

func oneLine(s string) string {
	n := len(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	assert(!strings.ContainsAny(s, "\n\t"), "line still holds a separator")
	assert(len(s) == n, "oneLine changed the length")
	return s
}

// report fails t with the readable trace and, when dir is set, writes the
// counterexample as a regression file (TR-02, TR-05).
func report(t testing.TB, sys System, dir string, v Violation) {
	t.Helper()
	assert(v.Found(), "report without a violation")
	assert(sys != nil, "report without System")
	var b strings.Builder
	if err := v.Trace.Format(sys, &b); err != nil {
		put(&b, fmt.Sprintf("(trace could not be formatted: %v)\n", err))
	}
	at := "in the initial world"
	if len(v.Trace) > 0 {
		at = fmt.Sprintf("after step #%02d", len(v.Trace)-1)
	}
	t.Errorf("merilo: %s violation %s: %s\n%sVIOLATION %s %q %s",
		v.Kind, at, v.Detail, b.String(), v.Kind, v.Detail, at)
	if dir != "" {
		writeRegression(t, sys, dir, v.Trace)
	}
}

func writeRegression(t testing.TB, sys System, dir string, tr Trace) {
	t.Helper()
	assert(dir != "", "regression without a directory")
	var b strings.Builder
	put(&b, traceHeader+"\n")
	if err := walk(newDriver(sys), tr, &b, true); err != nil {
		t.Errorf("merilo: regression not recorded: %v", err)
		return
	}
	assert(strings.HasPrefix(b.String(), traceHeader), "regression without header")
	sum := sha256.Sum256([]byte(b.String()))
	path := filepath.Join("testdata", "merilo", dir, hex.EncodeToString(sum[:])[:16]+".trace")
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err == nil {
		err = os.WriteFile(path, []byte(b.String()), 0o644)
	}
	if err != nil {
		t.Logf("merilo: could not write %s (%v); its contents are:\n%s", path, err, b.String())
		return
	}
	t.Logf("merilo: counterexample written to %s", path)
}

// Regress replays every trace in testdata/merilo/<dir>/ strictly. A trace
// passes when it replays with no violation; it fails when the bug has
// returned or when it no longer fits the system (TR-03, TR-04).
func Regress(t testing.TB, sys System, dir string) {
	t.Helper()
	assert(dir != "", "Regress without a directory")
	root := filepath.Join("testdata", "merilo", dir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		t.Logf("merilo: no regressions in %s", root)
		return
	}
	if err != nil {
		t.Fatalf("merilo: %v", err)
	}
	d := newDriver(sys)
	assert(d.kinds > 0, "driver without kinds")
	for _, e := range entries {
		assert(e.Name() != "", "directory entry without a name")
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".trace") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Errorf("merilo: %v", err)
			continue
		}
		if msg := regressOne(d, string(data)); msg != "" {
			t.Errorf("merilo: regression %s: %s", e.Name(), msg)
		}
	}
}

// regressOne replays one trace file and returns why it failed, or "".
func regressOne(d *driver, text string) string {
	assert(d != nil, "regressOne without a driver")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	assert(len(lines) >= 1, "Split returned nothing")
	if len(lines) == 0 || lines[0] != traceHeader {
		return "missing header " + strconv.Quote(traceHeader)
	}
	d.sys.Reset()
	for i, line := range lines[1:] {
		num, want, ok := strings.Cut(line, "\t")
		p, err := strconv.Atoi(num)
		if !ok || err != nil {
			return fmt.Sprintf("line %d is not \"<position>\\t<description>\"", i+2)
		}
		steps := d.enabled()
		if p < 0 || p >= len(steps) {
			return fmt.Sprintf("step %d: position %d is not below the %d enabled steps; the trace no longer fits the system: re-run Explore and replace the file", i, p, len(steps))
		}
		if got := oneLine(d.sys.Describe(steps[p])); got != want {
			return fmt.Sprintf("step %d is now %q, recorded as %q; the trace no longer fits the system: re-run Explore and replace the file", i, got, want)
		}
		a := d.apply(steps[p])
		if a.kind == kindNone {
			a = d.check()
		}
		if a.kind != kindNone {
			return fmt.Sprintf("the bug is back: %s at step %d: %s", a.kind, i, a.detail)
		}
	}
	return ""
}

// put appends to a strings.Builder, which cannot fail.
func put(b *strings.Builder, s string) {
	assert(b != nil, "put into a nil builder")
	_, err := b.WriteString(s)
	assert(err == nil, "strings.Builder failed")
}
