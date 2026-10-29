package m

func (m Machine) bump() Machine { m.count++; return m }

func (m Machine) plus(n int64) Machine { m.count = m.count + n; return m }

func (m Machine) addUnbounded(n int64) Machine {
	if m.count >= maxCount {
		return m
	}
	m.count += n
	return m
}

func (m Machine) addBounded(n int64) Machine {
	if m.count >= maxCount || n > 4 || n < 0 {
		return m
	}
	m.count += n
	return m
}
