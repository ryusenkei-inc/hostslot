//go:build linux

package platform

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// cpuMeter turns the cumulative CPU times of /proc/stat into a busy
// fraction since the previous reading.
type cpuMeter struct {
	mu          sync.Mutex
	idle, total uint64
}

func (m *cpuMeter) busy() (float64, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, false
	}
	var total, idle uint64
	for i, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		total += v
		// Fields: user nice system idle iowait irq softirq steal ...
		if i == 3 || i == 4 {
			idle += v
		}
	}
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
