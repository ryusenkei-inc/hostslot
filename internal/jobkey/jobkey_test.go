package jobkey

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestWorktreesShareTheKey(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.MkdirAll(filepath.Join(main, "apps", "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q")
	git(t, main, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt")
	git(t, main, "worktree", "add", "-q", wt)
	if err := os.MkdirAll(filepath.Join(wt, "apps", "site"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := For(filepath.Join(main, "apps", "site"), "pnpm test")
	b := For(filepath.Join(wt, "apps", "site"), "pnpm test")
	if a != b {
		t.Fatalf("worktrees differ:\n%q\n%q", a, b)
	}
	if !strings.HasPrefix(a, "git:") || !strings.HasSuffix(a, "\napps/site\npnpm test") {
		t.Fatalf("unexpected key %q", a)
	}
	if For(main, "pnpm test") == a {
		t.Fatal("different directories inside the repository share a key")
	}
	if For(filepath.Join(main, "apps", "site"), "pnpm lint") == a {
		t.Fatal("different labels share a key")
	}
}

func TestOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	if _, _, ok := repository(dir); ok {
		t.Skip("temp dir is inside a git repository")
	}
	if got, want := For(dir, "go test"), "dir:"+filepath.ToSlash(canonical(dir))+"\ngo test"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSubmoduleGitFile(t *testing.T) {
	root := t.TempDir()
	gitdir := filepath.Join(root, ".git", "modules", "sub")
	sub := filepath.Join(root, "sub")
	for _, d := range []string{gitdir, sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	common, top, ok := repository(sub)
	if !ok || common != gitdir || top != sub {
		t.Fatalf("got %q %q %v", common, top, ok)
	}
}
