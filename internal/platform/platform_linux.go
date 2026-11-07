//go:build linux

package platform

import (
	"bufio"
	"bytes"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// ---- Metrics: /proc/meminfo, limited by cgroup v2 ----

type nativeMetrics struct{ cpu *cpuMeter }

// CPUBusy implements CPUMeter.
func (m nativeMetrics) CPUBusy() (float64, bool) { return m.cpu.busy() }

func (nativeMetrics) Memory() (Memory, error) {
	info, err := readMeminfo("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	m := Memory{TotalMB: info["MemTotal"] / 1024, FreeMB: info["MemAvailable"] / 1024}
	if dir := cgroupDir(); dir != "" {
		limit, okLimit := readCgroupUint(filepath.Join(dir, "memory.max"))
		current, okCurrent := readCgroupUint(filepath.Join(dir, "memory.current"))
		if okLimit && limit/mib < m.TotalMB {
			m.TotalMB = limit / mib
			if okCurrent {
				m.FreeMB = min(m.FreeMB, (limit-min(current, limit))/mib)
			}
		}
	}
	return m, nil
}

func (nativeMetrics) CPUs() int {
	n := runtime.NumCPU()
	if dir := cgroupDir(); dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, "cpu.max")); err == nil {
			f := strings.Fields(string(b))
			if len(f) == 2 && f[0] != "max" {
				quota, err1 := strconv.ParseFloat(f[0], 64)
				period, err2 := strconv.ParseFloat(f[1], 64)
				if err1 == nil && err2 == nil && period > 0 {
					n = min(n, max(int(math.Ceil(quota/period)), 1))
				}
			}
		}
	}
	return n
}

func readMeminfo(path string) (map[string]uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
			out[key] = v
		}
	}
	return out, sc.Err()
}

// cgroupDir returns the cgroup v2 directory of this process, or "".
func cgroupDir() string {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			dir := filepath.Join("/sys/fs/cgroup", rest)
			if _, err := os.Stat(filepath.Join(dir, "memory.max")); err == nil {
				return dir
			}
		}
	}
	return ""
}

func readCgroupUint(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return v, err == nil
}

// ---- Process tree: /proc ----

const (
	treeSupported = true
	treeMethod    = "/proc (sum of resident memory of the tree)"
)

var pageSize = uint64(os.Getpagesize())

// treeRSS sums the resident memory of root and its descendants and returns
// their pids (root excluded).
func treeRSS(root int) (uint64, []int) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, nil
	}
	children := map[int][]int{}
	rss := map[int]uint64{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// The command name may contain spaces and parentheses, so fields
		// are counted after the last ')'.
		i := bytes.LastIndexByte(b, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(b[i+1:]))
		// f[0] is field 3 (state); ppid is field 4, rss is field 24.
		if len(f) < 22 {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		pages, _ := strconv.ParseUint(f[21], 10, 64)
		children[ppid] = append(children[ppid], pid)
		rss[pid] = pages * pageSize
	}
	total := rss[root]
	var pids []int
	queue := append([]int(nil), children[root]...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		total += rss[pid]
		pids = append(pids, pid)
		queue = append(queue, children[pid]...)
	}
	return total, pids
}

// Linux reports ru_maxrss in KiB.
func maxRSSBytes(ru *syscall.Rusage) uint64 { return uint64(ru.Maxrss) * 1024 }
