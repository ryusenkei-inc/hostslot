package platform

import "testing"

func TestParseGPU(t *testing.T) {
	g, ok := parseGPU("NVIDIA GeForce GTX 1660 Ti, 6144, 5040\nNVIDIA A100, 81920, 80000\n")
	if !ok || g != (GPU{Name: "NVIDIA GeForce GTX 1660 Ti", TotalMB: 6144, FreeMB: 5040}) {
		t.Fatalf("got %+v %v", g, ok)
	}
	for _, bad := range []string{"", "No devices were found", "x, [N/A], [N/A]"} {
		if _, ok := parseGPU(bad); ok {
			t.Fatalf("parsed %q", bad)
		}
	}
}
