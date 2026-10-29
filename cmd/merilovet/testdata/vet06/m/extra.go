package m

func bump(x Machine) State {
	x.count = 7
	return x.state
}
