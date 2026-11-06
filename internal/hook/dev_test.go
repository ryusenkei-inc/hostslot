package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultDevServer(t *testing.T) {
	for _, c := range []struct {
		line string
		dev  bool
	}{
		{"pnpm dev", true},
		{"pnpm run dev:web", true},
		{"npm start", true},
		{"pnpm --filter web dev", true},
		{"vite", true},
		{"vite --port 3000", true},
		{"npx vite preview", true},
		{"vite build", false},
		{"next dev", true},
		{"next build", false},
		{"pnpm build", false},
		{"pnpm developer", false},
	} {
		if got := DefaultDevServer.MatchString(c.line); got != c.dev {
			t.Fatalf("%q: got %v", c.line, got)
		}
	}
}

func claudeRun(t *testing.T, command, cwd string, o ClaudeOptions) map[string]any {
	t.Helper()
	in, _ := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": cwd, "tool_input": map[string]any{"command": command, "run_in_background": true}})
	var out bytes.Buffer
	if err := Claude(bytes.NewReader(in), &out, o); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		return nil
	}
	var res struct {
		HookSpecificOutput map[string]any `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res.HookSpecificOutput
}

func TestClaudeRunsDevServersWithServeAndSaysWhere(t *testing.T) {
	var gotDir string
	var gotLabels []string
	o := ClaudeOptions{DevContext: func(dir string, labels []string) string {
		gotDir, gotLabels = dir, labels
		return "shop on http://localhost:21643"
	}}
	out := claudeRun(t, "pnpm build && pnpm dev", "/r", o)
	cmd := out["updatedInput"].(map[string]any)["command"].(string)
	if !strings.Contains(cmd, "hostslot run") || !strings.HasSuffix(cmd, "&& hostslot serve -- pnpm dev") {
		t.Fatalf("command: %s", cmd)
	}
	if out["additionalContext"] != "shop on http://localhost:21643" || gotDir != "/r" || len(gotLabels) != 1 || gotLabels[0] != "pnpm dev" {
		t.Fatalf("context %v, dir %q, labels %v", out["additionalContext"], gotDir, gotLabels)
	}
	// After a cd the worktree isn't known.
	claudeRun(t, "cd apps/web && pnpm dev", "/r", o)
	if gotDir != "" {
		t.Fatalf("dir after cd: %q", gotDir)
	}
}

func TestAScriptThatStartsADevServerIsOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"web":"vite --host"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := claudeRun(t, "pnpm web", dir, ClaudeOptions{})
	if out == nil {
		t.Fatal("not rewritten")
	}
	if cmd := out["updatedInput"].(map[string]any)["command"].(string); cmd != "hostslot serve -- pnpm web" {
		t.Fatalf("command: %s", cmd)
	}
}

func TestCodexServePrefixInPowerShell(t *testing.T) {
	in := codexEvent("pnpm dev", "bypassPermissions")
	res, raw := runCodex(t, in, CodexOptions{Exe: "C:/h/hostslot.exe", PowerShell: true})
	if res.HookSpecificOutput.UpdatedInput["command"] != "& 'C:/h/hostslot.exe' serve -- pnpm dev" {
		t.Fatalf("got %s", raw)
	}
}
