package impure_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/eitanity/merilo"
	"github.com/eitanity/merilo/internal/impure"
)

type sys struct {
	m     impure.Machine
	steps int
}

func (s *sys) Reset()               { *s = sys{m: impure.New()} }
func (s *sys) Kinds() int           { return 1 }
func (s *sys) Invariants() []string { return []string{"never more than four steps"} }
func (s *sys) Steps(buf []merilo.Step) []merilo.Step {
	if s.steps < 4 {
		buf = append(buf, merilo.Step{})
	}
	return buf
}
func (s *sys) Apply(merilo.Step) error {
	var acts impure.Actions
	var out impure.Outcome
	s.m, acts, out = s.m.Fire(impure.EvStep)
	_ = acts
	if out != impure.Moved {
		return errors.New("not moved")
	}
	s.steps++
	return nil
}
func (s *sys) Key(buf []byte) []byte { return append(s.m.AppendKey(buf), byte(s.steps)) }
func (s *sys) Check() error {
	if s.steps > 4 {
		return errors.New("never more than four steps")
	}
	return nil
}
func (s *sys) Mode() fmt.Stringer          { return mode{} }
func (s *sys) Tuple() string               { return fmt.Sprint(s.m.State()) }
func (s *sys) Settled() bool               { return true }
func (s *sys) Describe(merilo.Step) string { return "step" }

type mode struct{}

func (mode) String() string { return "Normal" }

type expectFail struct{ *testing.T }

func (e *expectFail) Errorf(format string, args ...any) { e.Logf("expected: "+format, args...) }

func TestNondeterminismFound(t *testing.T) {
	r := merilo.Explore(&expectFail{T: t}, &sys{m: impure.New()}, merilo.Options{MaxDepth: 10, MaxStates: 100})
	if r.Violation.Kind != merilo.Nondeterminism {
		t.Fatalf("got %v, want Nondeterminism", r.Violation.Kind)
	}
	if len(r.Violation.Trace) > 2 {
		t.Errorf("trace %v is longer than the shortest replay that diverges", r.Violation.Trace)
	}
}
