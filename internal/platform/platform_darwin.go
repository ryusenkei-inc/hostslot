//go:build darwin

package platform

import (
	"bufio"
	"bytes"
	"errors"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ---- Metrics: sysctl ----

type nativeMetrics struct{}

func (nativeMetrics) Memory() (Memory, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Memory{}, err
	}
	free, err := availableBytes()
	if err != nil {
		return Memory{}, err
	}
	return Memory{TotalMB: total / mib, FreeMB: min(free, total) / mib}, nil
}

func (nativeMetrics) CPUs() int { return runtime.NumCPU() }

// availableBytes approximates reclaimable memory as Activity Monitor does:
// free, speculative and purgeable pages plus file-backed pages. Without cgo
// host_statistics is unreachable, so the page counters come from sysctl,
// with vm_stat as a fallback.
func availableBytes() (uint64, error) {
	page, err := unix.SysctlUint32("vm.pagesize")
	if err != nil {
		page = uint32(unix.Getpagesize())
	}
	var pages uint64
	found := false
	for _, name := range []string{"vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count", "vm.page_pageable_external_count"} {
		if v, err := unix.SysctlUint32(name); err == nil {
			pages += uint64(v)
			found = true
		}
	}
	if found {
		return pages * uint64(page), nil
	}
	return vmStatAvailable()
}

var vmStatPage = regexp.MustCompile(`page size of (\d+) bytes`)

func vmStatAvailable() (uint64, error) {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, err
	}
	page := uint64(4096)
	if m := vmStatPage.FindSubmatch(out); m != nil {
		page, _ = strconv.ParseUint(string(m[1]), 10, 64)
	}
	var pages uint64
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch key {
		case "Pages free", "Pages speculative", "Pages purgeable", "File-backed pages":
			v, _ := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
			pages += v
		}
	}
	if pages == 0 {
		return 0, errors.New("vm_stat: no page counters")
	}
	return pages * page, nil
}

// ---- Process tree: ps ----

// proc_pid_rusage needs cgo, so the tree is read from ps, which every macOS
// ships. Polling misses peaks shorter than the interval; ru_maxrss covers the
// root after exit.
const (
	treeSupported = true
	treeMethod    = "ps (sum of resident memory of the tree)"
)

func treeRSS(root int) (uint64, []int) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=").Output()
	if err != nil {
		return 0, nil
	}
	children := map[int][]int{}
	rss := map[int]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		kb, _ := strconv.ParseUint(f[2], 10, 64)
		children[ppid] = append(children[ppid], pid)
		rss[pid] = kb * 1024
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

// macOS reports ru_maxrss in bytes.
func maxRSSBytes(ru *syscall.Rusage) uint64 { return uint64(ru.Maxrss) }
