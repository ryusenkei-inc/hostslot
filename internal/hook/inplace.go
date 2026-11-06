package hook

import (
	"regexp"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// GateInPlace returns line with each heavy simple command prefixed by
// prefix(label, count of gated commands), so only those commands go through
// loadslot and the rest of the line keeps running in the agent's own shell:
// a slot is not held
// through a following `gh pr checks --watch`, and a `cd` carries over to the
// agent's next command. Everything else in the line is left byte for byte;
// the prefix goes right before the command's first word, after any
// assignments (`CI=1 pnpm test`), which then reach the command through
// loadslot's environment.
//
// A `sh -c` / `bash -c` call whose script has heavy commands is prefixed as
// a whole. It reports false, leaving the line alone, when nothing is heavy,
// the line does not parse, sets LOADSLOT_DISABLE=1 or runs loadslot itself.
func GateInPlace(line, dir string, heavy *regexp.Regexp, prefix func(label string, count int) string) (string, []string, bool) {
	if heavy == nil {
		heavy = DefaultHeavy
	}
	if strings.Contains(line, "LOADSLOT_DISABLE=1") {
		return "", nil, false
	}
	var scripts packageScripts
	if dir != "" {
		scripts = loadScripts(dir)
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash), syntax.KeepComments(true)).Parse(strings.NewReader(line), "")
	if err != nil {
		return "", nil, false
	}
	type site struct {
		offset uint
		label  string
	}
	var sites []site
	ours := false
	syntax.Walk(file, func(n syntax.Node) bool {
		call, isCall := n.(*syntax.CallExpr)
		if !isCall || len(call.Args) == 0 {
			return true
		}
		words := make([]string, len(call.Args))
		for i, w := range call.Args {
			words[i] = wordText(w)
		}
		if words[0] == "loadslot" {
			ours = true
			return false
		}
		label := strings.Join(words, " ")
		if isHeavyCall(words, label, heavy, scripts) {
			sites = append(sites, site{call.Args[0].Pos().Offset(), label})
		} else if script, ok := shellScript(call.Args); ok {
			if inner := heavyCommands(script, heavy, scripts, 0); len(inner) > 0 {
				sites = append(sites, site{call.Args[0].Pos().Offset(), strings.Join(inner, " && ")})
			}
		}
		return true
	})
	if ours || len(sites) == 0 {
		return "", nil, false
	}
	// Insert from the end, so earlier offsets stay valid.
	sort.Slice(sites, func(a, b int) bool { return sites[a].offset > sites[b].offset })
	out := line
	labels := make([]string, len(sites))
	for i, s := range sites {
		out = out[:s.offset] + prefix(s.label, len(sites)) + out[s.offset:]
		labels[len(sites)-1-i] = s.label
	}
	return out, labels, true
}

func isHeavyCall(words []string, label string, heavy *regexp.Regexp, scripts packageScripts) bool {
	if heavy.MatchString(label) {
		return true
	}
	if name, ok := scriptCall(words); ok {
		body, ok := scripts[name]
		return ok && len(heavyCommands(body, heavy, scripts, 1)) > 0
	}
	return false
}
