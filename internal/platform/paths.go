package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths are the directories jobgate writes to.
type Paths struct {
	// Data holds config.json, samples.json and daemon.log.
	Data string
	// Runtime holds the Unix socket and lock files.
	Runtime string
}

// DefaultPaths resolves the per-user directories of the running OS.
// JOBGATE_HOME overrides both, which also isolates the IPC endpoint.
func DefaultPaths() (Paths, error) {
	if home := os.Getenv("JOBGATE_HOME"); home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return Paths{}, err
		}
		return Paths{Data: abs, Runtime: abs}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		dir := filepath.Join(base, "jobgate")
		return Paths{Data: dir, Runtime: dir}, nil
	case "darwin":
		dir := filepath.Join(home, "Library", "Application Support", "jobgate")
		return Paths{Data: dir, Runtime: dir}, nil
	default:
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		p := Paths{Data: filepath.Join(data, "jobgate")}
		if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
			p.Runtime = filepath.Join(rt, "jobgate")
		} else {
			p.Runtime = p.Data
		}
		return p, nil
	}
}

// Ensure creates the directories with user-only permissions.
func (p Paths) Ensure() error {
	for _, d := range []string{p.Data, p.Runtime} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
