// Command merilovet enforces merilo's FSM rules on every package of the
// main module. It decides what to check from types, never from comments.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
)

// harnessPath is the merilo harness, which shipped code must not import.
const harnessPath = "code.eitanity.com/merilo"

// maxFaults bounds the internal faults one run records; the first is the
// one that matters.
const maxFaults = 64

// faults records merilovet's own broken invariants. An assertion that
// fails records one and returns its function's refusal value, so the run
// finishes without a panic and exits 2.
type faults struct {
	msgs []string
}

// fault records msg.
func (f *faults) fault(msg string) {
	if f == nil {
		return
	}
	if len(f.msgs) >= maxFaults {
		return
	}
	f.msgs = append(f.msgs, msg)
}

// internalError is the error an assertion returns from a function that
// returns an error.
func internalError(msg string) error {
	return errors.New("internal error: " + msg)
}

func main() {
	if len(os.Args) < 1 {
		os.Exit(2)
	}
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		if _, werr := fmt.Fprintln(os.Stderr, "merilovet:", err); werr != nil {
			os.Exit(2)
		}
		os.Exit(2)
	}
	switch {
	case opts.help:
		os.Exit(say(os.Stdout, usage))
	case opts.version:
		os.Exit(say(os.Stdout, "merilovet "+version()))
	}
	dir, err := os.Getwd()
	if err != nil || dir == "" {
		if _, werr := fmt.Fprintln(os.Stderr, "merilovet:", err); werr != nil {
			os.Exit(2)
		}
		os.Exit(2)
	}
	os.Exit(run(dir, opts.tags, os.Stdout))
}

// usage is what -h prints.
const usage = `usage: merilovet [-tags tag,...]
       merilovet -version

merilovet checks every package of the module in the current directory,
and their tests, against merilo's FSM rules. It reads the files GOOS and
GOARCH select, so run it once for each target you ship.

  -tags tag,...  build tags, comma separated, so files behind a
                 constraint such as //go:build tinygo are checked too
  -version       print merilovet's version and exit
  -h, -help      print this help and exit

Each finding is printed as file:line: VET-nn message, with the path
relative to the module root.

  VET-01  machine, event and enum types hold no pointer, interface,
          channel, func, slice or map; an event struct starts with its kind
  VET-02  every method on a machine has a value receiver
  VET-03  enums start with an unexported zero sentinel and end with their
          num count; every switch on an enum is complete, ends in a
          default that returns, and never panics; no if chain over an enum
  VET-04  no struct field holds a mode; derive it from the state
  VET-05  no conversion to a state, event or mode from a non-constant
  VET-06  machine fields are written only in New and the machine's
          methods; only New and Fire return a machine
  VET-07  a stored machine is replaced only by m, out = m.Fire(e)
  VET-08  no address of a machine or of its fields
  VET-09  every field of a machine is unexported
  VET-10  Fire's outcome, and its actions, go to local variables that are
          read in the same block and never discarded
  VET-11  no non-test file imports the merilo harness
  VET-12  no existing machine is copied, and a fresh one never overwrites
          a live one
  VET-13  every machine is built with New: no bare var, omitted literal
          field, new or make
  VET-14  a type with a Fire method has the whole machine shape
  VET-15  machine and event fields hold only int64, uint64 or an enum; no
          conversion narrows an integer on any target
  VET-16  arithmetic on a machine field comes after a comparison of the
          field, and of any non-constant operand, with a constant
  VET-17  a World method returns a value == can compare: no pointer,
          interface, channel, func, slice or map

Exit status: 0 no findings, 1 findings, 2 merilovet could not finish.`

// options is what the command line asks for.
type options struct {
	tags    []string
	help    bool
	version bool
}

// parseArgs reads the command line: -h or -help, -version, and -tags,
// build tags comma separated as go build takes them, so files behind a
// constraint such as //go:build tinygo are checked too.
func parseArgs(args []string) (options, error) {
	fs := flag.NewFlagSet("merilovet", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	list := fs.String("tags", "", "comma-separated build tags")
	ver := fs.Bool("version", false, "print the version")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{help: true}, nil
		}
		return options{}, fmt.Errorf("%w; run merilovet -h for help", err)
	}
	if rest := fs.Args(); len(rest) != 0 {
		return options{}, fmt.Errorf("unexpected argument %q; run merilovet -h for help", rest[0])
	}
	if list == nil || ver == nil {
		return options{}, internalError("every flag has a value")
	}
	if *list == "" {
		return options{version: *ver}, nil
	}
	tags := strings.Split(*list, ",")
	for _, tag := range tags {
		if !validTag(tag) {
			return options{}, fmt.Errorf("invalid build tag %q: a tag holds only letters, digits, underscores and dots", tag)
		}
	}
	return options{tags: tags, version: *ver}, nil
}

