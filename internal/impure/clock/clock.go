// Package clock is a helper with hidden state, standing in for a wall
// clock: each call returns a different value.
package clock

var ticks int

// Now returns the next tick.
func Now() int {
	ticks++
	return ticks
}
