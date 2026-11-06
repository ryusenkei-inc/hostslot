package hook

import (
	"strings"
	"testing"
)

func mark(label string, count int) string { return "[" + label + "] " }

func TestGateInPlace(t *testing.T) {
	cases := []struct{ line, want string }{
		// The watch loop after the tests runs ungated.
		{"cargo test -q; gh pr checks 11 --watch", "[cargo test -q] cargo test -q; gh pr checks 11 --watch"},
		// cd stays in the agent's shell.
		{"cd apps/site && pnpm test 2>&1 | tail -20", "cd apps/site && [pnpm test] pnpm test 2>&1 | tail -20"},
		// Assignments reach the command through hostslot's environment.
		{"CI=1 FOO=x pnpm test --run", "CI=1 FOO=x [pnpm test --run] pnpm test --run"},
		{"pnpm lint && pnpm test", "[pnpm lint] pnpm lint && [pnpm test] pnpm test"},
		{"echo $(go test ./...)", "echo $([go test ./...] go test ./...)"},
		{"bash -c 'cd x && npx vitest run'", "[npx vitest run] bash -c 'cd x && npx vitest run'"},
		{"pnpm test # run the tests", "[pnpm test] pnpm test # run the tests"},
		{"for d in a b; do (cd $d && go test ./...); done", "for d in a b; do (cd $d && [go test ./...] go test ./...); done"},
	}
	for _, c := range cases {
		got, _, ok := GateInPlace(c.line, "", nil, mark)
		if !ok || got != c.want {
			t.Errorf("%q:\n got %q (%v)\nwant %q", c.line, got, ok, c.want)
		}
	}
}

func TestGateInPlaceLeavesLinesAlone(t *testing.T) {
	for _, line := range []string{
		"git status",
		"python - <<'EOF'\nprint('pnpm test')\nEOF",
		"HOSTSLOT_DISABLE=1 pnpm test",
		"hostslot run -- pnpm test",
		"pnpm test 'unclosed",
	} {
		if got, _, ok := GateInPlace(line, "", nil, mark); ok {
			t.Errorf("%q rewritten to %q", line, got)
		}
	}
}

func TestGateInPlaceSharesTheWait(t *testing.T) {
	var counts []int
	_, labels, ok := GateInPlace("pnpm lint; pnpm test", "", nil, func(label string, count int) string {
		counts = append(counts, count)
		return ""
	})
	if !ok || strings.Join(labels, "|") != "pnpm lint|pnpm test" || counts[0] != 2 || counts[1] != 2 {
		t.Fatalf("labels %q counts %v", labels, counts)
	}
	b := Budget{MaxWait: 8 * 60e9, Timeout: 2 * 60e9}.share(2)
	if b.MaxWait != 4*60e9 || b.Timeout != 2*60e9 {
		t.Fatalf("share: %+v", b)
	}
}
