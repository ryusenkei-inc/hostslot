package platform

import (
	"net"
	"os"
	"runtime"
	"slices"
	"testing"
)

func TestListeningPortsSeesThisProcess(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("not available here")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if got := ListeningPorts(os.Getpid()); !slices.Contains(got, port) {
		t.Fatalf("port %d not in %v", port, got)
	}
}
