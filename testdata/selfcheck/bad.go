// Package bad breaks the self-check's rules.
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

type visitor interface{ Visit(n int) int }

type walker struct{ v visitor }

// Visit reaches itself through the interface: Rule 1.
func (w walker) Visit(n int) int { return w.v.Visit(n) }

func spin() { // Rule 2
	for {
		break
	}
}

func grow(n int) { // Rule 2: the bound moves
	for i := 0; i < n; i++ {
		n++
	}
}

func drain(ch chan int) int { // Rule 2
	t := 0
	for v := range ch {
		t += v
	}
	return t
}

func literal() { // Rule 9
	x := func() {}
	x()
}

type ptr *int // Rule 9

type node struct {
	v    int
	next *node
}

func chain(a *node) int { // Rule 9
	if a == nil || a.next == nil {
		return 0
	}
	return a.next.v
}

func generic[T any](x T) T { return x } // Rule 9

func fail() error { return errors.New("x") }

func count() int { return 1 }

func crash() { panic("x") } // Rule 1

func drop() { // Rule 7
	fail()
	count()
}

func unchecked(p *int) error { // Rule 7, and an impure assertion
	if fail() != nil {
		return errors.New("y")
	}
	return errors.New(string(rune(*p)))
}

func index(s []int, i int) int { return s[i] } // Rule 7

func sum(xs []int) int { // Overflow
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

func wide(ok bool) int { // Rule 6
	n := 2
	if ok {
		return n
	}
	return 0
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
