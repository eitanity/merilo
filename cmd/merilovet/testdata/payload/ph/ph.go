package ph

import (
	"fixture/p"

	"code.eitanity.com/merilo/fsm"
)

type Watchdog struct {
	w     p.Machine
	fired int
}

func NewWatchdog() *Watchdog { return &Watchdog{w: p.New()} }

// Expire delivers a timer expiry tagged with the generation read off the wire.
func (d *Watchdog) Expire(gen uint64) {
	var out fsm.Outcome
	d.w, out = d.w.Fire(p.Event{Kind: p.EvTimeout, Gen: gen})
	if out == fsm.Moved {
		d.fired++
	}
}
