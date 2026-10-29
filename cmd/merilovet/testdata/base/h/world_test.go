package h

type world struct {
	n     int
	ready bool
	slots [2]int
}

type cleanSys struct{ w world }

func (s *cleanSys) World() any { return s.w }
