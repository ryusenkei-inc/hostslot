# loadslot

A machine-wide gate for heavy local jobs. Wrap any command, and loadslot admits it only when the machine has room for it:

```sh
loadslot run --weight 1 -- pnpm check
```

Formerly `jobgate` (v0.1.0 and v0.2.0). `loadslot setup` replaces a hook registered under the old name, and the first run carries over learned costs and `config.json` from the old data directory. `JOBGATE_*` variables are now `LOADSLOT_*`.

## Why

Running several coding agents in parallel (each in its own worktree, often across repositories) makes every agent start heavy commands at the same time: type checks, test runners, builds. On a 16 GB machine this exhausts memory. Editors and agent IDEs can cap the number of agents, but nothing coordinates the commands those agents run.

loadslot does, across tools and repositories. It works the same from a plain terminal, any editor, Claude Code hooks, git hooks and package scripts.

## How it works

- **Slots:** `floor((total memory x 0.6 - reserve) / cost of a weight-1 job)`, capped by the CPU count and at least 1. With the defaults (1 GB reserve, 2 GB per weight-1 job), a 16 GB machine has 4 slots.
- **Admission:** a job needs free slots for its weight and enough free memory for its estimated peak. The free-memory reading already contains what running jobs use now, so only what they may still grow to is held back: a learned job reports its memory every 2 seconds and holds its remaining headroom (learned peak minus current), since a type check can keep growing for a minute; a job without enough samples holds its estimate for its first 15 seconds. When nothing runs, the next job is always admitted.
- **Order:** interactive jobs before background jobs, first come first served within each. The head of the queue blocks later jobs, so a heavy job is never starved by lighter ones.
- **Learning:** loadslot records the peak memory of each wrapped command, including its child processes, keyed by the command and where it runs: inside a git repository, the repository (shared by all of its worktrees) and the path inside it; elsewhere, the directory. `--as` names the work when the command line varies, such as a wrapper script. After 3 runs, the recent peak replaces the weight hint. A command that turns out light takes 1 slot, even with `--weight 3`.
- **Inner parallelism:** the wrapped command gets its share of CPUs in `VITEST_MAX_WORKERS`, `npm_config_workspace_concurrency` and `pnpm_config_workspace_concurrency` (pnpm's `--workspace-concurrency`: pnpm 10 reads the first, pnpm 11 the second), unless `CI` is set or the variable is already set. `LOADSLOT_WORKERS` is always set for your own scripts.
- **Nesting:** a `loadslot run` inside a gated command runs directly, since the outer job already holds a slot (loadslot sets `LOADSLOT_JOB` for the command). Wrapping at several layers, such as a Claude Code hook, a git hook and package scripts, never deadlocks.
- **Daemon:** the first `loadslot run` starts a small daemon in the background. The CLI talks to it over a Unix socket or a named pipe. The daemon exits after 10 minutes without jobs. After an upgrade, the first `loadslot run` of the newer version retires an older daemon: the old one stops accepting clients and frees its socket or pipe at once, finishes its current jobs and exits, and the run starts a new daemon. Until then, the two don't see each other's jobs; the free-memory check still sees the memory they use.
- **Lock fallback:** when the daemon can't start, loadslot falls back to one machine-wide lock per slot. Slots and memory checks still apply, but there is no priority or queue order.
- **Crash safety:** a job holds its slot through an open connection (or an OS lock in fallback mode). When loadslot or the job is killed, the OS closes it and the slot comes back.

## Install

Download the archive for your OS and CPU (Linux, macOS and Windows; amd64 and arm64) from [Releases](https://github.com/ryusenkei-inc/loadslot/releases), check it against `checksums.txt`, and put `loadslot` (`loadslot.exe`) on your `PATH`.

Or build it with Go 1.26 or later:

```sh
go install github.com/ryusenkei-inc/loadslot/cmd/loadslot@latest
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`.

Then connect your coding agents and check the result:

```sh
loadslot setup    # register the hooks of the agents it finds (Claude Code)
loadslot doctor   # check the binary, the daemon and the hooks
```

To upgrade, replace the binary. On Windows a running daemon keeps `loadslot.exe` open: rename the old file first, then put the new one in place. The next `loadslot run` retires the old daemon.

## Usage

```sh
loadslot run [--weight N] [--priority interactive|background] [--as LABEL] [--source NAME]
            [--max-wait DURATION] [--timeout DURATION] [--max-memory MB] [--keep-children]
            -- <command> [args...]
loadslot status [--json]
loadslot daemon [--idle 10m]
loadslot hook claude [--weight N] [--match REGEX]
loadslot setup [claude] [--remove] [--dry-run] [--settings PATH]
loadslot doctor [--json]
loadslot version
```

- `run` waits for admission, printing a status line to stderr every 5 seconds, then runs the command with stdin, stdout and stderr passed through. It exits with the command's exit code (`128 + signal` when killed by a signal, `127` when the command isn't found, `75` when `--max-wait` ran out, `124` when `--timeout` stopped it, `77` when it went over its memory ceiling).
- `--weight` is the expected cost in slots. It only matters until loadslot has learned the command.
- `--priority` defaults to `interactive` when stdin is a terminal and `background` otherwise.
- `--as` names the work for learning instead of the command line, e.g. `loadslot run --as "pnpm test" -- sh -c 'pnpm test 2>&1 | tail'`.
- `--max-wait 5m` gives up waiting for a slot after 5 minutes (exit `75`, retry later). The command never runs without a slot.
- `--timeout 2m` stops the command's process tree after it has run for 2 minutes (exit `124`). A stopped run is not learned.
- Background jobs (agents, scripts) run at a lower OS priority: below-normal priority class on Windows, `nice 10` on Linux and macOS. People's editors and terminals stay responsive; interactive jobs keep normal priority.
- When the command exits, processes it left running (test workers, browsers) are stopped. `--keep-children` leaves them, for commands that start a daemon on purpose.
- Memory ceiling: a job whose process tree uses more than `jobCeilingFraction` of total memory (default 75%) is stopped as a runaway, with exit `77` and a message naming the ceiling. `--max-memory 6000` sets it for one run in MB; `jobCeilingFraction` of 1 or more turns it off. The stopped run's peak is still learned, so the next run is admitted with room for it.
- `--source claude` names who started the job (default: `$LOADSLOT_SOURCE`), shown in `status`. The job also records `ORCA_PANE_KEY`, `ORCA_TAB_ID` and `ORCA_WORKTREE_ID` when set, so `status --json` tells which Orca pane a job belongs to.
- Ctrl+C reaches the command as usual, and loadslot waits for it to exit and releases the slot. A second Ctrl+C kills the whole process tree.
- `status` shows the slots, free memory, running jobs (command, weight, estimated and current memory, source) and waiting jobs. Without a daemon, it shows how many slot locks are held. `room` (in the text and `--json`) is how many more weight-1 jobs would start now: the free slots, limited by free memory after what running jobs may still grow to, and 0 while any job waits.
- `daemon` runs the daemon in the foreground. You don't need to start it yourself.
- `hook claude` is a Claude Code hook. `setup` registers it for every agent it finds, or for the one named (see [Claude Code](#claude-code)).
- `doctor` checks the binary, whether `loadslot` on the `PATH` is the same file, the daemon (running, version), and each agent's hook (registered, pointing at an existing loadslot), and tells how to fix each problem. It exits 1 on warnings or errors. `--json` prints the checks as `[{"level", "text"}]` for integrations.

Example:

```text
$ loadslot status
mode:    daemon pid 17048
slots:   1/1 used, room for 0 more
memory:  4396 MB free of 16325 MB
cpus:    8
tracker: job object (peak committed memory)

running (1)
  ID  WEIGHT  EST MB  NOW MB  SAMPLES  PRIORITY     SOURCE  AGE  COMMAND  DIR
  4   1       2048    6       0        interactive  -       2s   sleep 7  C:\src\app

waiting (1)
  ID  WEIGHT  EST MB  NOW MB  SAMPLES  PRIORITY    SOURCE  AGE  COMMAND  DIR
  5   1       2048    -       0        background  claude  1s   sleep 1  C:\src\app
```

## Integration

### Orca

This repository is also an [Orca](https://github.com/stablyai/orca) plugin (`orca-plugin.json` at the root, code in `apps/orca-plugin`). It shows the machine's capacity in Orca's status bar, such as `loadslot 2/4 · 1 waiting · 3.1 GB free`: yellow while jobs wait, red when memory is nearly gone. Pressing it opens the right sidebar, where the **loadslot** panel shows the machine (slots, free memory, CPUs, daemon) and the running and waiting jobs with their source, memory and age, updated every 5 seconds. Orca has no way yet for a plugin to open its own panel from the status bar, so pick the loadslot tab once; Orca keeps it selected.

- It runs `loadslot status --json` every 5 seconds, so install loadslot first. It looks for `loadslot` on the PATH Orca started with, then in `~/.local/bin`, `~/go/bin`, WinGet's `Links` and Homebrew's `bin`; `LOADSLOT_BIN` names another path.
- Gating still comes from the agents' hooks (`loadslot setup`); the plugin only shows.
- For development: `orca plugin dev load .` from a checkout (Orca asks to approve it; it runs as a trusted worker), `orca plugin logs local.loadslot` for its log.

### Claude Code

`loadslot hook claude` is a `PreToolUse` hook that rewrites heavy Bash commands to run through loadslot. Register it for every repository with:

```sh
loadslot setup            # add or update the hook in Claude Code's user settings
loadslot setup --dry-run  # show what would change
loadslot setup --remove   # remove it
```

`setup` edits only loadslot's own entry in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`; `--settings` picks another file). Other keys keep their order and other tools' hooks stay as they are; the entry goes after existing `PreToolUse` groups. It points the hook at the running `loadslot`: at the path on the `PATH` when that is the same file (a package manager's link such as Homebrew's `bin` or WinGet's `Links`, which survives upgrades), otherwise at the executable itself. Run it again after moving the binary; `loadslot doctor` tells when that is needed. The result is equivalent to:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "loadslot hook claude" }]
      }
    ]
  }
}
```

- The line is parsed as Bash. It is heavy when any of its commands, including those in `$(...)`, subshells and `sh -c` / `bash -c` scripts, is a package script `check`, `test`, `build`, `lint` or `typecheck` (also `test:*` and `build:*`), `vitest`, `jest`, `tsc`, `playwright`, `go test|build|vet` or `cargo test|build|check|clippy`. A package script call is heavy when its script runs a heavy command, whatever it is named: `pnpm tc:web` counts when `tc:web` is `tsc --noEmit …`, read from the `package.json` nearest to the call's directory, following scripts that call scripts up to 3 deep (calls across workspace packages with `-r` or `--filter` are matched by name only). Text in quotes, heredocs and comments never counts. `--match <regex>` replaces this list. A line that doesn't parse is left alone.
- Only the heavy commands are rewritten, in place: `cd apps/site && pnpm test 2>&1 | tail -20` becomes `cd apps/site && '<path to loadslot>' run --weight 1 --priority background --source claude --as 'pnpm test' -- pnpm test 2>&1 | tail -20`. The rest of the line runs in the agent's own shell as written, so a slot is held only while the heavy command runs (not through a following `gh pr checks --watch`), and a `cd` carries over to the agent's next command. Assignments before a command (`CI=1 pnpm test`) reach it through loadslot. A `sh -c` / `bash -c` call whose script has heavy commands is gated as a whole. Each gated command learns as itself (e.g. `pnpm test`). The absolute path works without loadslot on the `PATH` and skips shims, which would add hundreds of milliseconds to every call. `--weight` sets the weight hint.
- Light commands (`git`, `pnpm install`, formatters, prose linters) are left alone, so they never wait. `git push` is left alone too: gate the `pre-push` hook instead (see below).
- On invalid input, the hook exits 1, which Claude Code reports without blocking the command. With `LOADSLOT_DISABLE=1` in its environment or in the command line, it does nothing.
- Permission rules see the rewritten command, so an allow rule such as `Bash(pnpm test:*)` no longer matches it.

Waiting for a slot doesn't eat into the command's own timeout. The hook raises the Bash tool's timeout to its maximum (10 minutes), keeps the command's timeout (as asked, or the 2-minute default, at most 9 minutes) as `--timeout`, and gives the rest to `--max-wait`, split evenly when a line has several heavy commands. When no slot frees up in time, the call fails with exit `75` and a message, so the agent can retry. Background calls (`run_in_background`) wait without a limit.

### git pre-push

`.githooks/pre-push` (or `.git/hooks/pre-push`):

```sh
#!/bin/sh
exec loadslot run --weight 2 -- pnpm check
```

The shared `pre-push` of [`@ryusenkei/conventions`](https://www.npmjs.com/package/@ryusenkei/conventions) does this when loadslot (or `jobgate` in versions before the rename) is on the `PATH`.

### Package scripts

```json
{
  "scripts": {
    "check": "loadslot run --weight 2 -- pnpm run check:all",
    "test": "loadslot run -- vitest run"
  }
}
```

For contributors without loadslot, keep the unwrapped script under another name, or set `LOADSLOT_DISABLE=1` where loadslot is installed but not wanted (for example in CI).

## Configuration

Optional `config.json` in the data directory. The daemon rereads it every second.

```json
{
  "slots": 0,
  "costMB": 2048,
  "reserveMB": 1024,
  "memoryFraction": 0.6,
  "jobCeilingFraction": 0.75,
  "minSamples": 3
}
```

| Variable | Effect |
| --- | --- |
| `LOADSLOT_SLOTS` | Fixed number of slots instead of the computed one |
| `LOADSLOT_COST_MB` | Assumed peak memory of a weight-1 job without samples |
| `LOADSLOT_RESERVE_MB` | Memory kept free for the editor, browsers and agents |
| `LOADSLOT_PRIORITY` | Default priority: `interactive` or `background` |
| `LOADSLOT_DISABLE=1` | Run commands directly, without the gate |
| `LOADSLOT_NO_DAEMON=1` | Skip the daemon and use the lock fallback |
| `LOADSLOT_SOURCE` | Default `--source` of `loadslot run` |
| `LOADSLOT_HOME` | Data directory. It also isolates the daemon, so separate homes don't share slots |

Environment variables take precedence over `config.json`. The daemon reads them when it starts, so set them where the daemon is started, or use `config.json`.

| OS | Data directory |
| --- | --- |
| Linux | `$XDG_DATA_HOME/loadslot` (`~/.local/share/loadslot`); socket and locks in `$XDG_RUNTIME_DIR/loadslot` when set |
| macOS | `~/Library/Application Support/loadslot` |
| Windows | `%LOCALAPPDATA%\loadslot` |

The data directory holds `config.json`, `samples.json` (the last 10 peaks per command and directory) and `daemon.log`.

## OS support

| Port | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Memory (total, free) | `/proc/meminfo` `MemAvailable`, limited by cgroup v2 `memory.max` | `sysctl` `hw.memsize` and page counters (free, speculative, purgeable, file-backed), `vm_stat` as a fallback | `GlobalMemoryStatusEx` |
| CPUs | `runtime.NumCPU`, limited by cgroup v2 `cpu.max` | `runtime.NumCPU` | `runtime.NumCPU` |
| Peak and current memory of the process tree | Sum of resident memory over `/proc`, polled every 250 ms, plus `ru_maxrss` | Sum of resident memory from `ps`, polled every 250 ms, plus `ru_maxrss` | Job Object peak and current committed memory |
| Kill the process tree, clean up leftovers | Signals to descendants found in `/proc` | Signals to descendants found with `ps` | `TerminateJobObject` |
| Lower priority for background jobs | `setpriority` (nice 10) on the root; descendants inherit it | `setpriority` (nice 10) on the root; descendants inherit it | Job Object below-normal priority class |
| IPC | Unix socket (mode 0600) | Unix socket (mode 0600) | Named pipe per daemon, current user only (its name is in `daemon.pipe`, so a new daemon can start while a retired one finishes its jobs) |
| Machine-wide lock | `flock` | `flock` | Named mutex (per logon session) |

Limitations:

- The memory ceiling is checked every 0.5 seconds, so a tree that grows very fast can pass it by a little before it is stopped. OS-enforced limits (cgroup `memory.max`, Job Object limits) are not used: a failed allocation would end the program with its own error, which an agent can't tell from a test failure.
- Polling on Linux and macOS can miss peaks shorter than 250 ms in descendants.
- On Windows, a child started in the first instant before the process joins the Job Object can escape tracking.
- Other Unix systems build, but report no memory and track only the root process.

## Structure

| Path | Purpose | Depends on |
| --- | --- | --- |
| `cmd/loadslot` | CLI: `run`, `status`, `daemon`, `hook`, `setup`, `doctor` | `internal/*` |
| `internal/gate` | Scheduler core, OS-independent and pure: slots, admission, priority, learning estimates | — |
| `internal/platform` | OS ports: metrics, process tracking, IPC, locks, paths. One file per OS, selected by build tags | — |
| `internal/store` | Learning samples in `samples.json` | — |
| `internal/jobkey` | Learning keys: repository (across worktrees), path inside it, label | — |
| `internal/config` | `config.json` and environment overrides | `internal/gate` |
| `internal/daemon` | Daemon and client: admission over IPC, started on demand | `internal/gate`, `internal/platform`, `internal/store` |
| `internal/hook` | Agent hooks: rewrite heavy commands to `loadslot run`, text only | — |
| `internal/setup` | Register agent hooks in the agents' settings files, keeping other entries and key order; find installed agents and stable paths | — |
| `internal/runner` | `loadslot run`: acquire (daemon or lock fallback), run, release, record | all of the above |
| `apps/orca-plugin` | Orca plugin: status bar item and details panel (manifest: `orca-plugin.json` at the root) | the `loadslot` CLI |
| `scripts/` | Repository operations, such as managing `.env` | — |

The Go module stays at the repository root, so `go install github.com/ryusenkei-inc/loadslot/cmd/loadslot@latest` and `v*` tags work as for any Go tool. TypeScript packages live in `apps/` and have no runtime dependencies on each other or on the Go code; they call the `loadslot` CLI.

Dev tooling: [`@ryusenkei/conventions`](https://www.npmjs.com/package/@ryusenkei/conventions) (shared rules, Claude Code hooks and shared files such as `.editorconfig`, `.gitignore` and `.github/dependabot.yaml`; don't edit them by hand) and [`@ryusenkei/prose-lint`](https://www.npmjs.com/package/@ryusenkei/prose-lint) (Markdown, comments and commit messages).

## Commands

```sh
go build ./cmd/loadslot         # build the CLI
go test ./...                  # tests
pnpm install                   # dev tooling and the commit-msg hook
pnpm lint                      # typecheck (scripts/, apps/*), prose-lint, shared-file check, go vet
pnpm test                      # go test ./... and Vitest
pnpm check                     # lint and test (run by the pre-push hook, through loadslot when installed)
npx ryusenkei-conventions sync # write the shared files from @ryusenkei/conventions
node scripts/env.ts run <cmd>  # run a command with .env loaded
pnpm env:push [--dry-run]      # push .env values to GitHub Variables / Secrets
pnpm repo:settings             # apply GitHub repository settings (squash merge only)
```

Releases: pushing a `v*` tag runs `.github/workflows/release.yaml`, which tests, checks that `orca-plugin.json` has the same version, and publishes binaries with GoReleaser (`.goreleaser.yaml`). The same tag is the Orca plugin's version. `loadslot version` reports the tag through Go's VCS stamping.

CI (`.github/workflows/ci.yaml`) runs `go vet` and `go test` on Linux, macOS and Windows, cross-compiles for all three, and runs the TypeScript typecheck and Vitest, prose, PR title and secret checks.

## License

[MIT](LICENSE)
