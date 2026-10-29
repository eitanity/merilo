package t

type sys struct{ steps []int }

func (s sys) World() any { return s.steps }
