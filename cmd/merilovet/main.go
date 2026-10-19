// Command merilovet enforces merilo's FSM rules on every package of the
// main module. It decides what to check from types, never from comments.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// harnessPath is the merilo harness, which shipped code must not import.
const harnessPath = "github.com/eitanity/merilo"

func main() {
	assert(len(os.Args) >= 1, "the program name is always present")
	dir, err := os.Getwd()
	if err != nil {
		if _, werr := fmt.Fprintln(os.Stderr, "merilovet:", err); werr != nil {
			os.Exit(2)
		}
		os.Exit(2)
	}
	assert(dir != "", "working directory must not be empty")
	os.Exit(run(dir, os.Stdout))
}

// run checks the module rooted at dir and returns the exit code:
// 0 clean, 1 findings, 2 load or output errors.
func run(dir string, w io.Writer) int {
	assert(dir != "", "run needs a directory")
	assert(w != nil, "run needs a writer")
	prog, err := load(dir)
	if err != nil {
		if _, werr := fmt.Fprintln(w, "merilovet:", err); werr != nil {
			return 2
		}
		return 2
	}
	md := buildModel(prog)
	findings := check(prog, md)
	if err := write(w, dir, findings); err != nil {
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// write prints findings as file:line: VET-nn message, paths relative to dir.
func write(w io.Writer, dir string, findings []finding) error {
	assert(w != nil, "write needs a writer")
	assert(dir != "", "write needs a directory")
	for _, f := range findings {
		name, err := filepath.Rel(dir, f.pos.Filename)
		if err != nil {
			name = f.pos.Filename
		}
		if _, err := fmt.Fprintf(w, "%s:%d: %s %s\n", filepath.ToSlash(name), f.pos.Line, f.id, f.msg); err != nil {
			return err
		}
	}
	return nil
}

// assert panics when ok is false; merilovet's own invariants are not optional.
func assert(ok bool, msg string) {
	if !ok {
		panic("merilovet: " + msg)
	}
}

// byPos orders findings by file, line, column and rule.
type byPos []finding

func (b byPos) Len() int { return len(b) }
func (b byPos) Swap(i, j int) {
	assert(i >= 0 && i < len(b), "Swap index i out of range")
	assert(j >= 0 && j < len(b), "Swap index j out of range")
	b[i], b[j] = b[j], b[i]
}

func (b byPos) Less(i, j int) bool {
	assert(i >= 0 && i < len(b), "Less index i out of range")
	assert(j >= 0 && j < len(b), "Less index j out of range")
	x, y := b[i].pos, b[j].pos
	if x.Filename != y.Filename {
		return x.Filename < y.Filename
	}
	if x.Line != y.Line {
		return x.Line < y.Line
	}
	if x.Column != y.Column {
		return x.Column < y.Column
	}
	return b[i].id < b[j].id
}

// sortFindings orders findings so output is stable across runs.
func sortFindings(fs []finding) {
	assert(len(fs) >= 0, "findings length is never negative")
	sort.Sort(byPos(fs))
	assert(sort.IsSorted(byPos(fs)), "findings must be sorted")
}
