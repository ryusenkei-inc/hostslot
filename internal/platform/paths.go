package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths are the directories hostslot writes to.
type Paths struct {
	// Data holds config.json, samples.json and daemon.log.
	Data string
	// Runtime holds the Unix socket and lock files.
	Runtime string
}

// DefaultPaths resolves the per-user directories of the running OS.
// HOSTSLOT_HOME overrides both, which also isolates the IPC endpoint.
func DefaultPaths() (Paths, error) {
	if home := os.Getenv("HOSTSLOT_HOME"); home != "" {
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
		dir := filepath.Join(base, "hostslot")
		return Paths{Data: dir, Runtime: dir}, nil
	case "darwin":
		dir := filepath.Join(home, "Library", "Application Support", "hostslot")
		return Paths{Data: dir, Runtime: dir}, nil
	default:
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		p := Paths{Data: filepath.Join(data, "hostslot")}
		if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
			p.Runtime = filepath.Join(rt, "hostslot")
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

// legacyFiles are carried over from the data directory of a former name,
// so learned costs and settings survive the rename.
var legacyFiles = []string{"config.json", "samples.json"}

// legacyDirs are the data directories of the former names, newest first.
var legacyDirs = []string{"loadslot", "jobgate"}

// migrateLegacy copies each of legacyFiles from the newest sibling directory
// of a former name that has it into a newly created default data directory.
// Errors are ignored: the files only save relearning.
func migrateLegacy(data string) {
	if filepath.Base(data) != "hostslot" {
		return
	}
	for _, name := range legacyFiles {
		for _, dir := range legacyDirs {
			b, err := os.ReadFile(filepath.Join(filepath.Dir(data), dir, name))
			if err != nil {
				continue
			}
			_ = os.WriteFile(filepath.Join(data, name), b, 0o600)
			break
		}
	}
}
