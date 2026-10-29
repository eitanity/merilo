package main

import (
	"bytes"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
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
	code := run(dir, nil, &out)
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

// TestConcurrentRuns checks two fixtures at once: merilovet keeps every
// setting in its own loader and writes no global, so runs in one process
// do not interfere. make race runs it under the race detector.
func TestConcurrentRuns(t *testing.T) {
	clean, bad := fixture(t), fixture(t, "vet01")
	done := make(chan []string, 2)
	for _, dir := range []string{clean, bad} {
		go func() {
			got, _ := vet(t, dir)
			done <- got
		}()
	}
	n := len(<-done) + len(<-done)
	if n != 1 {
		t.Errorf("two concurrent runs found %d findings, want the one VET-01", n)
	}
}

// TestClean: the base fixture yields nothing.
func TestClean(t *testing.T) {
	expect(t, fixture(t))
}

// TestEachRule: one fixture per rule yields exactly that rule.
func TestEachRule(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"vet01", []string{"m/machine.go:8: VET-01"}},
		{"vet02", []string{"m/extra.go:3: VET-02"}},
		{"vet03", []string{"h/sw.go:6: VET-03"}},
		// default: panic is now a finding twice: the default does not return
		// an outcome, and the switch panics.
		{"vet03panic", []string{"h/sw.go:6: VET-03", "h/sw.go:6: VET-03"}},
		{"vet03zero", []string{"m/enums.go:16: VET-03"}},
		{"vet04", []string{"h/x.go:6: VET-04"}},
		{"vet05", []string{"h/x.go:5: VET-05"}},
		{"vet06", []string{"m/extra.go:4: VET-06"}},
		{"vet06export", []string{"m/extra.go:3: VET-06"}},
		{"vet07", []string{"h/x.go:12: VET-07"}},
		{"vet08", []string{"h/x.go:5: VET-08"}},
		{"vet09", []string{"m/machine.go:7: VET-09"}},
		{"vet17", []string{"h/world_test.go:10: VET-17", "t/t_test.go:5: VET-17"}},
		{"vet10", []string{"h/x.go:7: VET-10"}},
		{"vet11", []string{"h/x.go:3: VET-11"}},
		{"vet12", []string{"h/x.go:3: VET-12"}},
		{"vet13", []string{"h/x.go:3: VET-13"}},
		{"vet14", []string{"h/gun.go:3: VET-14"}},
		{"vet15field", []string{"m/machine.go:5: VET-15"}},
		{"vet15conv", []string{"m/narrow.go:3: VET-15"}},
		// int is 32 bits on 386, arm and TinyGo, so these truncate there.
		{"vet15arch", []string{"m/arch.go:3: VET-15", "m/arch.go:5: VET-15"}},
		// Unguarded ++, x = x + n, and x += n with n unchecked; the guarded
		// form at line 19 is clean.
		{"vet16", []string{"m/extra.go:3: VET-16", "m/extra.go:5: VET-16", "m/extra.go:11: VET-16"}},
		// A pointer-receiver Fire still makes the type a machine, so VET-02
		// reports it and the holder stays checked (VET-12), rather than
		// the machine dropping out of every rule.
		{"ptrfire", []string{"h/revert.go:4: VET-12", "m/fire.go:16: VET-02"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, fixture(t, tc.name), tc.want...)
		})
	}
}

