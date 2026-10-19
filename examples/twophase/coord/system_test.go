package coord_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/eitanity/merilo"
	"github.com/eitanity/merilo/examples/twophase/coord"
)

const (
	kStart merilo.Kind = iota
	kYes
	kNo
	kTick
	kResume
	numKinds
)

const (
	voteNone uint8 = iota
	voteYes
	voteNo
)

// SysMode mirrors the coordinator's mode at system level.
type SysMode string

func (m SysMode) String() string { return string(m) }

// sys is one coordinator and two participants who vote once each, after
// the coordinator asks.
type sys struct {
	bug      coord.Bug
	c        coord.Machine
	timer    bool
	asked    bool
	votes    [coord.Participants]uint8
	decision coord.Action
}

func newSys(b coord.Bug) *sys {
	s := &sys{bug: b, c: coord.New(b)}
	s.Reset()
	return s
}

func (s *sys) Reset() { *s = sys{bug: s.bug, c: coord.New(s.bug)} }

func (s *sys) Kinds() int { return int(numKinds) }

func (s *sys) Invariants() []string {
	return []string{"commit only when every participant voted yes", "the decision matches the coordinator's state"}
}

func (s *sys) Steps(buf []merilo.Step) []merilo.Step {
	if s.c.State() == coord.Init {
		buf = append(buf, merilo.Step{Kind: kStart})
	}
	for i, v := range s.votes {
		if s.asked && v == voteNone {
			buf = append(buf, merilo.Step{Kind: kYes, Arg: i}, merilo.Step{Kind: kNo, Arg: i})
		}
	}
	if s.timer {
		buf = append(buf, merilo.Step{Kind: kTick})
	}
	if s.c.State() == coord.Recovering {
		buf = append(buf, merilo.Step{Kind: kResume})
	}
	return buf
}

func (s *sys) Apply(st merilo.Step) error {
	before := s.c.Mode()
	switch st.Kind {
	case kStart:
		s.fire(coord.EvStart)
	case kYes:
		s.votes[st.Arg] = voteYes
		s.fire(coord.EvYes)
	case kNo:
		s.votes[st.Arg] = voteNo
		s.fire(coord.EvNo)
	case kTick:
		s.timer = false
		s.fire(coord.EvTimeout)
	case kResume:
		s.fire(coord.EvResume)
	default:
		return fmt.Errorf("unknown step kind %d", st.Kind)
	}
	if after := s.c.Mode(); after != before && !sysModeAllowed(before, after) {
		return fmt.Errorf("%w: %v -> %v", merilo.ErrModeChange, before, after)
	}
	return nil
}

// sysModeAllowed is the system's own table of mode changes; it agrees
// with the coordinator's, so only a machine panic can report AC-03.
func sysModeAllowed(from, to coord.Mode) bool {
	switch from {
	case coord.Normal:
		return to == coord.Degraded || to == coord.Closed
	case coord.Degraded:
		return to == coord.Recovery || to == coord.Closed || to == coord.Normal
	case coord.Recovery:
		return to == coord.Normal
	default:
		return false
	}
}

func (s *sys) fire(e coord.Event) {
	var acts coord.Actions
	var out coord.Outcome
	s.c, acts, out = s.c.Fire(e)
	if out == coord.Rejected {
		panic(fmt.Sprintf("coordinator rejected %d", e))
	}
	for i := range acts.Len() {
		switch a := acts.At(i); a {
		case coord.ActPrepare:
			s.asked = true
		case coord.ActCommit, coord.ActAbort:
			s.decision = a
		case coord.ActStartTimer:
			s.timer = true
		case coord.ActCancelTimer:
			s.timer = false
		case coord.ActModeChanged, coord.ActNone:
		}
	}
}

func (s *sys) Key(buf []byte) []byte {
	buf = s.c.AppendKey(buf)
	buf = append(buf, b(s.timer), b(s.asked), byte(s.decision))
	return append(buf, s.votes[:]...)
}

