package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAcceptRTK(t *testing.T) {
	accepted := map[string]string{
		"git status":                 "rtk git status",
		"pnpm run check":             "rtk pnpm run check",
		"git log -5 && pnpm build":   "rtk git log -5 && pnpm build",
		"cat foo | grep x":           "cat foo | rtk grep x",
		"cd apps/site && go test ./": "cd apps/site && rtk go test ./",
	}
	for line, rewritten := range accepted {
		if !AcceptRTK(line, rewritten, "", nil) {
			t.Errorf("refused %q -> %q", line, rewritten)
		}
	}
	refused := [][2]string{
		{"pnpm lint", "rtk lint"},                // rtk's ESLint instead of the script
		{"vitest run", "rtk vitest"},             // a word dropped
		{"npx tsc --noEmit", "rtk tsc --noEmit"}, // a word dropped
		{"git status", "git status"},             // nothing to do
		{"pnpm run dev", "rtk pnpm run dev"},     // a dev server
		{"git status; vite", "rtk git status; rtk vite"},
		{"git status", "rtk git status 'x"},            // does not parse
		{"git status && ls", "rtk git status"},         // a command lost
		{"echo a", "rtk echo a && rm x"},               // a command added
		{"vitest packages/a", "rtk vitest packages/a"}, // failures hidden
	}
	for _, c := range refused {
		if AcceptRTK(c[0], c[1], "", nil) {
			t.Errorf("accepted %q -> %q", c[0], c[1])
		}
	}
}

func TestHeavyLooksThroughRTK(t *testing.T) {
	if got := Heavy("rtk go test ./... && rtk git status", nil); strings.Join(got, "|") != "go test ./..." {
		t.Fatalf("got %q", got)
	}
}

// claudeRTK runs the Claude hook on a Bash command with rtk rewriting it to
// rewritten, and returns the command it gives back ("" for none) and the
// tool timeout.
func claudeRTK(t *testing.T, command, rewritten string) (string, any) {
	t.Helper()
	in, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}})
	var out bytes.Buffer
	o := ClaudeOptions{RTK: func(line string) (string, bool) {
		if line != command {
			t.Fatalf("rtk got %q", line)
		}
		return rewritten, rewritten != ""
	}}
	if err := Claude(bytes.NewReader(in), &out, o); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		return "", nil
	}
	var res struct {
		HookSpecificOutput struct {
			UpdatedInput map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	cmd, _ := res.HookSpecificOutput.UpdatedInput["command"].(string)
	return cmd, res.HookSpecificOutput.UpdatedInput["timeout"]
}

func TestClaudeGatesTheRTKRewrite(t *testing.T) {
	got, timeout := claudeRTK(t, "pnpm run check", "rtk pnpm run check")
	want := "hostslot run --weight 1 --priority background --source claude --as 'pnpm run check' --max-wait 8m0s --timeout 2m0s -- rtk pnpm run check"
	if got != want || timeout != float64(600000) {
		t.Fatalf("got %q (timeout %v), want %q", got, timeout, want)
	}
}

func TestClaudeReturnsALightRTKRewriteUngated(t *testing.T) {
	got, timeout := claudeRTK(t, "git log -5", "rtk git log -5")
	if got != "rtk git log -5" || timeout != nil {
		t.Fatalf("got %q (timeout %v)", got, timeout)
	}
}

func TestClaudeIgnoresARefusedRTKRewrite(t *testing.T) {
	if got, _ := claudeRTK(t, "pnpm lint", "rtk lint"); got != "hostslot run --weight 1 --priority background --source claude --as 'pnpm lint' --max-wait 8m0s --timeout 2m0s -- pnpm lint" {
		t.Fatalf("got %q", got)
	}
	if got, _ := claudeRTK(t, "echo hi", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestClaudeSkipsRTKWhenDisabled(t *testing.T) {
	in := `{"tool_name":"Bash","tool_input":{"command":"HOSTSLOT_DISABLE=1 git status"}}`
	var out bytes.Buffer
	o := ClaudeOptions{RTK: func(string) (string, bool) {
		t.Fatal("rtk called")
		return "", false
	}}
	if err := Claude(strings.NewReader(in), &out, o); err != nil || out.Len() != 0 {
		t.Fatalf("output %q err %v", out.String(), err)
	}
}
