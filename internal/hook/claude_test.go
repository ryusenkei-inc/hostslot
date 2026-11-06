package hook

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIsHeavy(t *testing.T) {
	heavy := []string{
		"pnpm test",
		"pnpm check --all",
		"pnpm run build",
		"pnpm -r --if-present typecheck",
		"pnpm --filter @app/site test",
		"pnpm test:coverage",
		"npm run test",
		"npx vitest run src/a.test.ts",
		"pnpm exec tsc",
		"tsc -b",
		"go test ./...",
		"cargo clippy",
		"cd apps/site && pnpm build",
		"CI=1 FOO=bar pnpm test",
		"git status; go vet ./...",
	}
	light := []string{
		"git push",
		"git status",
		"pnpm install",
		"pnpm add -D vitest",
		"npx prose-lint",
		"pnpm lint:prose",
		"ls tests",
		"pnpm testing-library",
		"go mod tidy",
		"jobgate run -- pnpm test",
		"",
		"python - <<'EOF'\nprint('pnpm test')\nEOF",
		`node -e 'p.scripts.test="go test ./... && vitest run"'`,
		`echo "a | pnpm test"`,
		"git log # pnpm test",
		"pnpm test 'unclosed",
		"JOBGATE_DISABLE=1 go test ./...",
	}
	for _, c := range heavy {
		if len(Heavy(c, nil)) == 0 {
			t.Errorf("not heavy: %q", c)
		}
	}
	for _, c := range light {
		if got := Heavy(c, nil); len(got) != 0 {
			t.Errorf("heavy: %q (%q)", c, got)
		}
	}
	if len(Heavy("make all", regexp.MustCompile(`^make\b`))) == 0 || len(Heavy("pnpm test", regexp.MustCompile(`^make\b`))) != 0 {
		t.Error("custom pattern not used")
	}
}

func TestHeavyReturnsTheHeavyCommands(t *testing.T) {
	cases := map[string][]string{
		`sed -i "s/a/b/" x.ts && pnpm test 2>&1 | grep -E "Tests"`: {"pnpm test"},
		"CI=1 pnpm lint && pnpm test --run":                        {"pnpm lint", "pnpm test --run"},
		`bash -c 'cd apps/site && npx vitest run'`:                 {"npx vitest run"},
		"echo $(go test ./...)":                                    {"go test ./..."},
	}
	for line, want := range cases {
		if got := Heavy(line, nil); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
}

func TestClaudeRewritesHeavyBash(t *testing.T) {
	in := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"pnpm test","timeout":600000}}`
	var out bytes.Buffer
	if err := Claude(strings.NewReader(in), &out, ClaudeOptions{Weight: 2}); err != nil {
		t.Fatal(err)
	}
	var res struct {
		HookSpecificOutput struct {
			HookEventName string         `json:"hookEventName"`
			UpdatedInput  map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	got := res.HookSpecificOutput
	if got.HookEventName != "PreToolUse" || got.UpdatedInput["command"] != "jobgate run --weight 2 --priority background --source claude --as 'pnpm test' --max-wait 1m0s --timeout 9m0s -- sh -c 'pnpm test'" {
		t.Fatalf("unexpected output: %s", out.String())
	}
	if got.UpdatedInput["timeout"] != float64(600000) {
		t.Fatalf("other input fields dropped: %v", got.UpdatedInput)
	}
}

func TestClaudeLeavesOtherCallsAlone(t *testing.T) {
	for _, in := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"git status"}}`,
		`{"tool_name":"Read","tool_input":{"file_path":"pnpm test"}}`,
	} {
		var out bytes.Buffer
		if err := Claude(strings.NewReader(in), &out, ClaudeOptions{}); err != nil || out.Len() != 0 {
			t.Fatalf("%s: output %q err %v", in, out.String(), err)
		}
	}
	if err := Claude(strings.NewReader("not json"), &bytes.Buffer{}, ClaudeOptions{}); err == nil {
		t.Fatal("invalid input accepted")
	}
}

func TestWrapQuotesForSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("no sh")
		}
	}
	line := `printf '%s\n' "it's" $HOME`
	wrapped := Wrap("", line, "x", 1, Budget{})
	inner := strings.TrimPrefix(wrapped, "jobgate run --weight 1 --priority background --source claude --as 'x' -- sh -c ")
	// The quoted argument must reach sh as the original line.
	out, err := exec.Command("sh", "-c", "printf '%s' "+inner).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != line {
		t.Fatalf("got %q, want %q", out, line)
	}
}

func TestClaudeBudget(t *testing.T) {
	cases := []struct {
		asked, wait, run time.Duration
	}{
		{0, 8 * time.Minute, 2 * time.Minute},
		{30 * time.Second, 9*time.Minute + 30*time.Second, 30 * time.Second},
		{10 * time.Minute, time.Minute, 9 * time.Minute},
		{time.Hour, time.Minute, 9 * time.Minute},
	}
	for _, c := range cases {
		b, tool := claudeBudget(c.asked)
		if b.MaxWait != c.wait || b.Timeout != c.run || tool != 10*time.Minute {
			t.Errorf("asked %s: got wait %s run %s tool %s", c.asked, b.MaxWait, b.Timeout, tool)
		}
	}
}

func TestClaudeBackgroundCallsHaveNoBudget(t *testing.T) {
	in := `{"tool_name":"Bash","tool_input":{"command":"pnpm test","run_in_background":true}}`
	var out bytes.Buffer
	if err := Claude(strings.NewReader(in), &out, ClaudeOptions{}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "--max-wait") || strings.Contains(s, "--timeout") || strings.Contains(s, `"timeout"`) {
		t.Fatalf("background call got a budget: %s", s)
	}
}

func TestWrapRunsTheGivenExecutable(t *testing.T) {
	got := Wrap(`C:\tools\job gate\jobgate.exe`, "pnpm test", "pnpm test", 1, Budget{})
	if !strings.HasPrefix(got, "'C:/tools/job gate/jobgate.exe' run --weight 1 ") {
		t.Fatalf("got %s", got)
	}
}
