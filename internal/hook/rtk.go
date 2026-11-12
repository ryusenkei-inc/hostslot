package hook

import (
	"regexp"
	"slices"
	"strings"
)

// AcceptRTK reports whether rewritten, the line `rtk rewrite` made of line,
// may replace it: rtk only put `rtk` before some of its commands, and none
// of those commands is a dev server (dev, DefaultDevServer when nil).
//
// rtk also maps some commands to its own (`pnpm lint` to `rtk lint`, which
// runs ESLint instead of the package's lint script); such a rewrite changes
// what runs, so it is refused, and so is filtering the output of a dev
// server, which runs until it is stopped.
func AcceptRTK(line, rewritten, dir string, dev *regexp.Regexp) bool {
	if dev == nil {
		dev = DefaultDevServer
	}
	before, ok := simpleCommands(line, 0)
	if !ok {
		return false
	}
	after, ok := simpleCommands(rewritten, 0)
	if !ok || len(after) != len(before) {
		return false
	}
	changed := false
	for i, words := range after {
		inner := throughRTK(words)
		if !slices.Equal(inner, before[i]) {
			return false
		}
		if len(inner) == len(words) {
			continue
		}
		changed = true
		c := strings.Join(inner, " ")
		if dev.MatchString(c) || len(HeavyIn(c, dir, dev)) > 0 {
			return false
		}
	}
	return changed
}
