// Package jobkey derives the learning key of a job, so samples of the same
// command add up across runs and across worktrees of one repository.
package jobkey

import (
	"os"
	"path/filepath"
	"strings"
)

// For returns the learning key of label run in dir. Inside a git
// repository it is the repository's common git directory (shared by all of
// its worktrees), the path of dir inside the worktree and the label.
// Outside a repository it is dir and the label.
func For(dir, label string) string {
	dir = filepath.Clean(dir)
	if common, top, ok := repository(dir); ok {
		rel, err := filepath.Rel(top, dir)
		if err != nil {
			rel = "."
		}
		return "git:" + filepath.ToSlash(canonical(common)) + "\n" + filepath.ToSlash(rel) + "\n" + label
	}
	return "dir:" + filepath.ToSlash(canonical(dir)) + "\n" + label
}

// canonical resolves symlinks and, on Windows, short (8.3) names: git writes
// the resolved path into a linked worktree's `.git` file, while the main
// worktree is reached through whatever path the shell used (/var vs
// /private/var on macOS, RUNNER~1 vs runneradmin on Windows).
func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// repository finds the worktree that contains dir: its top directory and the
// common git directory. It reads the files git writes instead of running git,
// since it is called for every job.
func repository(dir string) (common, top string, ok bool) {
	for d := dir; ; {
		dotgit := filepath.Join(d, ".git")
		if st, err := os.Stat(dotgit); err == nil {
			if st.IsDir() {
				return dotgit, d, true
			}
			if gitdir, ok := linkedGitDir(dotgit, d); ok {
				return commonDir(gitdir), d, true
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", "", false
		}
		d = parent
	}
}

// linkedGitDir reads a `.git` file of a linked worktree or submodule:
// "gitdir: <path>", relative to the worktree when not absolute.
func linkedGitDir(dotgit, top string) (string, bool) {
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return "", false
	}
	path, found := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !found {
		return "", false
	}
	path = filepath.FromSlash(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		path = filepath.Join(top, path)
	}
	return filepath.Clean(path), true
}

// commonDir follows the `commondir` file of a linked worktree's git
// directory. A submodule's git directory has none and is its own common
// directory.
func commonDir(gitdir string) string {
	b, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return gitdir
	}
	path := filepath.FromSlash(strings.TrimSpace(string(b)))
	if !filepath.IsAbs(path) {
		path = filepath.Join(gitdir, path)
	}
	return filepath.Clean(path)
}
