package runner

// contextVars are the environment variables recorded with a job, by the
// tool that sets them in its terminals, so its integration can show where a
// job came from. A new tool is added here; nothing else knows about it.
var contextVars = map[string][]string{
	"orca": {"ORCA_PANE_KEY", "ORCA_TAB_ID", "ORCA_WORKTREE_ID"},
}

func jobContext(getenv func(string) string) map[string]string {
	var ctx map[string]string
	for _, vars := range contextVars {
		for _, k := range vars {
			if v := getenv(k); v != "" {
				if ctx == nil {
					ctx = map[string]string{}
				}
				ctx[k] = v
			}
		}
	}
	return ctx
}
