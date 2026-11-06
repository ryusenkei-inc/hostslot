package ports

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Tool is a dev server whose port Hostslot knows how to hand over.
type Tool struct {
	Name string
	// Default is the port it uses when told nothing.
	Default int
	// Flags pass the port when the tool doesn't read PORT; "{port}" is
	// replaced. Only tools checked to let a later flag win over one already
	// in the script are listed with flags.
	Flags []string
}

// Tools are the dev servers known by name. Others get PORT and "{port}".
var Tools = []Tool{
	// Vite ignores PORT and moves to the next port when 5173 is taken;
	// --strictPort makes a taken port fail instead. Checked with Vite 8.3.
	{Name: "vite", Default: 5173, Flags: []string{"--port", "{port}", "--strictPort"}},
	// Next.js reads PORT.
	{Name: "next", Default: 3000},
}

// Placeholder in a command line is replaced by the port.
const Placeholder = "{port}"

// Plan is how a dev server command gets its port.
type Plan struct {
	// Name of the service: the package's name without its scope, else its
	// directory, else "app".
	Name string
	// Tool is the known dev server it runs, if any.
	Tool *Tool
	// Default is the tool's default port (0 when unknown).
	Default int
	// npmScript is set for `npm run <script>`, which needs `--` before
	// extra arguments.
	npmScript bool
}

var packageManagers = map[string]bool{"pnpm": true, "npm": true, "yarn": true, "bun": true}

// Detect looks at a command run in dir: which known tool it starts,
// directly, through npx and the like, or through a package script.
func Detect(args []string, dir string) Plan {
	pkg := nearestPackage(dir)
	plan := Plan{Name: pkg.service()}
	words := args
	if len(words) > 0 && packageManagers[base(words[0])] {
		if script, npm, ok := scriptOf(words); ok {
			plan.npmScript = npm
			words = strings.Fields(pkg.Scripts[script])
		}
	}
	for _, w := range commandWords(words) {
		for i := range Tools {
			if base(w) == Tools[i].Name {
				plan.Tool = &Tools[i]
				plan.Default = Tools[i].Default
				return plan
			}
		}
	}
	return plan
}

// Apply returns the command line with the port in it: "{port}" replaced
// where written, else the tool's flags appended.
func (p Plan) Apply(args []string, port string) []string {
	out := make([]string, len(args))
	replaced := false
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, Placeholder, port)
		replaced = replaced || out[i] != a
	}
	if replaced || p.Tool == nil || len(p.Tool.Flags) == 0 {
		return out
	}
	if p.npmScript && !contains(out, "--") {
		out = append(out, "--")
	}
	for _, f := range p.Tool.Flags {
		out = append(out, strings.ReplaceAll(f, Placeholder, port))
	}
	return out
}

// EnvName is the variable for a named port: PORT_WEB for "web".
func EnvName(name string) string {
	return "PORT_" + strings.ToUpper(nonWord.ReplaceAllString(name, "_"))
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9]+`)

// commandWords skips runners that start a package's binary: `npx vite`,
// `pnpm exec vite`, `bunx vite`, `pnpm dlx vite`.
func commandWords(words []string) []string {
	for len(words) > 0 {
		switch base(words[0]) {
		case "npx", "bunx":
			words = words[1:]
		case "pnpm", "npm", "yarn":
			if len(words) > 1 && (words[1] == "exec" || words[1] == "dlx") {
				words = words[2:]
				continue
			}
			return words
		default:
			return words
		}
	}
	return words
}

// scriptOf returns the script a package manager runs: `pnpm dev`,
// `pnpm run dev`, `npm run dev`, `yarn dev`, `bun run dev`.
func scriptOf(words []string) (script string, npm bool, ok bool) {
	pm := base(words[0])
	rest := words[1:]
	if len(rest) > 0 && rest[0] == "run" {
		rest = rest[1:]
	} else if pm == "npm" {
		return "", false, false
	}
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") || rest[0] == "exec" || rest[0] == "dlx" {
		return "", false, false
	}
	return rest[0], pm == "npm", true
}

type packageJSON struct {
	Name    string            `json:"name"`
	Scripts map[string]string `json:"scripts"`
	dir     string
}

func (p packageJSON) service() string {
	if p.Name != "" {
		name := p.Name[strings.LastIndex(p.Name, "/")+1:]
		if name != "" {
			return name
		}
	}
	if p.dir != "" {
		return filepath.Base(p.dir)
	}
	return "app"
}

// nearestPackage reads the package.json nearest to dir, looking up the
// tree as package managers do.
func nearestPackage(dir string) packageJSON {
	for d := filepath.Clean(dir); ; {
		if b, err := os.ReadFile(filepath.Join(d, "package.json")); err == nil {
			var p packageJSON
			_ = json.Unmarshal(b, &p)
			p.dir = d
			return p
		}
		parent := filepath.Dir(d)
		if parent == d {
			return packageJSON{}
		}
		d = parent
	}
}

func base(word string) string {
	b := filepath.Base(filepath.FromSlash(word))
	for _, ext := range []string{".cmd", ".exe", ".ps1"} {
		b = strings.TrimSuffix(b, ext)
	}
	return b
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
