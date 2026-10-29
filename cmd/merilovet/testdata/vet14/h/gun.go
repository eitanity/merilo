package h

type Gun struct{ shots int }

func (g *Gun) Fire() { g.shots++ }
