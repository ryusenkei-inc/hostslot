package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// maxScriptHops bounds how many package scripts deep a call is followed
// (`pnpm tc` running `pnpm tc:web` running `tsc`).
const maxScriptHops = 3

// HeavyIn is Heavy for a line run in dir. It also counts a package script
// call (`pnpm tc:web`, `npm run build:all`) as heavy when the script itself
// runs a heavy command: script names differ between repositories, what they
// run does not. Scripts come from the package.json nearest to dir.
func HeavyIn(line, dir string, heavy *regexp.Regexp) []string {
	var s packageScripts
	if dir != "" {
		s = loadScripts(dir)
	}
	return heavyCommands(line, heavy, s, 0)
}

type packageScripts map[string]string

// loadScripts reads the scripts of the package.json nearest to dir, looking
// up the tree as package managers do.
func loadScripts(dir string) packageScripts {
	for d := filepath.Clean(dir); ; {
		if b, err := os.ReadFile(filepath.Join(d, "package.json")); err == nil {
			var pkg struct {
				Scripts packageScripts `json:"scripts"`
			}
			if json.Unmarshal(b, &pkg) == nil {
				return pkg.Scripts
			}
			return nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil
		}
		d = parent
	}
}

// packageManagerCommands are pnpm and yarn commands that a bare
// `pnpm <name>` or `yarn <name>` runs instead of a script of that name.
var packageManagerCommands = []string{
	"add", "audit", "bin", "cache", "config", "create", "dedupe", "dlx", "exec", "i", "import", "info",
	"init", "install", "link", "list", "ls", "outdated", "pack", "prune", "publish", "rebuild", "remove",
	"rm", "root", "run", "setup", "store", "un", "uninstall", "unlink", "up", "update", "upgrade", "why",
	"workspace", "workspaces",
}

// scriptCall returns the script a command runs: `pnpm run x`, `pnpm x`,
// `npm run x`, `npm run-script x`, `yarn x`, `yarn run x`, `bun run x`.
// Calls across workspace packages (`-r`, `--filter`) run other packages'
// scripts, which are not looked up.
func scriptCall(words []string) (string, bool) {
	if len(words) < 2 {
		return "", false
	}
	tool, rest := words[0], words[1:]
	for _, w := range rest {
		if w == "-r" || w == "--recursive" || strings.HasPrefix(w, "--filter") || w == "-F" || w == "--workspace" || w == "-w" {
			return "", false
		}
	}
	switch tool {
	case "npm", "bun":
		if len(rest) >= 2 && (rest[0] == "run" || rest[0] == "run-script") {
			return rest[1], true
		}
	case "pnpm", "yarn":
		if rest[0] == "run" && len(rest) >= 2 {
			return rest[1], true
		}
		if !strings.HasPrefix(rest[0], "-") && !slices.Contains(packageManagerCommands, rest[0]) {
			return rest[0], true
		}
	}
	return "", false
}
