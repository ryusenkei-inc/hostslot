//go:build unix && !linux && !darwin

package platform

import (
	"errors"
	"runtime"
	"syscall"
)

// Other Unix systems build, but report no memory and track only the root.

type nativeMetrics struct{}

func (nativeMetrics) Memory() (Memory, error) {
	return Memory{}, errors.New("memory metrics are not supported on " + runtime.GOOS)
}

func (nativeMetrics) CPUs() int { return runtime.NumCPU() }

const (
	treeSupported = false
	treeMethod    = "ru_maxrss of the root only"
)

func treeRSS(int) (uint64, []int) { return 0, nil }

func maxRSSBytes(ru *syscall.Rusage) uint64 { return uint64(ru.Maxrss) * 1024 }
