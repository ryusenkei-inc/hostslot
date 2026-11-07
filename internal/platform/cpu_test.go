package platform

import (
	"runtime"
	"testing"
	"time"
)

func TestCPUBusyReadsAFraction(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("not available here")
	}
	m := nativeMetrics{cpu: &cpuMeter{}}
	m.CPUBusy()
	for end := time.Now().Add(200 * time.Millisecond); time.Now().Before(end); {
	}
	busy, ok := m.CPUBusy()
	if !ok || busy < 0 || busy > 1 {
		t.Fatalf("busy %v ok %v", busy, ok)
	}
}
