// Package ok follows every rule the self-check enforces, so
// a finding here is a false positive.
package ok

import "errors"

const limit = 8

type shape interface{ area() (int, error) }

type square struct{ side int }

func (s square) area() (int, error) {
	if s.side < 0 {
		return 0, errors.New("negative side")
	}
	if s.side > limit {
		return 0, errors.New("side over limit")
	}
	return s.side * s.side, nil
}

func total(sh shape, n int) (int, error) {
	if sh == nil {
		return 0, errors.New("total needs a shape")
	}
	if n < 0 || n > limit {
		return 0, errors.New("n out of range")
	}
	sum := 0
	for i := 0; i < n; i++ {
		a, err := sh.area()
		if err != nil {
			return 0, err
		}
		if sum > limit*limit*limit {
			return 0, errors.New("sum over its bound")
		}
		sum += a
	}
	return sum, nil
}

func pick(xs []int, i int) (int, error) {
	if i < 0 || i >= len(xs) {
		return 0, errors.New("index out of range")
	}
	if len(xs) > limit {
		return 0, errors.New("too many")
	}
	return xs[i], nil
}
