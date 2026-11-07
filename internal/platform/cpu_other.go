//go:build unix && !linux && !darwin

package platform

// cpuMeter is not available on this system.
type cpuMeter struct{}

func (*cpuMeter) busy() (float64, bool) { return 0, false }
