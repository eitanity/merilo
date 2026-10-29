package turnstile

import (
	"fmt"
	"testing"

	"code.eitanity.com/merilo"
	"code.eitanity.com/merilo/fsm"
)

type table struct{}

func (table) Machines() int { return int(numStates) }
func (table) Events() int   { return int(numEvents) }

func (table) Fire(m, e int) (fsm.Outcome, bool) {
	before := Machine{state: State(m)}
	next, out := before.Fire(Event(e))
	return out, next == before
}

func (table) Name(m, e int) string { return fmt.Sprintf("state %d / event %d", m, e) }

func TestTotal(t *testing.T) { merilo.Total(t, table{}) }
