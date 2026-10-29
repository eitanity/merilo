// Package fsm is what every merilo machine imports: the outcome Fire
// returns, the error a holder reports for a refused event, and the bounded
// counter a machine keeps instead of a raw integer. A machine declares only
// its own states, events, actions and transitions.
//
// fsm imports nothing, allocates nothing and never panics.
package fsm

// Outcome reports what Fire did with an event.
type Outcome int64

// Outcome values. The zero value is an unexported sentinel, so an outcome
// that was never set matches no case.
const (
	outcomeNone Outcome = iota
	Moved               // the event took effect: the machine changed, or its actions say what to do
	Ignored             // nothing to do; the machine is unchanged
	Rejected            // the event is not allowed now; the machine is unchanged
	Invalid             // an impossible state or event, refused with the machine unchanged
)

// String names the outcome, or returns "invalid" for a value outside the
// declared constants.
func (o Outcome) String() string {
	switch o {
	case Moved:
		return "Moved"
	case Ignored:
		return "Ignored"
	case Rejected:
		return "Rejected"
	case Invalid:
		return "Invalid"
	default:
		return "invalid"
	}
}

// Err is the holder's verdict on an outcome: nil when the event was
// handled, Moved or Ignored, and a Refusal for anything else.
func (o Outcome) Err() error {
	switch o {
	case Moved, Ignored:
		return nil
	case Rejected, Invalid:
		return Refusal(o)
	default:
		return Refusal(outcomeNone)
	}
}

// Refusal is the error Err returns for an outcome that refused its event.
// A holder wraps it with the event and state, and tests for it with
// errors.As.
type Refusal Outcome

// Error says why the event was refused.
func (r Refusal) Error() string {
	switch Outcome(r) {
	case Rejected:
		return "rejected"
	case Invalid:
		return "invalid; the machine is unchanged"
	case Moved, Ignored:
		return "not a refusal"
	default:
		return "unknown outcome"
	}
}

// Counter is a count from zero to a limit fixed when it is built. It never
// passes either end: Inc refuses at the limit and Dec at zero. It is a
// comparable value, so a machine holding one stays comparable.
type Counter struct {
	n     int64
	limit int64
}

// NewCounter returns a counter at zero that counts up to limit. A negative
// limit counts nothing.
func NewCounter(limit int64) Counter {
	if limit < 0 {
		return Counter{}
	}
	return Counter{limit: limit}
}

// Inc returns the counter one higher, or the counter unchanged and false
// at its limit.
func (c Counter) Inc() (next Counter, ok bool) {
	if c.n >= c.limit {
		return c, false
	}
	c.n++
	return c, true
}

// Dec returns the counter one lower, or the counter unchanged and false at
// zero.
func (c Counter) Dec() (next Counter, ok bool) {
	if c.n <= 0 {
		return c, false
	}
	c.n--
	return c, true
}

// Value returns the count.
func (c Counter) Value() int64 { return c.n }

// Limit returns the most the counter can reach.
func (c Counter) Limit() int64 { return c.limit }

// Full reports whether the counter is at its limit.
func (c Counter) Full() bool { return c.n >= c.limit }
