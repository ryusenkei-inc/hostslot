package platform

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GPU is the memory of the first NVIDIA GPU.
type GPU struct {
	Name    string `json:"name"`
	TotalMB uint64 `json:"totalMB"`
	FreeMB  uint64 `json:"freeMB"`
}

// ReadGPU asks nvidia-smi for the first GPU's memory; ok is false without an
// NVIDIA GPU or driver. Other vendors are not read yet.
func ReadGPU() (GPU, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name,memory.total,memory.free", "--format=csv,noheader,nounits")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return GPU{}, false
	}
	return parseGPU(string(out))
}

// parseGPU reads the first line of `name, total, free` in MiB.
func parseGPU(out string) (GPU, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	f := strings.Split(line, ",")
	if len(f) != 3 {
		return GPU{}, false
	}
	total, err1 := strconv.ParseUint(strings.TrimSpace(f[1]), 10, 64)
	free, err2 := strconv.ParseUint(strings.TrimSpace(f[2]), 10, 64)
	if err1 != nil || err2 != nil || total == 0 {
		return GPU{}, false
	}
	return GPU{Name: strings.TrimSpace(f[0]), TotalMB: total, FreeMB: min(free, total)}, true
}
