package m

import "encoding/binary"

type Machine struct {
	state State
	count int64
}

func New() Machine { return Machine{state: Idle} }

func (m Machine) State() State { return m.state }

func (m Machine) AppendKey(b []byte) []byte {
	return binary.AppendVarint(append(b, byte(m.state)), int64(m.count))
}
