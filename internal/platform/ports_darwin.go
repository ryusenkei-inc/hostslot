//go:build darwin

package platform

import (
	"bufio"
	"bytes"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// ListeningPorts returns the TCP ports that root and its descendants listen
// on, from lsof (macOS has no /proc).
func ListeningPorts(root int) []int {
	_, pids := treeRSS(root)
	list := make([]string, 0, len(pids)+1)
	for _, pid := range append(pids, root) {
		list = append(list, strconv.Itoa(pid))
	}
	// lsof exits 1 when nothing matches; the output is still read.
	out, _ := exec.Command("lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-p", strings.Join(list, ","), "-Fn").Output()
	seen := map[int]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "n") {
			continue
		}
		i := strings.LastIndexByte(line, ':')
		if p, err := strconv.Atoi(line[i+1:]); i >= 0 && err == nil {
			seen[p] = true
		}
	}
	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}
