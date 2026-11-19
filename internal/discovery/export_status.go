package discovery

// SetStatusForTest sets s as the responder would: for tests of the
// packages that show it.
func SetStatusForTest(s *Status, state, name string, port int) {
	s.setPort(port)
	s.set(state, name)
}
