package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const existing = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [{ "type": "command", "command": "C:/ide/agent-hook.exe claude", "timeout": 10 }]
      }
    ],
    "Stop": []
  },
  "theme": "light"
}
`

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClaudeAppendsAndKeepsEverythingElse(t *testing.T) {
	path := write(t, existing)
	cmd := HookCommand("C:/bin/hostslot.exe", Claude)
	res, err := Register(path, Claude, cmd, false, false)
	if err != nil || res != Added {
		t.Fatalf("res %v err %v", res, err)
	}
	got := read(t, path)
	order := []string{`"model"`, `"hooks"`, `"PreToolUse"`, "agent-hook.exe", "hostslot.exe hook claude", `"Stop"`, `"theme"`}
	last := -1
	for _, s := range order {
		i := strings.Index(got, s)
		if i <= last {
			t.Fatalf("%s missing or out of order in:\n%s", s, got)
		}
		last = i
	}
	if !strings.Contains(got, `"matcher": "Bash"`) {
		t.Fatalf("no Bash matcher:\n%s", got)
	}

	if res, err := Register(path, Claude, cmd, false, false); err != nil || res != Unchanged {
		t.Fatalf("second run: res %v err %v", res, err)
	}
	if res, err := Register(path, Claude, HookCommand("D:/new/hostslot.exe", Claude), false, false); err != nil || res != Updated {
		t.Fatalf("moved exe: res %v err %v", res, err)
	}
	if got := read(t, path); strings.Contains(got, "C:/bin/hostslot.exe") || strings.Count(got, "hook claude") != 1 {
		t.Fatalf("not updated in place:\n%s", got)
	}
}

func TestClaudeRemove(t *testing.T) {
	path := write(t, existing)
	if _, err := Register(path, Claude, HookCommand("C:/bin/hostslot.exe", Claude), false, false); err != nil {
		t.Fatal(err)
	}
	res, err := Register(path, Claude, "", true, false)
	if err != nil || res != Removed {
		t.Fatalf("res %v err %v", res, err)
	}
	got := read(t, path)
	if strings.Contains(got, "hostslot") || !strings.Contains(got, "agent-hook.exe") {
		t.Fatalf("remove touched the wrong entries:\n%s", got)
	}
	if res, _ := Register(path, Claude, "", true, false); res != Unchanged {
		t.Fatalf("second remove: %v", res)
	}
}

func TestClaudeCreatesMissingFileAndDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	if res, err := Register(path, Claude, HookCommand("/usr/local/bin/hostslot", Claude), false, true); err != nil || res != Added {
		t.Fatalf("dry run: res %v err %v", res, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the file")
	}
	if _, err := Register(path, Claude, HookCommand("/usr/local/bin/hostslot", Claude), false, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, `"command": "/usr/local/bin/hostslot hook claude"`) {
		t.Fatalf("unexpected file:\n%s", got)
	}
}

func TestHookCommandQuotesSpaces(t *testing.T) {
	cmd := HookCommand(filepath.Join("/opt", "my tools", "hostslot"), Claude)
	if cmd != `"/opt/my tools/hostslot" hook claude` {
		t.Fatalf("got %s", cmd)
	}
	if _, ours := hostslotCommand([]byte(`{"hooks":[{"command":`+quote(cmd)+`}]}`), Claude); !ours {
		t.Fatal("quoted command not recognized as hostslot's")
	}
}

func TestClaudeRejectsInvalidSettings(t *testing.T) {
	path := write(t, `{"hooks": [1, 2]}`)
	if _, err := Register(path, Claude, "x hook claude", false, false); err == nil {
		t.Fatal("accepted hooks that are not an object")
	}
	if got := read(t, path); got != `{"hooks": [1, 2]}` {
		t.Fatalf("invalid file was rewritten: %s", got)
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func TestCodexKeepsOtherGroupsFirst(t *testing.T) {
	path := write(t, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"C:/ide/codex-hook.cmd","timeout":10}]}]}}`)
	res, err := Register(path, Codex, HookCommand("C:/bin/hostslot.exe", Codex), false, false)
	if err != nil || res != Added {
		t.Fatalf("res %v err %v", res, err)
	}
	got := read(t, path)
	if i, j := strings.Index(got, "codex-hook.cmd"), strings.Index(got, "hostslot.exe hook codex"); i < 0 || j < i {
		t.Fatalf("existing group must stay first:\n%s", got)
	}
	// A Claude entry is not the Codex entry.
	if res, _ := Register(path, Claude, "", true, false); res != Unchanged {
		t.Fatalf("removing Claude's hook touched Codex's: %v", res)
	}
}

func TestRegisterReplacesTheFormerNames(t *testing.T) {
	for _, former := range []string{"loadslot", "jobgate"} {
		path := write(t, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"C:/bin/`+former+`.exe hook claude"}]}]}}`)
		res, err := Register(path, Claude, HookCommand("C:/bin/hostslot.exe", Claude), false, false)
		if err != nil || res != Updated {
			t.Fatalf("%s: res %v err %v", former, res, err)
		}
		if got := read(t, path); strings.Contains(got, former) || strings.Count(got, "hook claude") != 1 {
			t.Fatalf("%s hook not replaced in place:\n%s", former, got)
		}
	}
}
