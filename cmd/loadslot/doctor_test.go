package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryusenkei-inc/loadslot/internal/setup"
)

func TestHookCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")

	if c, ok := hookCheck(setup.Claude, exe); !ok || c.level != "warn" || !strings.Contains(c.text, "not registered") {
		t.Fatalf("unregistered: %+v %v", c, ok)
	}
	if _, err := setup.Register(settings, setup.Claude, setup.HookCommand(filepath.Join(dir, "gone", "loadslot"), setup.Claude), false, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := hookCheck(setup.Claude, exe); c.level != "error" || !strings.Contains(c.text, "does not exist") {
		t.Fatalf("missing executable: %+v", c)
	}
	if _, err := setup.Register(settings, setup.Claude, setup.HookCommand(exe, setup.Claude), false, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := hookCheck(setup.Claude, exe); c.level != "ok" {
		t.Fatalf("registered: %+v", c)
	}
}

func TestPrintChecksExitCode(t *testing.T) {
	var b bytes.Buffer
	if code := printChecks(&b, []check{{"ok", "a"}, {"info", "b"}}); code != 0 {
		t.Fatalf("ok and info gave %d", code)
	}
	if code := printChecks(&b, []check{{"ok", "a"}, {"warn", "b"}}); code != 1 {
		t.Fatalf("warn gave %d", code)
	}
	if !strings.Contains(b.String(), "warn   b") {
		t.Fatalf("output: %q", b.String())
	}
}
