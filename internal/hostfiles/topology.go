package hostfiles

func (m *Manager) Roots() ([]Entry, error) {
	return m.topology.Roots()
}
