package merilo

import (
	"errors"
	"testing"

	"code.eitanity.com/merilo/fsm"
)

// Totality exposes one machine's state–event table to Total by position.
// Positions are indexes the test chooses, never domain values.
type Totality interface {
	Machines() int // machine values to check; position 0 is the zero machine
	Events() int   // events to fire; position 0 is the zero event
	// Fire builds machine m, fires event e, and returns the outcome and
	// whether the machine and its actions were unchanged.
	Fire(m, e int) (out fsm.Outcome, unchanged bool)
	Name(m, e int) string // a readable name for the pair
}

// Total fires every event at every machine. Position 0 of either must be
// refused as Invalid, with the machine unchanged; any other pair must not
// be Invalid, and a quiet pair must leave the machine unchanged with no
// actions.
func Total(t testing.TB, tab Totality) {
	if t == nil {
		return
	}
	t.Helper()
	if tab == nil {
		t.Fatalf("merilo: nil Totality")
	}
	if again := reentered(); again {
		t.Fatalf("%v", errReentered)
	}
	nm, ne := tab.Machines(), tab.Events()
	if nm < 2 || ne < 2 {
		t.Fatalf("merilo: Totality needs the zero machine and event plus at least one real one; got %d machines, %d events", nm, ne)
	}
	for m := range nm {
		for e := range ne {
			if err := judge(tab, m, e); err != nil {
				t.Errorf("merilo: %s: %v", oneLine(tab.Name(m, e)), err)
			}
		}
	}
}

// judge fires one pair and returns why it fails, or nil.
func judge(tab Totality, m, e int) error {
	if tab == nil {
		return errors.New("merilo: nil Totality")
	}
	out, unchanged := tab.Fire(m, e)
	quiet := out == fsm.Ignored || out == fsm.Rejected
	invalid := !quiet && out != fsm.Moved
	zero := m == 0 || e == 0
	switch {
	case zero && (!invalid || !unchanged):
		return errors.New("the zero machine or zero event was not refused as Invalid with the machine unchanged")
	case !zero && invalid:
		return errors.New("refused as Invalid; the machine does not handle this pair")
	case !zero && quiet && !unchanged:
		return errors.New("quiet outcome (Ignored or Rejected), but the machine changed or actions were returned")
	}
	return nil
}
