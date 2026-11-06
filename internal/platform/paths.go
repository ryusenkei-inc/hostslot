package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths are the directories loadslot writes to.
type Paths struct {
	// Data holds config.json, samples.json and daemon.log.
	Data string
	// Runtime holds the Unix socket and lock files.
	Runtime string
}

// DefaultPaths resolves the per-user directories of the running OS.
// LOADSLOT_HOME overrides both, which also isolates the IPC endpoint.
func DefaultPaths() (Paths, error) {
	if home := os.Getenv("LOADSLOT_HOME"); home != "" {
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
		dir := filepath.Join(base, "loadslot")
		return Paths{Data: dir, Runtime: dir}, nil
	case "darwin":
		dir := filepath.Join(home, "Library", "Application Support", "loadslot")
		return Paths{Data: dir, Runtime: dir}, nil
	default:
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		p := Paths{Data: filepath.Join(data, "loadslot")}
		if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
			p.Runtime = filepath.Join(rt, "loadslot")
		} else {
			p.Runtime = p.Data
		}
		return p, nil
	}
}

// Ensure creates the directories with user-only permissions.
func (p Paths) Ensure() error {
	_, statErr := os.Stat(p.Data)
	for _, d := range []string{p.Data, p.Runtime} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if os.IsNotExist(statErr) {
		migrateLegacy(p.Data)
	}
	return nil
}

// legacyFiles are carried over from the data directory of jobgate, the
// former name, so learned costs and settings survive the rename.
var legacyFiles = []string{"config.json", "samples.json"}

// migrateLegacy copies legacyFiles from a sibling "jobgate" directory into a
// newly created default data directory. Errors are ignored: the files only
// save relearning.
func migrateLegacy(data string) {
	if filepath.Base(data) != "loadslot" {
		return
	}
	legacy := filepath.Join(filepath.Dir(data), "jobgate")
	for _, name := range legacyFiles {
		b, err := os.ReadFile(filepath.Join(legacy, name))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(data, name), b, 0o600)
	}
}
