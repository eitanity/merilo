package coord_test

import (
	"errors"
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/examples/twophase/coord"
	"code.eitanity.com/merilo/fsm"
)

// Step kinds. A vote has one kind per participant: kYes+i is participant
// i voting yes.
const (
	kStart = iota
	kTick
	kResume
	kYes
	kNo      = kYes + coord.Participants
	numKinds = kNo + coord.Participants
)

// slot splits kind k into its step and, for a vote, the participant.
func slot(k int) (step, i int) {
	switch {
	case k >= kNo:
		return kNo, k - kNo
	case k >= kYes:
		return kYes, k - kYes
	}
	return k, 0
}

// vote is a participant's ballot.
type vote uint8

const (
	voteNone vote = iota
	voteYes
	voteNo
)

// sys is one coordinator and two participants who vote once each, after
// the coordinator asks.
type sys struct {
	bug      coord.Bug
	c        coord.Machine
	timer    bool
	asked    bool
	votes    [coord.Participants]vote
	decision coord.Actions
}

func newSys(b coord.Bug) *sys {
	s := &sys{bug: b, c: coord.New(b)}
	s.Reset()
	return s
}

func (s *sys) Reset() { *s = sys{bug: s.bug, c: coord.New(s.bug)} }

func (s *sys) Kinds() int { return numKinds }

func (s *sys) Invariants() []string {
	return []string{"commit only when every participant voted yes", "the decision matches the coordinator's state"}
}

func (s *sys) Enabled(k int) bool {
	if k < 0 || k >= numKinds {
		return false
	}
	step, i := slot(k)
	switch step {
	case kStart:
		return s.c.State() == coord.Init
	case kTick:
		return s.timer
	case kResume:
		return s.c.State() == coord.Recovering
	case kYes, kNo:
		return s.asked && s.votes[i] == voteNone
	}
	return false
}

func (s *sys) Apply(k int) error {
	before := s.c.Mode()
	step, i := slot(k)
	switch step {
	case kStart:
		if err := s.fire(coord.EvStart); err != nil {
			return err
		}
	case kYes:
		s.votes[i] = voteYes
		if err := s.fire(coord.EvYes); err != nil {
			return err
		}
	case kNo:
		s.votes[i] = voteNo
		if err := s.fire(coord.EvNo); err != nil {
			return err
		}
	case kTick:
		s.timer = false
		if err := s.fire(coord.EvTimeout); err != nil {
			return err
		}
	case kResume:
		if err := s.fire(coord.EvResume); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown step kind %d", k)
	}
	if after := s.c.Mode(); after != before && !sysModeAllowed(before, after) {
		return fmt.Errorf("%w: %v -> %v", merilo.ErrModeChange, before, after)
	}
	return nil
}

// sysModeAllowed is the system's own table of mode changes; it agrees
// with the coordinator's, so only a machine panic can report the seeded mode change.
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

func (s *sys) fire(e coord.Event) error {
	var acts coord.Actions
	var out fsm.Outcome
	s.c, acts, out = s.c.Fire(e)
	if out == fsm.Rejected || out == fsm.Invalid {
		return fmt.Errorf("coordinator refused event %d with outcome %d", e, out)
	}
	for a := coord.Actions(1); a != 0; a <<= 1 {
		switch acts & a {
		case coord.ActPrepare:
			s.asked = true
		case coord.ActCommit, coord.ActAbort:
			s.decision = a
		case coord.ActStartTimer:
			s.timer = true
		case coord.ActCancelTimer:
			s.timer = false
		case coord.ActModeChanged:
		}
	}
	return nil
}

// World is the whole adapter: the coordinator, the timer and the votes.
func (s *sys) World() any { return *s }

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

func (s *sys) Tuple() string { return s.c.State().String() }

func (s *sys) Settled() bool {
	switch s.c.State() {
	case coord.Init, coord.Committed, coord.Aborted:
		return true
	default:
		return false
	}
}

func (s *sys) Describe(k int) string {
	step, i := slot(k)
	switch step {
	case kStart:
		return "start"
	case kYes:
		return fmt.Sprintf("p%d votes yes", i)
	case kNo:
		return fmt.Sprintf("p%d votes no", i)
	case kTick:
		return "timeout"
	case kResume:
		return "resume"
	default:
		return fmt.Sprintf("kind %d", k)
	}
}

var opts = merilo.Options{MaxStates: 10000, RequireComplete: true}

// The seeded commit-on-timeout bug is found with the hand-derived
// shortest trace: start, one yes vote, timeout.
func TestSeededCommitOnTimeout(t *testing.T) {
	r := explore(t, newSys(coord.CommitOnTimeout))
	if r.Violation.Kind != merilo.Invariant || len(r.Violation.Trace) != 3 {
		t.Errorf("got %v with trace %v, want an invariant violation of length 3", r.Violation.Kind, r.Violation.Trace)
	}
}

// The machine refuses the seeded Degraded-to-Normal change as
// Invalid, the adapter reports it, and the explorer gives its shortest
// trace: start, timeout, one yes vote.
func TestSeededSkipRecovery(t *testing.T) {
	r := explore(t, newSys(coord.SkipRecovery))
	if r.Violation.Kind != merilo.ApplyError || len(r.Violation.Trace) != 3 {
		t.Errorf("got %v with trace %v (%s), want an apply error of length 3", r.Violation.Kind, r.Violation.Trace, r.Violation.Detail)
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

// The correct coordinator verifies completely.
func TestCorrect(t *testing.T) {
	r := merilo.Explore(t, newSys(coord.NoBug), opts)
	if !r.Complete || r.Violation.Found() {
		t.Fatalf("complete=%t violation=%v", r.Complete, r.Violation.Kind)
	}
}

func FuzzCorrect(f *testing.F) {
	merilo.Fuzz(f, newSys(coord.NoBug), merilo.FuzzOptions{MaxDepth: 30, Explore: opts})
}

// FuzzSeededShallow: the explorer stores too few worlds to reach
// the bug's depth, so only fuzzing from its witnesses can find the bug. Run it with
// go test -run '^$' -fuzz FuzzSeededShallow; it is expected to fail.
func FuzzSeededShallow(f *testing.F) {
	shallow := merilo.Options{MaxStates: 8} // every world within 2 steps; the bug is 3 away
	r := merilo.Explore(f, newSys(coord.CommitOnTimeout), shallow)
	if r.Violation.Found() {
		f.Fatal("bounded search should not reach the bug")
	}
	merilo.Fuzz(f, newSys(coord.CommitOnTimeout), merilo.FuzzOptions{MaxDepth: 30, Explore: shallow})
}
