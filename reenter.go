package merilo

import (
	"runtime"
	"strings"
)

// maxFrames bounds the stack the re-entry guard reads. A re-entry is
// found whenever the adapter's own calls between merilo's call into it and
// its call back are fewer than this.
const maxFrames = 1 << 10

// ownPrefix begins the name of every function in package merilo, and of
// none in another package, fsm included.
const ownPrefix = "code.eitanity.com/merilo."

// reentered reports whether merilo is being entered again from adopter
// code that merilo itself called: reading the stack outward, merilo's
// frames, then adopter frames, then merilo again. An adapter method that
// calls back into merilo is recursion through code the self-check cannot
// see (Rule 1), so the harness refuses it at run time instead.
func reentered() bool {
	var pcs [maxFrames]uintptr
	n := runtime.Callers(2, pcs[:])
	if n < 0 || n > maxFrames {
		return false
	}
	left := false // an adopter frame has been passed
	for i := range n {
		mine := own(pcs[i] - 1)
		if mine && left {
			return true
		}
		left = left || !mine
	}
	return false
}

// own reports whether pc lies in merilo's own non-test code. Code in
// merilo's test files is adapter code, like an adopter's.
func own(pc uintptr) bool {
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return false
	}
	if !strings.HasPrefix(fn.Name(), ownPrefix) {
		return false
	}
	file, _ := fn.FileLine(pc)
	return !strings.HasSuffix(file, "_test.go")
}
