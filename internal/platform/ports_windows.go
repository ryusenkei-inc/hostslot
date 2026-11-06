//go:build windows

package platform

import (
	"encoding/binary"
	"sort"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	tcpTableOwnerPIDListener = 3
	afINET                   = 2
	afINET6                  = 23
	errInsufficientBuffer    = 122
)

// ListeningPorts returns the TCP ports that root and its descendants listen
// on, from the system's TCP table with owning processes.
func ListeningPorts(root int) []int {
	tree := processTree(uint32(root))
	seen := map[int]bool{}
	for _, af := range []uint32{afINET, afINET6} {
		for _, l := range tcpListeners(af) {
			if tree[l.pid] {
				seen[l.port] = true
			}
		}
	}
	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// processTree is root and every process descended from it.
func processTree(root uint32) map[uint32]bool {
	tree := map[uint32]bool{root: true}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return tree
	}
	defer windows.CloseHandle(snap)
	children := map[uint32][]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
	}
	queue := []uint32{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			if !tree[c] {
				tree[c] = true
				queue = append(queue, c)
			}
		}
	}
	return tree
}

type tcpListener struct {
	port int
	pid  uint32
}

// tcpListeners reads MIB_TCPTABLE_OWNER_PID (IPv4) or
// MIB_TCP6TABLE_OWNER_PID (IPv6) for listening sockets.
func tcpListeners(af uint32) []tcpListener {
	var size uint32
	r, _, _ := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDListener, 0)
	if r != errInsufficientBuffer || size == 0 {
		return nil
	}
	buf := make([]byte, size)
	r, _, _ = procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDListener, 0)
	if r != 0 || len(buf) < 4 {
		return nil
	}
	// Row layouts: IPv4 is state, local address, local port, remote address,
	// remote port, pid (6 DWORDs); IPv6 is local address (16), scope, local
	// port, remote address (16), scope, remote port, state, pid.
	rowSize, portAt, pidAt := 24, 8, 20
	if af == afINET6 {
		rowSize, portAt, pidAt = 56, 20, 52
	}
	n := int(binary.LittleEndian.Uint32(buf[:4]))
	var out []tcpListener
	for i := range n {
		row := buf[4+i*rowSize:]
		if len(row) < rowSize {
			break
		}
		// The port is in network byte order in the DWORD's first two bytes.
		port := int(binary.BigEndian.Uint16(row[portAt : portAt+2]))
		out = append(out, tcpListener{port: port, pid: binary.LittleEndian.Uint32(row[pidAt : pidAt+4])})
	}
	return out
}
