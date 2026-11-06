package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"slots": 3, "costMB": 1500}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOADSLOT_SLOTS", "")
	t.Setenv("LOADSLOT_COST_MB", "")
	t.Setenv("LOADSLOT_RESERVE_MB", "")
	c, err := Load(dir)
	if err != nil || c.Slots != 3 || c.CostMB != 1500 || c.ReserveMB != 1024 {
		t.Fatalf("file: %+v %v", c, err)
	}
	t.Setenv("LOADSLOT_SLOTS", "5")
	c, err = Load(dir)
	if err != nil || c.Slots != 5 {
		t.Fatalf("env: %+v %v", c, err)
	}
	t.Setenv("LOADSLOT_COST_MB", "lots")
	if _, err := Load(dir); err == nil {
		t.Fatal("invalid env accepted")
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("LOADSLOT_SLOTS", "")
	t.Setenv("LOADSLOT_COST_MB", "")
	t.Setenv("LOADSLOT_RESERVE_MB", "")
	c, err := Load(t.TempDir())
	if err != nil || c.CostMB != 2048 {
		t.Fatalf("%+v %v", c, err)
	}
}
