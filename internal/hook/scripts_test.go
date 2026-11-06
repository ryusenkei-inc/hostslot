package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shapes of Orca's own typecheck scripts (stablyai/orca#28662).
const orcaLikePackage = `{
  "scripts": {
    "tc": "pnpm tc:web && pnpm tc:node",
    "tc:web": "tsc --noEmit -p config/tsconfig.tc.web.json",
    "tc:node": "tsc --noEmit -p config/tsconfig.tc.node.json",
    "typecheck:e2e": "node config/scripts/run-typecheck-e2e.mjs",
    "loop": "pnpm loop",
    "fmt": "oxfmt ."
  }
}`

func TestHeavyInFollowsPackageScripts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(orcaLikePackage), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "src", "main")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"pnpm tc:web":              "pnpm tc:web",
		"pnpm run tc:node":         "pnpm run tc:node",
		"npm run tc:web":           "npm run tc:web",
		"yarn tc":                  "yarn tc",
		"pnpm tc 2>&1 | tail -20":  "pnpm tc",
		"cd src && pnpm tc:web":    "pnpm tc:web",
		"pnpm typecheck:e2e":       "",
		"pnpm fmt":                 "",
		"pnpm loop":                "",
		"pnpm install":             "",
		"pnpm -r tc:web":           "",
		"pnpm --filter app tc:web": "",
		"pnpm unknown-script":      "",
	}
	for line, want := range cases {
		got := strings.Join(HeavyIn(line, sub, nil), "|")
		if got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
	if got := HeavyIn("pnpm tc:web", "", nil); len(got) != 0 {
		t.Errorf("without a directory, scripts are not looked up: %q", got)
	}
}

func TestScriptCall(t *testing.T) {
	cases := map[string]string{
		"pnpm tc":                "tc",
		"pnpm run tc":            "tc",
		"npm run-script build":   "build",
		"bun run test:unit":      "test:unit",
		"yarn run lint":          "lint",
		"pnpm exec tsc":          "",
		"pnpm add -D vitest":     "",
		"npm test":               "",
		"pnpm --silent tc":       "",
		"pnpm --filter x run tc": "",
	}
	for line, want := range cases {
		got, ok := scriptCall(strings.Fields(line))
		if (want != "") != ok || got != want {
			t.Errorf("%q: got %q %v, want %q", line, got, ok, want)
		}
	}
}

func TestClaudeUsesTheCallsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(orcaLikePackage), 0o644); err != nil {
		t.Fatal(err)
	}
	in := `{"tool_name":"Bash","cwd":` + quoteJSON(root) + `,"tool_input":{"command":"pnpm tc:web"}}`
	var out strings.Builder
	if err := Claude(strings.NewReader(in), &out, ClaudeOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--as 'pnpm tc:web'") {
		t.Fatalf("script call not gated: %s", out.String())
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
