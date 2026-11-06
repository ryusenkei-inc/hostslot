package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func codexEvent(command, mode string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"permission_mode": mode,
		"tool_input":      map[string]any{"command": command},
	})
	return string(b)
}

type codexResult struct {
	HookSpecificOutput struct {
		PermissionDecision string         `json:"permissionDecision"`
		UpdatedInput       map[string]any `json:"updatedInput"`
	} `json:"hookSpecificOutput"`
}

func runCodex(t *testing.T, in string, o CodexOptions) (codexResult, string) {
	t.Helper()
	var out bytes.Buffer
	if err := Codex(strings.NewReader(in), &out, o); err != nil {
		t.Fatal(err)
	}
	var res codexResult
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatalf("%v: %s", err, out.String())
		}
	}
	return res, out.String()
}

func TestCodexGatesHeavyCommandsInPlace(t *testing.T) {
	res, raw := runCodex(t, codexEvent("cd web && pnpm test; git status", "bypassPermissions"), CodexOptions{})
	got := res.HookSpecificOutput
	want := "cd web && loadslot run --weight 1 --priority background --source codex --as 'pnpm test' -- pnpm test; git status"
	if got.PermissionDecision != "allow" || got.UpdatedInput["command"] != want {
		t.Fatalf("got %s", raw)
	}
}

func TestCodexPowerShellPrefix(t *testing.T) {
	o := CodexOptions{Exe: `C:\Program Files\loadslot\loadslot.exe`, Weight: 2, PowerShell: true}
	res, raw := runCodex(t, codexEvent(`Set-Location web; go test ./...`, "bypassPermissions"), o)
	want := `Set-Location web; & 'C:/Program Files/loadslot/loadslot.exe' run --weight 2 --priority background --source codex --as 'go test ./...' -- go test ./...`
	if res.HookSpecificOutput.UpdatedInput["command"] != want {
		t.Fatalf("got %s", raw)
	}
	if got := powerShellQuote("it's \u2019x"); got != "'it''s \u2019\u2019x'" {
		t.Fatalf("quote: %s", got)
	}
}

func TestCodexLeavesOtherCallsAlone(t *testing.T) {
	for _, in := range []string{
		codexEvent("pnpm test", "default"),
		codexEvent("pnpm test", "acceptEdits"),
		codexEvent("git status", "bypassPermissions"),
		`{"tool_name":"apply_patch","permission_mode":"bypassPermissions","tool_input":{"command":"pnpm test"}}`,
	} {
		if _, raw := runCodex(t, in, CodexOptions{}); raw != "" {
			t.Fatalf("%s: rewritten to %s", in, raw)
		}
	}
}