func b(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func (s *sys) Check() error {
	if s.decision == coord.ActCommit {
		for _, v := range s.votes {
			if v != voteYes {
				return errors.New("commit only when every participant voted yes")
			}
		}
	}
	if (s.decision == coord.ActCommit) != (s.c.State() == coord.Committed) ||
		(s.decision == coord.ActAbort) != (s.c.State() == coord.Aborted) {
		return errors.New("the decision matches the coordinator's state")
	}
	return nil
}

func (s *sys) Mode() fmt.Stringer { return SysMode(s.c.Mode().String()) }
func (s *sys) Tuple() string      { return s.c.State().String() }

func (s *sys) Settled() bool {
	switch s.c.State() {
	case coord.Init, coord.Committed, coord.Aborted:
		return true
	default:
		return false
	}
}

func (s *sys) Describe(st merilo.Step) string {
	switch st.Kind {
	case kStart:
		return "start"
	case kYes:
		return fmt.Sprintf("p%d votes yes", st.Arg)
	case kNo:
		return fmt.Sprintf("p%d votes no", st.Arg)
	case kTick:
		return "timeout"
	case kResume:
		return "resume"
	default:
		return fmt.Sprintf("kind %d", st.Kind)
	}
}

var opts = merilo.Options{MaxDepth: 20, MaxStates: 10000, RequireComplete: true}

// AC-01: the seeded commit-on-timeout bug is found with the hand-derived
// shortest trace: start, one yes vote, timeout.
func TestSeededCommitOnTimeout(t *testing.T) {
	r := explore(t, newSys(coord.CommitOnTimeout))
	if r.Violation.Kind != merilo.Invariant || len(r.Violation.Trace) != 3 {
		t.Errorf("got %v with trace %v, want an invariant violation of length 3", r.Violation.Kind, r.Violation.Trace)
	}
}

// AC-03: the seeded Degraded-to-Normal change panics in the machine and
// is reported with its shortest trace: start, timeout, one yes vote.
func TestSeededSkipRecovery(t *testing.T) {
	r := explore(t, newSys(coord.SkipRecovery))
	if r.Violation.Kind != merilo.Panic || len(r.Violation.Trace) != 3 {
		t.Errorf("got %v with trace %v (%s), want a panic of length 3", r.Violation.Kind, r.Violation.Trace, r.Violation.Detail)
	}
}

// explore runs the search expecting a violation, so a seeded bug's
// report does not fail the test that seeded it.
func explore(t *testing.T, s *sys) merilo.Result {
	r := merilo.Explore(&expectFail{T: t}, s, opts)
	if !r.Violation.Found() {
		t.Fatal("seeded bug not found")
	}
	return r
}

type expectFail struct{ *testing.T }

func (e *expectFail) Errorf(format string, args ...any) { e.Logf("expected: "+format, args...) }

// AC-04: the correct coordinator verifies completely.
func TestCorrect(t *testing.T) {
	r := merilo.Explore(t, newSys(coord.NoBug), opts)
	if !r.Complete || r.Violation.Found() {
		t.Fatalf("complete=%t violation=%v", r.Complete, r.Violation.Kind)
	}
	want := map[[2]string]bool{
		{"Normal", "Degraded"}: true, {"Normal", "Closed"}: true, {"Degraded", "Recovering"}: true,
		{"Degraded", "Closed"}: true, {"Recovering", "Normal"}: true,
	}
	for _, e := range r.ModeEdges {
		delete(want, e)
	}
	if len(want) != 0 {
		t.Errorf("declared mode changes never taken: %v", want)
	}
}

// AC-06: the seeds alone reach every reachable mode edge.
func TestCoverageFromSeeds(t *testing.T) {
	r := merilo.Explore(t, newSys(coord.NoBug), opts)
	merilo.Coverage(t, newSys(coord.NoBug), "FuzzCorrect", r,
		merilo.FuzzOptions{MaxDepth: 30, Seeds: r.Witnesses},
		merilo.CoverageMin{Modes: 100, ModeEdges: 100, Tuples: 100})
}

func FuzzCorrect(f *testing.F) {
	r := merilo.Explore(f, newSys(coord.NoBug), opts)
	merilo.Fuzz(f, newSys(coord.NoBug), merilo.FuzzOptions{MaxDepth: 30, Seeds: r.Witnesses})
}

// FuzzSeededShallow is AC-05: the explorer is bounded below the bug's
// depth, so only fuzzing from its witnesses can find the bug. Run it with
// go test -run '^$' -fuzz FuzzSeededShallow; it is expected to fail.
func FuzzSeededShallow(f *testing.F) {
	shallow := merilo.Options{MaxDepth: 2, MaxStates: 10000}
	r := merilo.Explore(f, newSys(coord.CommitOnTimeout), shallow)
	if r.Violation.Found() {
		f.Fatal("bounded search should not reach the bug")
	}
	merilo.Fuzz(f, newSys(coord.CommitOnTimeout), merilo.FuzzOptions{MaxDepth: 30, Seeds: r.Witnesses})
}
