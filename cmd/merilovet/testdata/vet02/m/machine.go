package m

type Machine struct {
	state State
	count uint8
	log   []byte
}

func New() Machine { return Machine{state: Idle} }

func (m Machine) State() State { return m.state }

func (m Machine) AppendKey(b []byte) []byte {
	return append(append(b, byte(m.state), m.count), m.log...)
}
