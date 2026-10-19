// Package bad breaks one Power of Ten rule per declaration, for the self-check.
package bad

import "errors"

var global = 1 // Rule 6

//go:generate echo generated

func recurse(n int) int { // Rule 1
	if n == 0 {
		return 0
	}
	return recurse(n - 1)
}

func spin() { // Rule 2
	for {
		break
	}
}

func literal() { // Rule 9
	x := func() {}
	x()
}

func fail() error { return errors.New("x") }

func drop() { // Rule 7
	fail()
}

func long() int { // Rule 4
	n := global
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	n++
	return n
}