// TestOptionalFeatures: a one-mode machine without
// actions, Fire(E) (Machine, Outcome), and a machine whose event is a
// struct led by its kind, are recognised and checked; the new VET-03 forms
// fire, and a single if on an outcome does not.
func TestOptionalFeatures(t *testing.T) {
	cases := []struct {
		name  string
		cases []string
		extra map[string]string // extra files written into the fixture
		want  []string
	}{
		{name: "single", cases: []string{"single"}},
		{name: "single misuse", cases: []string{"single"}, extra: map[string]string{
			"sh/bad.go": "package sh\n\nimport \"fixture/s\"\n\nfunc Snapshot(g *Gate) s.State {\n\tsaved := g.t\n\treturn saved.State()\n}\n\nfunc Drop(g *Gate) {\n\tg.t, _ = g.t.Fire(s.EvCoin)\n}\n",
		}, want: []string{"sh/bad.go:6: VET-12", "sh/bad.go:11: VET-10"}},
		{name: "payload", cases: []string{"payload"}},
		{name: "payload kind", cases: []string{"payload"}, extra: map[string]string{
			"ph/bad.go": "package ph\n\nimport \"fixture/p\"\n\nfunc Kind(b byte) p.EventKind { return p.EventKind(b) }\n",
		}, want: []string{"ph/bad.go:5: VET-05"}},
		{name: "payload slice", cases: []string{"payload", "payloadbad"}, want: []string{"p/p.go:29: VET-01"}},
		{name: "if chain", cases: []string{"vet03chain"}, want: []string{"h/chain.go:6: VET-03"}},
		{name: "num not largest", cases: []string{"vet03num"}, want: []string{"m/enums.go:18: VET-03"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixture(t, tc.cases...)
			for name, src := range tc.extra {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			expect(t, dir, tc.want...)
		})
	}
}

// mutationWant is every FAIL form of the spec's Mutation rules example;
// the PASS forms in the same file yield nothing.
var mutationWant = []string{
	"h/mut.go:38: VET-13",  // var a Node
	"h/mut.go:39: VET-13",  // Node{}
	"h/mut.go:40: VET-13",  // new(Node)
	"h/mut.go:41: VET-13",  // make([]Node, 3)
	"h/mut.go:46: VET-12",  // saved := n.order
	"h/mut.go:47: VET-12",  // snapshot := *n
	"h/mut.go:48: VET-12",  // ch <- n.order
	"h/mut.go:49: VET-12",  // range value variable
	"h/mut.go:55: VET-12",  // value receiver on Node
	"h/mut.go:58: VET-12",  // existing machine inside a new literal
	"h/mut.go:62: VET-12",  // n.order = m.New()
	"h/mut.go:63: VET-12",  // *n = Node{...}
	"h/mut.go:69: VET-07",  // nodes[j] fired into nodes[i]
	"h/mut.go:71: VET-07",  // other fired into n
	"h/mut.go:80: VET-07",  // receiver with a side effect
	"h/mut.go:86: VET-10",  // outcome discarded
	"h/mut.go:92: VET-10",  // outcome stored in a field
	"h/mut.go:99: VET-10",  // acts overwritten before read
	"h/mut.go:99: VET-10",  // out overwritten before read
	"h/mut.go:111: VET-10", // _ = out
}

// TestMutationExample: each FAIL form yields
// its diagnostic, and the PASS forms, including a read in an if init
// statement and a nested block, yield none.
func TestMutationExample(t *testing.T) {
	expect(t, fixture(t, "mutation"), mutationWant...)
}

// TestHolderDoingIO: a holder that imports time and net and starts
// goroutines, with no marker comments, is checked; only the revert is flagged.
func TestHolderDoingIO(t *testing.T) {
	dir := fixture(t, "io")
	assertNoMarkers(t, dir)
	expect(t, dir, "h/io.go:12: VET-12", "h/io.go:14: VET-12")
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

// TestRenameChangesNothing: renaming the machine's types and its
// package yields the same diagnostics at the same lines.
func TestRenameChangesNothing(t *testing.T) {
	dir := fixture(t, "mutation")
	renameMachine(t, dir)
	got, _ := vet(t, dir)
	want := make([]string, 0, len(mutationWant))
	for _, w := range mutationWant {
		want = append(want, strings.Replace(w, "m/", "q/", 1))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("after renaming:\n got  %q\n want %q", got, want)
	}
}

// renameMachine renames Machine, Event and Actions, and moves
// package m to package q, in every file of the fixture.
func renameMachine(t *testing.T, dir string) {
	t.Helper()
	renames := [][2]string{
		{`\bMachine\b`, "Order"},
		{`\bEvent\b`, "Signal"},
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
	if code := run(dir, nil, &out); code != 2 {
		t.Fatalf("exit code %d, want 2; output %q", code, out.String())
	}
}

// TestNoForeignToolchain: a module whose go.mod asks for a newer Go is
// refused, not downloaded and run, whatever the caller's environment says.
// merilovet starts no go command, so the refusal comes from type-checking
// against the version go.mod names.
func TestNoForeignToolchain(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "auto")
	t.Setenv("GOPROXY", "https://proxy.golang.org")
	dir := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.99.0\n\ntoolchain go1.99.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := run(dir, nil, &out)
	if code != 2 || !strings.Contains(out.String(), "requires newer Go version go1.99") || strings.Contains(out.String(), "downloading") {
		t.Fatalf("exit %d, output %q: want a refusal and no download", code, out.String())
	}
}

// TestOversizedSourceIsALoadError: a file past maxSourceBytes is refused
// with exit 2, not parsed and not a panic.
func TestOversizedSourceIsALoadError(t *testing.T) {
	dir := fixture(t)
	big := append([]byte("package h\n\n// "), bytes.Repeat([]byte("x"), maxSourceBytes)...)
	if err := os.WriteFile(filepath.Join(dir, "h", "big.go"), append(big, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(dir, nil, &out); code != 2 || !strings.Contains(out.String(), "larger than") {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
}

// TestOtherTargetFiles: a holder file that only builds for
// Windows is unchecked on Linux and checked when merilovet runs for Windows,
// which is why make fsm runs it per target.
func TestOtherTargetFiles(t *testing.T) {
	dir := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "h", "revert_windows.go"),
		[]byte("package h\n\nfunc Snapshot(n *Node) Node { return *n }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOS", "linux")
	if got, code := vet(t, dir); code != 0 || len(got) != 0 {
		t.Fatalf("linux run saw the windows file: %v (exit %d)", got, code)
	}
	t.Setenv("GOOS", "windows")
	t.Setenv("GOARCH", "amd64") // a supported windows port on every host
	expect(t, dir, "h/revert_windows.go:3: VET-12")
}

// TestBuildTags: a holder file behind a build constraint, as
// TinyGo and bare-metal code is, is unchecked without its tag and checked
// with -tags naming it.
func TestBuildTags(t *testing.T) {
	dir := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "h", "board.go"),
		[]byte("//go:build tinygo && baremetal\n\npackage h\n\nfunc Snapshot(n *Node) Node { return *n }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, code := vet(t, dir); code != 0 || len(got) != 0 {
		t.Fatalf("a run without tags saw the tinygo file: %v (exit %d)", got, code)
	}
	opts, err := parseArgs([]string{"-tags", "tinygo,baremetal"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(dir, opts.tags, &out); code != 1 || !strings.Contains(out.String(), "h/board.go:5: VET-12") {
		t.Errorf("exit %d, output %q; want VET-12 in the tinygo file", code, out.String())
	}
	for _, bad := range [][]string{{"-tags", "a b"}, {"-tags", "a,,b"}, {"-x"}, {"dir"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("arguments %q accepted", bad)
		}
	}
	if opts, err := parseArgs(nil); err != nil || opts.tags != nil || opts.help || opts.version {
		t.Errorf("no arguments: %+v, %v", opts, err)
	}
}

// TestHelpAndVersion: -h and -help ask for the help, -version for the
// version, and neither is mistaken for the other.
func TestHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"-help"}, {"--help"}} {
		if opts, err := parseArgs(args); err != nil || !opts.help || opts.version {
			t.Errorf("%q: %+v, %v; want help", args, opts, err)
		}
	}
	if opts, err := parseArgs([]string{"-version"}); err != nil || !opts.version || opts.help {
		t.Errorf("-version: %+v, %v; want version", opts, err)
	}
	var out bytes.Buffer
	if code := say(&out, usage); code != 0 || !strings.Contains(out.String(), "-tags tag,...") || !strings.Contains(out.String(), "Exit status") || !strings.Contains(out.String(), "VET-17") {
		t.Errorf("help exit %d:\n%s", code, out.String())
	}
}

// TestVersionOf: the version is merilo's, whether merilo is the main
// module or a dependency of an adopter's.
func TestVersionOf(t *testing.T) {
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"no build info", nil, "(devel)"},
		{"tagged", &debug.BuildInfo{Main: debug.Module{Path: harnessPath, Version: "v1.2.3"}}, "v1.2.3"},
		{"no version control", &debug.BuildInfo{Main: debug.Module{Path: harnessPath}}, "(devel)"},
		{"a dependency", &debug.BuildInfo{Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{{Path: "example.com/other", Version: "v9.0.0"}, {Path: harnessPath, Version: "v1.4.0"}}}, "v1.4.0"},
		{"replaced", &debug.BuildInfo{Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{{Path: harnessPath, Version: "v1.4.0", Replace: &debug.Module{Path: "../merilo"}}}}, "(devel)"},
		{"absent", &debug.BuildInfo{Main: debug.Module{Path: "example.com/app"}}, "(devel)"},
	}
	for _, c := range cases {
		if got := versionOf(c.info); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestNoGoCommand: merilovet loads and checks a module with no go command
// on PATH at all, so it cannot run one.
func TestNoGoCommand(t *testing.T) {
	t.Setenv("PATH", "")
	expect(t, fixture(t, "vet12"), "h/x.go:3: VET-12")
}

// TestDependencies: merilovet checks the module's code, not its
// dependencies. An import outside the module and the standard library needs
// no go.mod require, no module cache and no network; the holder that uses it
// is still checked. A package that imports nothing outside stays strict, so a
// real type error there is a load error.
func TestDependencies(t *testing.T) {
	t.Setenv("GOMODCACHE", t.TempDir()) // empty: nothing can come from it
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "h", "dep.go"), "package h\n\nimport (\n\t\"example.com/Lib\"\n\t\"gopkg.in/yaml.v3\"\n)\n\nfunc Leak(n *Node) int {\n\tlib.Keep(n.order)\n\treturn yaml.Count()\n}\n")
	expect(t, dir, "h/dep.go:9: VET-12")

	if err := os.Remove(filepath.Join(dir, "h", "dep.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "h", "broken.go"), "package h\n\nfunc Broken() int { return \"not an int\" }\n")
	var out bytes.Buffer
	if code := run(dir, nil, &out); code != 2 || !strings.Contains(out.String(), "type-checking fixture/h") {
		t.Fatalf("a type error in a package with no outside imports: exit %d, output %q", code, out.String())
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRulePhaseAllocatesNothing: once the module is
// loaded and its model built, applying every rule to it allocates nothing.
// merilo's own module is the input: it is clean, and it holds machines,
// holders and adapters of every shape the rules know.
func TestRulePhaseAllocatesNothing(t *testing.T) {
	flt := &faults{}
	prog, err := load(flt, filepath.Join("..", ".."), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newChecker(flt, prog, buildModel(flt, prog))
	if got := c.run(); len(got) != 0 || len(flt.msgs) != 0 {
		t.Fatalf("the module is not clean: %d findings, faults %q", len(got), flt.msgs)
	}
	// AllocsPerRun counts every allocation in the process and reports the
	// mean rounded down. A rule that allocates does so on every run; ten
	// runs keep a few stray allocations by the runtime, left over from the
	// fixtures before, from reading as one.
	if n := testing.AllocsPerRun(10, func() { c.run() }); n != 0 {
		t.Errorf("the rule phase allocates %v times", n)
	}
}

// TestFSMSource keeps merilovet's carried copy of fsm identical to the
// package adopters import.
func TestFSMSource(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "fsm", "fsm.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != fsmSource {
		t.Fatal("fsmSource differs from fsm/fsm.go; copy fsm/fsm.go into it")
	}
}

// TestInternalFaultExits2 is the fault path: an assertion inside merilovet
// that fails records a fault instead of panicking, and the run then exits
// 2 printing the fault, never the findings it can no longer trust.
func TestInternalFaultExits2(t *testing.T) {
	flt := &faults{}
	if md := buildModel(flt, nil); md != nil {
		t.Fatal("buildModel without a program returned a model")
	}
	if len(flt.msgs) != 1 {
		t.Fatalf("faults = %q, want the one buildModel recorded", flt.msgs)
	}
	findings := []finding{{pos: token.Position{Filename: "/m/x.go", Line: 3}, id: "VET-03", msg: "untrusted"}}
	var out bytes.Buffer
	if code := report(&out, "/m", flt, findings); code != 2 {
		t.Fatalf("exit %d with a fault, want 2", code)
	}
	if got, want := out.String(), "merilovet: internal error: buildModel needs a program\n"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}
	out.Reset()
	if code := report(&out, "/m", &faults{}, findings); code != 1 || !strings.Contains(out.String(), "x.go:3: VET-03") {
		t.Errorf("without a fault: exit %d, output %q; want 1 and the finding", code, out.String())
	}
}

// TestFaultsAreBounded: a run that keeps failing its own checks records at
// most maxFaults, so a broken invariant inside a loop cannot grow memory.
func TestFaultsAreBounded(t *testing.T) {
	flt := &faults{}
	for range 2 * maxFaults {
		flt.fault("again")
	}
	if len(flt.msgs) != maxFaults {
		t.Errorf("recorded %d faults, want %d", len(flt.msgs), maxFaults)
	}
	var nilFaults *faults
	nilFaults.fault("ignored") // a nil recorder is refused, not a crash
	if code := report(nil, "/m", flt, nil); code != 2 {
		t.Errorf("report with no writer: exit %d, want 2", code)
	}
}
