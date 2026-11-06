package hook

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
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

func TestCodexRewritesOnlyWithoutApprovals(t *testing.T) {
	var out bytes.Buffer
	if err := Codex(strings.NewReader(codexEvent("pnpm test", "bypassPermissions")), &out, CodexOptions{}); err != nil {
		t.Fatal(err)
	}
	var res struct {
		HookSpecificOutput struct {
			PermissionDecision string         `json:"permissionDecision"`
			UpdatedInput       map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	got := res.HookSpecificOutput
	if got.PermissionDecision != "allow" || got.UpdatedInput["command"] != "jobgate run --weight 1 --priority background --source codex --as 'pnpm test' -- sh -c 'pnpm test'" {
		t.Fatalf("unexpected output: %s", out.String())
	}

	for _, in := range []string{
		codexEvent("pnpm test", "default"),
		codexEvent("git status", "bypassPermissions"),
		`{"tool_name":"apply_patch","permission_mode":"bypassPermissions","tool_input":{"command":"pnpm test"}}`,
	} {
		out.Reset()
		if err := Codex(strings.NewReader(in), &out, CodexOptions{}); err != nil || out.Len() != 0 {
			t.Fatalf("%s: output %q err %v", in, out.String(), err)
		}
	}
}

func TestWrapCodexPowerShell(t *testing.T) {
	line := `Write-Output "it's $env:USERNAME"; exit 3`
	got := WrapCodex(line, "it's", 2, "pwsh")
	prefix := "jobgate run --weight 2 --priority background --source codex --as 'it''s' -- 'pwsh' -NoProfile -NonInteractive -EncodedCommand "
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("got %s", got)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, prefix))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("not UTF-16LE base64: %v", err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if decoded := string(utf16.Decode(units)); decoded != line {
		t.Fatalf("decoded %q, want %q", decoded, line)
	}
}

func TestWrapCodexRunsInPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell wrapping is for Windows")
	}
	shell := "pwsh"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell"
	}
	wrapped := WrapCodex(`Write-Output "a'b"; exit 3`, "x", 1, shell)
	// Start what jobgate would start after admission: the shell with the
	// encoded command.
	fields := strings.Fields(wrapped)
	cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-EncodedCommand", fields[len(fields)-1])
	out, err := cmd.Output()
	exit, _ := err.(*exec.ExitError)
	if exit == nil || exit.ExitCode() != 3 || strings.TrimSpace(string(out)) != "a'b" {
		t.Fatalf("output %q err %v", out, err)
	}
}
