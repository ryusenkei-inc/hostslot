//go:build unix && !linux && !darwin

package platform

// ListeningPorts is not available on this system: it returns nothing, and
// callers fall back to checking the assigned port.
func ListeningPorts(int) []int { return nil }
