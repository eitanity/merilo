// Command fib prints the nth Fibonacci number. It is the holder for
// package fib: its loop is the trampoline that evaluates the call until it
// returns, without a stack.
package main

import (
	"fmt"
	"math/big"
	"os"
	"strconv"

	"code.eitanity.com/merilo/examples/fib"
	"code.eitanity.com/merilo/fsm"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fib n")
		os.Exit(2)
	}
	n, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fib:", err)
		os.Exit(2)
	}
	v, err := compute(n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fib:", err)
		os.Exit(1)
	}
	fmt.Println(v)
}

// compute evaluates fib(n): each Fire is one call, and a tail call leaves
// the machine as the next call, so the loop fires until it has returned.
func compute(n int64) (*big.Int, error) {
	m := fib.New(n)
	var out fsm.Outcome
	for m.State() == fib.Calling {
		m, out = m.Fire(fib.EvCall)
		if err := out.Err(); err != nil {
			return nil, fmt.Errorf("call in %v: %w", m.State(), err)
		}
	}
	v, ok := m.Result()
	if !ok {
		return nil, fmt.Errorf("fib(%d): %v", n, m.State())
	}
	return v, nil
}
