package fsm

import (
	"errors"
	"math"
	"testing"
)

func TestOutcomeErr(t *testing.T) {
	for _, tc := range []struct {
		out     Outcome
		name    string
		refused bool
	}{
		{outcomeNone, "invalid", true},
		{Moved, "Moved", false},
		{Ignored, "Ignored", false},
		{Rejected, "Rejected", true},
		{Invalid, "Invalid", true},
		{Invalid + 1, "invalid", true},
		{-1, "invalid", true},
	} {
		if got := tc.out.String(); got != tc.name {
			t.Errorf("Outcome(%d).String() = %q, want %q", int64(tc.out), got, tc.name)
		}
		err := tc.out.Err()
		if (err != nil) != tc.refused {
			t.Errorf("Outcome(%d).Err() = %v, want refused=%t", int64(tc.out), err, tc.refused)
		}
		var r Refusal
		if tc.refused && !errors.As(err, &r) {
			t.Errorf("Outcome(%d).Err() = %v, want a Refusal", int64(tc.out), err)
		}
	}
	if got := Rejected.Err().Error(); got != "rejected" {
		t.Errorf("Rejected.Err() = %q", got)
	}
	if got := Outcome(9).Err().Error(); got != "unknown outcome" {
		t.Errorf("Outcome(9).Err() = %q", got)
	}
}

func TestCounter(t *testing.T) {
	c := NewCounter(2)
	var ok bool
	for want := int64(1); want <= 2; want++ {
		if c, ok = c.Inc(); !ok || c.Value() != want {
			t.Fatalf("Inc to %d: value %d ok %t", want, c.Value(), ok)
		}
	}
	if !c.Full() {
		t.Error("a counter at its limit is full")
	}
	if next, ok := c.Inc(); ok || next != c {
		t.Errorf("Inc at the limit: ok %t, counter changed %t", ok, next != c)
	}
	for want := int64(1); want >= 0; want-- {
		if c, ok = c.Dec(); !ok || c.Value() != want {
			t.Fatalf("Dec to %d: value %d ok %t", want, c.Value(), ok)
		}
	}
	if next, ok := c.Dec(); ok || next != c {
		t.Errorf("Dec at zero: ok %t, counter changed %t", ok, next != c)
	}
}

func TestCounterEdges(t *testing.T) {
	if c := NewCounter(-5); c != (Counter{}) || c.Limit() != 0 {
		t.Errorf("a negative limit counts nothing, got %+v", c)
	}
	if _, ok := (Counter{}).Inc(); ok {
		t.Error("the zero counter cannot count")
	}
	c := NewCounter(math.MaxInt64)
	c.n = math.MaxInt64 - 1
	c, ok := c.Inc()
	if !ok || c.Value() != math.MaxInt64 {
		t.Fatalf("Inc to MaxInt64: value %d ok %t", c.Value(), ok)
	}
	if _, ok := c.Inc(); ok {
		t.Error("Inc past MaxInt64 must refuse, not wrap")
	}
}

func TestAllocatesNothing(t *testing.T) {
	c := NewCounter(1 << 40)
	allocs := testing.AllocsPerRun(1000, func() {
		c, _ = c.Inc()
		c, _ = c.Dec()
		_ = c.Full()
		_ = Rejected.String()
		_ = Invalid.Err()
		_ = Moved.Err()
		_ = Outcome(-3).Err()
	})
	if allocs != 0 {
		t.Errorf("fsm allocated %v times per run", allocs)
	}
}
