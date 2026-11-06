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
	data := filepath.Join(base, "loadslot")
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