// say writes msg and a newline to w, and returns the exit code: 0, or 2
// when it cannot write.
func say(w io.Writer, msg string) int {
	if w == nil {
		return 2
	}
	if _, err := fmt.Fprintln(w, msg); err != nil {
		return 2
	}
	return 0
}

// devel is the version of a build with no version to report.
const devel = "(devel)"

// version is the version of merilo this merilovet was built from.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return devel
	}
	return versionOf(info)
}

// versionOf finds merilo's version in info. Built by go install at a
// tag, or in a tagged checkout, it is the tag; between tags, a
// pseudo-version naming the commit; without version control, (devel).
// Run from an adopter's module, merilo is a dependency, so its version is
// the one that module requires, or its replacement's.
func versionOf(info *debug.BuildInfo) string {
	if info == nil {
		return devel
	}
	if info.Main.Path == harnessPath {
		return orDevel(info.Main.Version)
	}
	for _, dep := range info.Deps {
		if dep == nil || dep.Path != harnessPath {
			continue
		}
		if r := dep.Replace; r != nil {
			return orDevel(r.Version)
		}
		return orDevel(dep.Version)
	}
	return devel
}

// orDevel is v, or devel when v is empty.
func orDevel(v string) string {
	if v == "" {
		return devel
	}
	return v
}

// validTag reports whether tag is a build tag //go:build lines can name.
func validTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, r := range tag {
		letter := r == '_' || r == '.' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9'
		if !letter {
			return false
		}
	}
	return true
}

// run checks the module rooted at dir, its files chosen for the target
// and the build tags, and returns the exit code: 0 clean, 1 findings, 2
// load, output or internal errors.
func run(dir string, tags []string, w io.Writer) int {
	if w == nil {
		return 2
	}
	if dir == "" {
		if _, err := fmt.Fprintln(w, "merilovet: no directory"); err != nil {
			return 2
		}
		return 2
	}
	flt := &faults{}
	prog, err := load(flt, dir, tags)
	if err != nil {
		if _, werr := fmt.Fprintln(w, "merilovet:", err); werr != nil {
			return 2
		}
		return 2
	}
	md := buildModel(flt, prog)
	return report(w, dir, flt, check(flt, prog, md))
}

// report writes the run's outcome and returns its exit code. An internal
// fault outranks every finding: the findings of a run whose own
// invariants broke cannot be trusted, so only the faults are printed.
func report(w io.Writer, dir string, flt *faults, findings []finding) int {
	if w == nil || flt == nil {
		return 2
	}
	if len(flt.msgs) > 0 {
		return writeFaults(w, flt)
	}
	if err := write(w, dir, findings); err != nil {
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// writeFaults prints the internal faults and returns exit code 2.
func writeFaults(w io.Writer, flt *faults) int {
	if w == nil || flt == nil {
		return 2
	}
	for _, msg := range flt.msgs {
		if _, err := fmt.Fprintln(w, "merilovet: internal error:", msg); err != nil {
			return 2
		}
	}
	return 2
}

// write prints findings as file:line: VET-nn message, paths relative to dir.
func write(w io.Writer, dir string, findings []finding) error {
	if w == nil {
		return internalError("write needs a writer")
	}
	if dir == "" {
		return internalError("write needs a directory")
	}
	for _, f := range findings {
		name, err := filepath.Rel(dir, f.pos.Filename)
		if err != nil {
			name = f.pos.Filename
		}
		if _, err := fmt.Fprintf(w, "%s:%d: %s %s\n", filepath.ToSlash(name), f.pos.Line, f.id, f.msg); err != nil {
			return fmt.Errorf("writing findings: %w", err)
		}
	}
	return nil
}

// byPos orders findings by file, line, column and rule.
type byPos struct {
	list []finding
	flt  *faults
}

func (b byPos) Len() int { return len(b.list) }

func (b byPos) Swap(i, j int) {
	if i < 0 || i >= len(b.list) || j < 0 || j >= len(b.list) {
		b.flt.fault("Swap index out of range")
		return
	}
	b.list[i], b.list[j] = b.list[j], b.list[i]
}

func (b byPos) Less(i, j int) bool {
	if i < 0 || i >= len(b.list) || j < 0 || j >= len(b.list) {
		b.flt.fault("Less index out of range")
		return false
	}
	x, y := b.list[i].pos, b.list[j].pos
	if x.Filename != y.Filename {
		return x.Filename < y.Filename
	}
	if x.Line != y.Line {
		return x.Line < y.Line
	}
	if x.Column != y.Column {
		return x.Column < y.Column
	}
	return b.list[i].id < b.list[j].id
}

// sortFindings orders the findings so output is stable across runs.
func (c *checker) sortFindings() {
	if c == nil {
		return
	}
	if len(c.findings) < 2 {
		return
	}
	c.order = byPos{list: c.findings, flt: c.flt}
	sort.Sort(&c.order)
	if sorted := sort.IsSorted(&c.order); !sorted {
		c.flt.fault("findings must be sorted")
	}
}
