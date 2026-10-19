package merilo

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// CoverageMin sets the minimum percentage, 1 to 100, that the seeds and
// the committed corpus must reach. There are no defaults.
type CoverageMin struct {
	Modes, ModeEdges, Tuples int
}

// Coverage replays the seeds and the committed corpus in
// testdata/fuzz/<fuzzName>/ and fails t when they reach less than min of
// what explored found reachable (CV-01 to CV-03).
func Coverage(t testing.TB, sys System, fuzzName string, explored Result, o FuzzOptions, min CoverageMin) {
	t.Helper()
	if err := validateCoverage(sys, fuzzName, o, min); err != nil {
		t.Fatalf("%v", err)
	}
	obs := &observer{tuples: map[string]bool{}, modes: map[string]bool{}, edges: map[[2]string]bool{}}
	r := newRunner(sys, o.MaxDepth)
	for _, s := range o.Seeds {
		r.run(s.FuzzBytes(sys), obs)
	}
	assert(len(obs.modes) > 0 || len(o.Seeds) == 0, "seeds reached no mode")
	corpus := readCorpus(t, filepath.Join("testdata", "fuzz", fuzzName))
	for _, in := range corpus {
		r.run(in, obs)
	}
	edges := make([]string, len(explored.ModeEdges))
	for i, e := range explored.ModeEdges {
		edges[i] = e[0] + " -> " + e[1]
	}
	assert(len(edges) == len(explored.ModeEdges), "lost a mode edge")
	mm := missing(explored.Modes, obs.modes)
	me := missingEdges(explored.ModeEdges, obs.edges)
	mt := missing(explored.Tuples, obs.tuples)
	assert(len(mm) <= len(explored.Modes) && len(mt) <= len(explored.Tuples), "more missing than explored")
	t.Logf("merilo: seeds %d corpus %d modes %d/%d modeEdges %d/%d tuples %d/%d",
		len(o.Seeds), len(corpus), len(explored.Modes)-len(mm), len(explored.Modes),
		len(edges)-len(me), len(edges), len(explored.Tuples)-len(mt), len(explored.Tuples))
	gate(t, "modes", mm, len(explored.Modes), min.Modes)
	gate(t, "mode edges", me, len(edges), min.ModeEdges)
	gate(t, "tuples", mt, len(explored.Tuples), min.Tuples)
}

func validateCoverage(sys System, fuzzName string, o FuzzOptions, min CoverageMin) error {
	assert(sys != nil, "validateCoverage without System")
	if err := validateFuzz(sys, o); err != nil {
		return err
	}
	for _, p := range [3]int{min.Modes, min.ModeEdges, min.Tuples} {
		if p < 1 || p > 100 {
			return fmt.Errorf("merilo: every CoverageMin field must be set, from 1 to 100; it has no default")
		}
	}
	if fuzzName == "" || strings.ContainsAny(fuzzName, `/\`) {
		return fmt.Errorf("merilo: Coverage needs the fuzz function's name, such as FuzzOrders")
	}
	return nil
}

func missing(all []string, got map[string]bool) []string {
	assert(got != nil, "missing against a nil set")
	var out []string
	for _, s := range all {
		if !got[s] {
			out = append(out, s)
		}
	}
	assert(len(out) <= len(all), "more missing than listed")
	return out
}

func missingEdges(all [][2]string, got map[[2]string]bool) []string {
	assert(got != nil, "missingEdges against a nil set")
	var out []string
	for _, e := range all {
		if !got[e] {
			out = append(out, e[0]+" -> "+e[1])
		}
	}
	assert(len(out) <= len(all), "more missing than listed")
	return out
}

// gate fails t when fewer than min percent of total were reached.
func gate(t testing.TB, what string, unreached []string, total, min int) {
	t.Helper()
	assert(min >= 1 && min <= 100, "unvalidated minimum")
	assert(len(unreached) <= total, "more unreached than exist")
	if total == 0 || (total-len(unreached))*100 >= min*total {
		return
	}
	t.Errorf("merilo: %s coverage %d/%d is below %d%%; unreached: %s",
		what, total-len(unreached), total, min, strings.Join(unreached, "; "))
}

// readCorpus reads every input in dir, which Go writes in its
// "go test fuzz v1" format. A missing directory is an empty corpus.
func readCorpus(t testing.TB, dir string) [][]byte {
	t.Helper()
	assert(dir != "", "corpus without a directory")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("merilo: %v", err)
	}
	var out [][]byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("merilo: %v", err)
		}
		in, err := parseCorpus(string(data))
		if err != nil {
			t.Fatalf("merilo: corpus file %s: %v", e.Name(), err)
		}
		out = append(out, in)
	}
	assert(len(out) <= len(entries), "more inputs than files")
	return out
}

func parseCorpus(text string) ([]byte, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	assert(len(lines) >= 1, "Split returned nothing")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "go test fuzz v1" {
		return nil, fmt.Errorf("not a single-argument \"go test fuzz v1\" file")
	}
	arg := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(arg, "[]byte(") || !strings.HasSuffix(arg, ")") {
		return nil, fmt.Errorf("argument is not a []byte literal")
	}
	assert(len(arg) >= len("[]byte()"), "literal shorter than its wrapper")
	s, err := strconv.Unquote(arg[len("[]byte(") : len(arg)-1])
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}
