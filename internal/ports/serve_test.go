package ports

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func pkgDir(t *testing.T, json string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "web")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectFindsTheToolThroughScripts(t *testing.T) {
	dir := pkgDir(t, `{"name":"@acme/shop","scripts":{"dev":"vite --port 5173","start":"next dev","serve":"node server.js"}}`)
	cases := []struct {
		args    []string
		tool    string
		def     int
		applied []string
	}{
		{[]string{"pnpm", "dev"}, "vite", 5173, []string{"pnpm", "dev", "--port", "20001", "--strictPort"}},
		{[]string{"npm", "run", "dev"}, "vite", 5173, []string{"npm", "run", "dev", "--", "--port", "20001", "--strictPort"}},
		{[]string{"pnpm", "exec", "vite"}, "vite", 5173, []string{"pnpm", "exec", "vite", "--port", "20001", "--strictPort"}},
		{[]string{"npx", "vite"}, "vite", 5173, []string{"npx", "vite", "--port", "20001", "--strictPort"}},
		// Next.js reads PORT: nothing to append.
		{[]string{"yarn", "start"}, "next", 3000, []string{"yarn", "start"}},
		{[]string{"pnpm", "serve"}, "", 0, []string{"pnpm", "serve"}},
		// A placeholder wins over the tool's flags.
		{[]string{"vite", "--port", "{port}"}, "vite", 5173, []string{"vite", "--port", "20001"}},
	}
	for _, c := range cases {
		p := Detect(c.args, dir)
		tool := ""
		if p.Tool != nil {
			tool = p.Tool.Name
		}
		if tool != c.tool || p.Default != c.def || p.Name != "shop" {
			t.Fatalf("%v: tool %q default %d name %q", c.args, tool, p.Default, p.Name)
		}
		if got := p.Apply(c.args, "20001"); !reflect.DeepEqual(got, c.applied) {
			t.Fatalf("%v: applied %v", c.args, got)
		}
	}
}

func TestServiceNameFallsBackToTheDirectory(t *testing.T) {
	dir := pkgDir(t, `{"scripts":{"dev":"vite"}}`)
	if p := Detect([]string{"pnpm", "dev"}, dir); p.Name != "web" {
		t.Fatalf("got %q", p.Name)
	}
	if p := Detect([]string{"go", "run", "."}, t.TempDir()); p.Name != "app" || p.Tool != nil {
		t.Fatalf("got %+v", p)
	}
}

func TestEnvName(t *testing.T) {
	if got := EnvName("admin-ui"); got != "PORT_ADMIN_UI" {
		t.Fatalf("got %s", got)
	}
}
