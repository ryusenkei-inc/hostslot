//go:build windows

package platform

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetSystemTimes = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimes")

// cpuMeter turns the system's cumulative CPU times into a busy fraction
// since the previous reading.
type cpuMeter struct {
	mu          sync.Mutex
	idle, total uint64
}

func (m *cpuMeter) busy() (float64, bool) {
	var idle, kernel, user windows.Filetime
	r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return 0, false
	}
	ft := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	// Kernel time includes idle time.
	return m.next(ft(idle), ft(kernel)+ft(user))
}

func (m *cpuMeter) next(idle, total uint64) (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dIdle, dTotal := idle-m.idle, total-m.total
	first := m.total == 0
	m.idle, m.total = idle, total
	if first || dTotal == 0 || dIdle > dTotal {
		return 0, false
	}
	return 1 - float64(dIdle)/float64(dTotal), true
}
