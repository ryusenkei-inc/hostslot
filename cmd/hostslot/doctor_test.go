package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryusenkei-inc/hostslot/internal/setup"
)

func TestHookCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")

	if c, ok := hookCheck(setup.Claude, exe); !ok || c.Level != "warn" || !strings.Contains(c.Text, "not registered") {
		t.Fatalf("unregistered: %+v %v", c, ok)
	}
	if _, err := setup.Register(settings, setup.Claude, setup.HookCommand(filepath.Join(dir, "gone", "hostslot"), setup.Claude), false, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := hookCheck(setup.Claude, exe); c.Level != "error" || !strings.Contains(c.Text, "does not exist") {
		t.Fatalf("missing executable: %+v", c)
	}
	if _, err := setup.Register(settings, setup.Claude, setup.HookCommand(exe, setup.Claude), false, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := hookCheck(setup.Claude, exe); c.Level != "ok" {
		t.Fatalf("registered: %+v", c)
	}
}

func TestPrintChecksExitCode(t *testing.T) {
	var b bytes.Buffer
	if code := printChecks(&b, []check{{Level: "ok", Text: "a"}, {Level: "info", Text: "b"}}); code != 0 {
		t.Fatalf("ok and info gave %d", code)
	}
	if code := printChecks(&b, []check{{Level: "ok", Text: "a"}, {Level: "warn", Text: "b"}}); code != 1 {
		t.Fatalf("warn gave %d", code)
	}
	if !strings.Contains(b.String(), "warn   b") {
		t.Fatalf("output: %q", b.String())
	}
}

func TestChecksMarshalForIntegrations(t *testing.T) {
	b, err := json.Marshal([]check{{Level: "warn", Text: "x"}, {Level: "warn", Text: "y", Fix: "setup"}})
	if err != nil || string(b) != `[{"level":"warn","text":"x"},{"level":"warn","text":"y","fix":"setup"}]` {
		t.Fatalf("%s %v", b, err)
	}
}

func TestSetupReportMarshalsForIntegrations(t *testing.T) {
	r := setupReport{Agents: []agentResult{{Agent: "codex", File: "h.json", Result: "added"}}, Retired: true}
	b, err := json.Marshal(r)
	if err != nil || string(b) != `{"agents":[{"agent":"codex","file":"h.json","result":"added"}],"retired":true}` {
		t.Fatalf("%s %v", b, err)
	}
}
