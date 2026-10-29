package h

type world struct {
	n    int
	next *int
}

type sys struct{ w world }

func (s *sys) World() any { return s.w }
