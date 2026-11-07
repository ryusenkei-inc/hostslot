//go:build darwin

package platform

import (
	"encoding/binary"
	"runtime"

	"golang.org/x/sys/unix"
)

// cpuMeter approximates how busy the CPUs are by the 1-minute load average
// per CPU: macOS gives cumulative CPU times only through Mach calls that
// need cgo.
type cpuMeter struct{}

func (*cpuMeter) busy() (float64, bool) {
	// struct loadavg { fixpt_t ldavg[3]; long fscale; }
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(b) < 24 {
		return 0, false
	}
	load := float64(binary.LittleEndian.Uint32(b[0:4]))
	scale := float64(binary.LittleEndian.Uint64(b[16:24]))
	if scale == 0 {
		return 0, false
	}
	return min(load/scale/float64(runtime.NumCPU()), 1), true
}
