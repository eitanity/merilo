package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fixture copies testdata/base into a temporary module, then overlays each
// named case directory on top of it.
func fixture(t *testing.T, cases ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, src := range append([]string{"base"}, cases...) {
		copyTree(t, filepath.Join("testdata", src), dir)
	}
	return dir
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// vet runs merilovet on dir and returns each finding as "file:line: VET-nn",
// without its message, plus the exit code.
func vet(t *testing.T, dir string) ([]string, int) {
	t.Helper()
	var out bytes.Buffer
	code := run(dir, &out)
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "VET-") {
			t.Fatalf("unexpected output line %q (exit %d)", line, code)
		}
		got = append(got, fields[0]+" "+fields[1])
	}
	return got, code
}

func expect(t *testing.T, dir string, want ...string) {
	t.Helper()
	got, code := vet(t, dir)
	if !slices.Equal(got, want) {
		t.Fatalf("findings:\n got  %q\n want %q", got, want)
	}
	wantCode := 0
	if len(want) > 0 {
		wantCode = 1
	}
	if code != wantCode {
		t.Fatalf("exit code %d, want %d", code, wantCode)
	}
}

// Tests run sequentially: merilovet points build.Default at the module
// it checks, so two fixtures cannot be checked at once in one process.

// TestClean is the clean half of AC-09: the base fixture yields nothing.
func TestClean(t *testing.T) {
	expect(t, fixture(t))
}

// TestEachRule is AC-09: one fixture per rule yields exactly that rule.
func TestEachRule(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"vet02", []string{"m/machine.go:6: VET-02"}},
		{"vet03", []string{"m/extra.go:3: VET-03"}},
		{"vet08", []string{"h/sw.go:6: VET-08"}},
		{"vet08zero", []string{"m/enums.go:16: VET-08"}},
		{"vet10", []string{"h/x.go:6: VET-10"}},
		{"vet11", []string{"h/x.go:5: VET-11"}},
		{"vet12", []string{"m/extra.go:4: VET-12"}},
		{"vet12export", []string{"m/extra.go:3: VET-12"}},
		{"vet13", []string{"h/x.go:8: VET-13"}},
		{"vet14", []string{"h/x.go:5: VET-14"}},
		{"vet15", []string{"m/machine.go:5: VET-15"}},
		{"vet16", []string{"m/machine.go:12: VET-16"}},
		{"vet17", []string{"h/x.go:7: VET-17"}},
		{"vet20", []string{"h/x.go:3: VET-20"}},
		{"vet21", []string{"h/x.go:3: VET-21"}},
		{"vet22", []string{"h/x.go:3: VET-22"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, fixture(t, tc.name), tc.want...)
		})
	}
}

// mutationWant is every FAIL form of the spec's Mutation rules example;
// the PASS forms in the same file yield nothing.
var mutationWant = []string{
	"h/mut.go:34: VET-22",  // var a Node
	"h/mut.go:35: VET-22",  // Node{}
	"h/mut.go:36: VET-22",  // new(Node)
	"h/mut.go:37: VET-22",  // make([]Node, 3)
	"h/mut.go:42: VET-21",  // saved := n.order
	"h/mut.go:43: VET-21",  // snapshot := *n
	"h/mut.go:44: VET-21",  // ch <- n.order
	"h/mut.go:45: VET-21",  // range value variable
	"h/mut.go:51: VET-21",  // value receiver on Node
	"h/mut.go:54: VET-21",  // existing machine inside a new literal
	"h/mut.go:58: VET-21",  // n.order = m.New()
	"h/mut.go:59: VET-21",  // *n = Node{...}
	"h/mut.go:65: VET-13",  // nodes[j] fired into nodes[i]
	"h/mut.go:67: VET-13",  // other fired into n
	"h/mut.go:76: VET-13",  // receiver with a side effect
	"h/mut.go:82: VET-17",  // outcome discarded
	"h/mut.go:88: VET-17",  // outcome stored in a field
	"h/mut.go:95: VET-17",  // acts overwritten before read
	"h/mut.go:95: VET-17",  // out overwritten before read
	"h/mut.go:107: VET-17", // _ = out
}

// TestMutationExample covers AC-16, AC-18 and AC-19: each FAIL form yields
// its diagnostic, and the PASS forms, including a read in an if init
// statement and a nested block, yield none.
func TestMutationExample(t *testing.T) {
	expect(t, fixture(t, "mutation"), mutationWant...)
}

// TestHolderDoingIO is AC-12: a holder that imports time and net and starts
// goroutines, with no marker comments, is checked; only the revert is flagged.
func TestHolderDoingIO(t *testing.T) {
	dir := fixture(t, "io")
	assertNoMarkers(t, dir)
	expect(t, dir, "h/io.go:12: VET-21", "h/io.go:14: VET-21")
}

func assertNoMarkers(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte("//merilo:")) {
			t.Errorf("%s contains a //merilo: comment", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenameChangesNothing is AC-13: renaming the machine's types and its
// package yields the same diagnostics at the same lines.
func TestRenameChangesNothing(t *testing.T) {
	dir := fixture(t, "mutation")
	renameMachine(t, dir)
	got, _ := vet(t, dir)
	var want []string
	for _, w := range mutationWant {
		want = append(want, strings.Replace(w, "m/", "q/", 1))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("after renaming:\n got  %q\n want %q", got, want)
	}
}

// renameMachine renames Machine, Event, Outcome and Actions, and moves
// package m to package q, in every file of the fixture.
func renameMachine(t *testing.T, dir string) {
	t.Helper()
	renames := [][2]string{
		{`\bMachine\b`, "Order"},
		{`\bEvent\b`, "Signal"},
		{`\bOutcome\b`, "Verdict"},
		{`\bActions\b`, "Effects"},
		{`"fixture/m"`, `"fixture/q"`},
		{`(?m)^package m$`, "package q"},
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, r := range renames {
			data = regexp.MustCompile(r[0]).ReplaceAll(data, []byte(r[1]))
		}
		if strings.Contains(filepath.ToSlash(path), "/h/") {
			data = regexp.MustCompile(`\bm\.`).ReplaceAll(data, []byte("q."))
		}
		return os.WriteFile(path, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "m"), filepath.Join(dir, "q")); err != nil {
		t.Fatal(err)
	}
}

// TestLoadError checks that a module that does not type-check exits 2.
func TestLoadError(t *testing.T) {
	dir := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "h", "bad.go"), []byte("package h\n\nvar x int = \"s\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(dir, &out); code != 2 {
		t.Fatalf("exit code %d, want 2; output %q", code, out.String())
	}
}
