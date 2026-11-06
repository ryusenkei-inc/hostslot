package hook

import (
	"regexp"
	"strings"
)

// DefaultDevServer matches one shell command that starts a dev server:
// package scripts named dev, start, serve or preview (also `dev:web`), and
// dev servers started directly. Builds such as `vite build` don't match.
var DefaultDevServer = regexp.MustCompile(`^(?:` +
	`(?:pnpm|npm|yarn|bun)\s+(?:(?:--filter|-F)\s+\S+\s+)*(?:run\s+)?(?:dev|start|serve|preview)(?::\S+)?(?:\s|$)` +
	`|(?:npx\s+|pnpm\s+(?:exec|dlx)\s+|yarn\s+|bunx\s+)?(?:` +
	`vite(?:\s+(?:dev|serve|preview))?(?:\s+-.*)?$` +
	`|(?:next\s+(?:dev|start)|astro\s+(?:dev|preview)|nuxt\s+dev|wrangler\s+dev|storybook\s+dev)(?:\s|$)` +
	`))`)

// DevContext tells the agent where the dev servers of a line will listen.
// dir is empty when the line changes directory first, so the worktree is
// not known. It may assign ports, so the hook package leaves it to callers.
type DevContext func(dir string, labels []string) string

// gateRules combines heavy and dev-server commands for GateInPlace. A
// command is a dev server when it, or the package script it runs, starts
// one; it then gets the dev prefix instead of a capacity slot.
func gateRules(heavy, dev *regexp.Regexp, dir string, heavyPrefix func(label string, count int) string, devPrefix string) (*regexp.Regexp, func(string, int) string, *[]string) {
	if heavy == nil {
		heavy = DefaultHeavy
	}
	if dev == nil {
		dev = DefaultDevServer
	}
	combined := regexp.MustCompile(`(?:` + heavy.String() + `)|(?:` + dev.String() + `)`)
	var devLabels []string
	prefix := func(label string, count int) string {
		if dev.MatchString(label) || len(HeavyIn(label, dir, dev)) > 0 {
			devLabels = append(devLabels, label)
			return devPrefix
		}
		return heavyPrefix(label, count)
	}
	return combined, prefix, &devLabels
}

// changesDirectory reports whether a line may run its commands elsewhere
// than where it starts, in POSIX shells or PowerShell.
func changesDirectory(line string) bool {
	for _, w := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ';' || r == '&' || r == '|' || r == '(' }) {
		switch strings.ToLower(w) {
		case "cd", "pushd", "set-location", "sl", "push-location":
			return true
		}
	}
	return false
}
