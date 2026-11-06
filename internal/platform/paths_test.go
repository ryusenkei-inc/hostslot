package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCarriesOverTheFormerDataDirectory(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "jobgate")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "samples.json"), []byte(`{"k":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(base, "hostslot")
	if err := (Paths{Data: data, Runtime: data}).Ensure(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(data, "samples.json")); err != nil || string(b) != `{"k":[]}` {
		t.Fatalf("samples not carried over: %q %v", b, err)
	}
	// An existing data directory is left alone.
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Paths{Data: data, Runtime: data}).Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "config.json")); !os.IsNotExist(err) {
		t.Fatal("copied into an existing data directory")
	}
}

func TestEnsurePrefersTheNewestFormerName(t *testing.T) {
	base := t.TempDir()
	for dir, samples := range map[string]string{"loadslot": `{"new":[]}`, "jobgate": `{"old":[]}`} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, dir, "samples.json"), []byte(samples), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Only jobgate had a config.
	if err := os.WriteFile(filepath.Join(base, "jobgate", "config.json"), []byte(`{"slots":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(base, "hostslot")
	if err := (Paths{Data: data, Runtime: data}).Ensure(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "samples.json")); string(b) != `{"new":[]}` {
		t.Fatalf("samples from the wrong directory: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "config.json")); string(b) != `{"slots":2}` {
		t.Fatalf("config not carried over: %s", b)
	}
}
