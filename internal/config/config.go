// Package config loads the scheduler settings from config.json in the data
// directory and applies environment overrides on top.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ryusenkei-inc/hostslot/internal/gate"
)

// FileName is the config file inside the data directory.
const FileName = "config.json"

// Load reads dataDir/config.json (optional) and applies HOSTSLOT_SLOTS,
// HOSTSLOT_COST_MB and HOSTSLOT_RESERVE_MB. The result is normalized.
func Load(dataDir string) (gate.Config, error) {
	var c gate.Config
	b, err := os.ReadFile(filepath.Join(dataDir, FileName))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &c); err != nil {
			return gate.Config{}, fmt.Errorf("%s: %w", FileName, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return gate.Config{}, err
	}
	if v, ok, err := envInt("HOSTSLOT_SLOTS"); err != nil {
		return gate.Config{}, err
	} else if ok {
		c.Slots = int(v)
	}
	if v, ok, err := envInt("HOSTSLOT_COST_MB"); err != nil {
		return gate.Config{}, err
	} else if ok {
		c.CostMB = v
	}
	if v, ok, err := envInt("HOSTSLOT_RESERVE_MB"); err != nil {
		return gate.Config{}, err
	} else if ok {
		c.ReserveMB = v
	}
	return c.Normalize(), nil
}

func envInt(name string) (uint64, bool, error) {
	s := os.Getenv(name)
	if s == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, false, fmt.Errorf("%s: not a non-negative integer: %q", name, s)
	}
	return v, true, nil
}
