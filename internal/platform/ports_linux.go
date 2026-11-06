//go:build linux

package platform

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ListeningPorts returns the TCP ports that root and its descendants listen
// on: the listening sockets in /proc/net/tcp{,6} whose inodes the tree has
// open.
func ListeningPorts(root int) []int {
	_, pids := treeRSS(root)
	inodes := map[string]bool{}
	for _, pid := range append(pids, root) {
		dir := "/proc/" + strconv.Itoa(pid) + "/fd"
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(dir + "/" + fd.Name())
			if err == nil && strings.HasPrefix(link, "socket:[") {
				inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
			}
		}
	}
	seen := map[int]bool{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		for _, port := range listeningIn(table, inodes) {
			seen[port] = true
		}
	}
	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// listeningIn reads a /proc/net/tcp table: local address "ADDR:PORT" in hex
// (field 2), state 0A for LISTEN (field 4), inode (field 10).
func listeningIn(table string, inodes map[string]bool) []int {
	f, err := os.Open(table)
	if err != nil {
		return nil
	}
	defer f.Close()
	var ports []int
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] {
			continue
		}
		_, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		if p, err := strconv.ParseUint(hexPort, 16, 16); err == nil {
			ports = append(ports, int(p))
		}
	}
	return ports
}
