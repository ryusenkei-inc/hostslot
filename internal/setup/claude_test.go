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
	cmd := HookCommand("C:/bin/jobgate.exe")
	res, err := Claude(path, cmd, false, false)
	if err != nil || res != Added {
		t.Fatalf("res %v err %v", res, err)
	}
	got := read(t, path)
	order := []string{`"model"`, `"hooks"`, `"PreToolUse"`, "agent-hook.exe", "jobgate.exe hook claude", `"Stop"`, `"theme"`}
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

	if res, err := Claude(path, cmd, false, false); err != nil || res != Unchanged {
		t.Fatalf("second run: res %v err %v", res, err)
	}
	if res, err := Claude(path, HookCommand("D:/new/jobgate.exe"), false, false); err != nil || res != Updated {
		t.Fatalf("moved exe: res %v err %v", res, err)
	}
	if got := read(t, path); strings.Contains(got, "C:/bin/jobgate.exe") || strings.Count(got, "hook claude") != 1 {
		t.Fatalf("not updated in place:\n%s", got)
	}
}

func TestClaudeRemove(t *testing.T) {
	path := write(t, existing)
	if _, err := Claude(path, HookCommand("C:/bin/jobgate.exe"), false, false); err != nil {
		t.Fatal(err)
	}
	res, err := Claude(path, "", true, false)
	if err != nil || res != Removed {
		t.Fatalf("res %v err %v", res, err)
	}
	got := read(t, path)
	if strings.Contains(got, "jobgate") || !strings.Contains(got, "agent-hook.exe") {
		t.Fatalf("remove touched the wrong entries:\n%s", got)
	}
	if res, _ := Claude(path, "", true, false); res != Unchanged {
		t.Fatalf("second remove: %v", res)
	}
}

func TestClaudeCreatesMissingFileAndDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	if res, err := Claude(path, HookCommand("/usr/local/bin/jobgate"), false, true); err != nil || res != Added {
		t.Fatalf("dry run: res %v err %v", res, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the file")
	}
	if _, err := Claude(path, HookCommand("/usr/local/bin/jobgate"), false, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, `"command": "/usr/local/bin/jobgate hook claude"`) {
		t.Fatalf("unexpected file:\n%s", got)
	}
}

func TestHookCommandQuotesSpaces(t *testing.T) {
	cmd := HookCommand(`C:\Program Files\jobgate\jobgate.exe`)
	if cmd != `"C:/Program Files/jobgate/jobgate.exe" hook claude` {
		t.Fatalf("got %s", cmd)
	}
	if _, ours := jobgateCommand([]byte(`{"hooks":[{"command":` + quote(cmd) + `}]}`)); !ours {
		t.Fatal("quoted command not recognized as jobgate's")
	}
}

func TestClaudeRejectsInvalidSettings(t *testing.T) {
	path := write(t, `{"hooks": [1, 2]}`)
	if _, err := Claude(path, "x hook claude", false, false); err == nil {
		t.Fatal("accepted hooks that are not an object")
	}
	if got := read(t, path); got != `{"hooks": [1, 2]}` {
		t.Fatalf("invalid file was rewritten: %s", got)
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
