# jobgate

A machine-wide gate for heavy local jobs. Wrap any command, and jobgate admits it only when the machine has room for it:

```sh
jobgate run --weight 1 -- pnpm check
```

## Why

Running several coding agents in parallel (each in its own worktree, often across repositories) makes every agent start heavy commands at the same time: type checks, test runners, builds. On a 16 GB machine this exhausts memory. Editors and agent IDEs can cap the number of agents, but nothing coordinates the commands those agents run.

jobgate does, across tools and repositories. It works the same from a plain terminal, any editor, Claude Code hooks, git hooks and package scripts.

## How it works

- **Slots:** `floor((total memory x 0.6 - reserve) / cost of a weight-1 job)`, capped by the CPU count and at least 1. With the defaults (1 GB reserve, 2 GB per weight-1 job), a 16 GB machine has 4 slots.
- **Admission:** a job needs free slots for its weight and enough free memory for its estimated peak. Memory of jobs admitted in the last 15 seconds is subtracted, since they haven't grown yet. When nothing runs, the next job is always admitted.
- **Order:** interactive jobs before background jobs, first come first served within each. The head of the queue blocks later jobs, so a heavy job is never starved by lighter ones.
- **Learning:** jobgate records the peak memory of each wrapped command, including its child processes, keyed by command line and working directory. After 3 runs, the recent peak replaces the weight hint. A command that turns out light takes 1 slot, even with `--weight 3`.
- **Inner parallelism:** the wrapped command gets its share of CPUs in `VITEST_MAX_WORKERS` and `npm_config_workspace_concurrency` (pnpm's `--workspace-concurrency`), unless `CI` is set or the variable is already set. `JOBGATE_WORKERS` is always set for your own scripts.
- **Nesting:** a `jobgate run` inside a gated command runs directly, since the outer job already holds a slot (jobgate sets `JOBGATE_JOB` for the command). Wrapping at several layers, such as a Claude Code hook, a git hook and package scripts, never deadlocks.
- **Daemon:** the first `jobgate run` starts a small daemon in the background. The CLI talks to it over a Unix socket or a named pipe. The daemon exits after 10 minutes without jobs.
- **Lock fallback:** when the daemon can't start, jobgate falls back to one machine-wide lock per slot. Slots and memory checks still apply, but there is no priority or queue order.
- **Crash safety:** a job holds its slot through an open connection (or an OS lock in fallback mode). When jobgate or the job is killed, the OS closes it and the slot comes back.

## Install

```sh
go install github.com/ryusenkei-inc/jobgate/cmd/jobgate@latest
```

Requires Go 1.26 or later. Make sure `$(go env GOPATH)/bin` is on your `PATH`.

## Usage

```sh
jobgate run [--weight N] [--priority interactive|background] -- <command> [args...]
jobgate status [--json]
jobgate daemon [--idle 10m]
jobgate hook claude [--weight N] [--match REGEX]
jobgate version
```

- `run` waits for admission, printing a status line to stderr every 5 seconds, then runs the command with stdin, stdout and stderr passed through. It exits with the command's exit code (`128 + signal` when killed by a signal, `127` when the command isn't found).
- `--weight` is the expected cost in slots. It only matters until jobgate has learned the command.
- `--priority` defaults to `interactive` when stdin is a terminal and `background` otherwise.
- Ctrl+C reaches the command as usual, and jobgate waits for it to exit and releases the slot. A second Ctrl+C kills the whole process tree.
- `status` shows the slots, free memory, running jobs (command, weight, estimated memory) and waiting jobs. Without a daemon, it shows how many slot locks are held.
- `daemon` runs the daemon in the foreground. You don't need to start it yourself.
- `hook claude` is a Claude Code hook (see [Claude Code](#claude-code)).

Example:

```text
$ jobgate status
mode:    daemon pid 17048
slots:   1/1 used
memory:  4396 MB free of 16325 MB
cpus:    8
tracker: job object (peak committed memory)

running (1)
  ID  WEIGHT  EST MB  SAMPLES  PRIORITY     AGE  COMMAND  DIR
  4   1       2048    0        interactive  2s   sleep 7  C:\src\app

waiting (1)
  ID  WEIGHT  EST MB  SAMPLES  PRIORITY    AGE  COMMAND  DIR
  5   1       2048    0        background  1s   sleep 1  C:\src\app
```

## Integration

### Claude Code

`jobgate hook claude` is a `PreToolUse` hook that rewrites heavy Bash commands to run through jobgate. Register it in `~/.claude/settings.json` to cover every repository:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "jobgate hook claude" }]
      }
    ]
  }
}
```

- A command line is heavy when any of its commands (split at `&&`, `||`, `;`, `|`) is a package script `check`, `test`, `build`, `lint` or `typecheck` (also `test:*` and `build:*`), `vitest`, `jest`, `tsc`, `playwright`, `go test|build|vet` or `cargo test|build|check|clippy`. `--match <regex>` replaces this list.
- The whole line becomes `jobgate run --weight 1 --priority background -- sh -c '<line>'`. `--weight` sets the weight hint. A `cd` inside a rewritten line doesn't carry over to the next command.
- Light commands (`git`, `pnpm install`, formatters, prose linters) are left alone, so they never wait. `git push` is left alone too: gate the `pre-push` hook instead (see below).
- On invalid input, the hook exits 1, which Claude Code reports without blocking the command. With `JOBGATE_DISABLE=1`, it does nothing.
- Permission rules see the rewritten command, so an allow rule such as `Bash(pnpm test:*)` no longer matches it.

Time spent waiting for a slot counts toward the Bash tool's timeout.

### git pre-push

`.githooks/pre-push` (or `.git/hooks/pre-push`):

```sh
#!/bin/sh
exec jobgate run --weight 2 -- pnpm check
```

The shared `pre-push` of [`@ryusenkei/conventions`](https://www.npmjs.com/package/@ryusenkei/conventions) 0.3.1 or later does this when jobgate is on the `PATH`.

### Package scripts

```json
{
  "scripts": {
    "check": "jobgate run --weight 2 -- pnpm run check:all",
    "test": "jobgate run -- vitest run"
  }
}
```

For contributors without jobgate, keep the unwrapped script under another name, or set `JOBGATE_DISABLE=1` where jobgate is installed but not wanted (for example in CI).

## Configuration

Optional `config.json` in the data directory. The daemon rereads it every second.

```json
{
  "slots": 0,
  "costMB": 2048,
  "reserveMB": 1024,
  "memoryFraction": 0.6,
  "minSamples": 3
}
```

| Variable | Effect |
| --- | --- |
| `JOBGATE_SLOTS` | Fixed number of slots instead of the computed one |
| `JOBGATE_COST_MB` | Assumed peak memory of a weight-1 job without samples |
| `JOBGATE_RESERVE_MB` | Memory kept free for the editor, browsers and agents |
| `JOBGATE_PRIORITY` | Default priority: `interactive` or `background` |
| `JOBGATE_DISABLE=1` | Run commands directly, without the gate |
| `JOBGATE_NO_DAEMON=1` | Skip the daemon and use the lock fallback |
| `JOBGATE_HOME` | Data directory. It also isolates the daemon, so separate homes don't share slots |

Environment variables take precedence over `config.json`. The daemon reads them when it starts, so set them where the daemon is started, or use `config.json`.

| OS | Data directory |
| --- | --- |
| Linux | `$XDG_DATA_HOME/jobgate` (`~/.local/share/jobgate`); socket and locks in `$XDG_RUNTIME_DIR/jobgate` when set |
| macOS | `~/Library/Application Support/jobgate` |
| Windows | `%LOCALAPPDATA%\jobgate` |

The data directory holds `config.json`, `samples.json` (the last 10 peaks per command and directory) and `daemon.log`.

## OS support

| Port | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Memory (total, free) | `/proc/meminfo` `MemAvailable`, limited by cgroup v2 `memory.max` | `sysctl` `hw.memsize` and page counters (free, speculative, purgeable, file-backed), `vm_stat` as a fallback | `GlobalMemoryStatusEx` |
| CPUs | `runtime.NumCPU`, limited by cgroup v2 `cpu.max` | `runtime.NumCPU` | `runtime.NumCPU` |
| Peak memory of the process tree | Sum of resident memory over `/proc`, polled every 250 ms, plus `ru_maxrss` | Sum of resident memory from `ps`, polled every 250 ms, plus `ru_maxrss` | Job Object peak committed memory |
| Kill the process tree | Signals to descendants found in `/proc` | Signals to descendants found with `ps` | `TerminateJobObject` |
| IPC | Unix socket (mode 0600) | Unix socket (mode 0600) | Named pipe, current user only |
| Machine-wide lock | `flock` | `flock` | Named mutex (per logon session) |

Limitations:

- Hard memory limits (cgroup `memory.max`, Job Object limits) are not enforced yet. jobgate only watches.
- Polling on Linux and macOS can miss peaks shorter than 250 ms in descendants.
- On Windows, a child started in the first instant before the process joins the Job Object can escape tracking.
- Other Unix systems build, but report no memory and track only the root process.

## Structure

| Path | Purpose | Depends on |
| --- | --- | --- |
| `cmd/jobgate` | CLI: `run`, `status`, `daemon`, `hook` | `internal/*` |
| `internal/gate` | Scheduler core, OS-independent and pure: slots, admission, priority, learning estimates | — |
| `internal/platform` | OS ports: metrics, process tracking, IPC, locks, paths. One file per OS, selected by build tags | — |
| `internal/store` | Learning samples in `samples.json` | — |
| `internal/config` | `config.json` and environment overrides | `internal/gate` |
| `internal/daemon` | Daemon and client: admission over IPC, started on demand | `internal/gate`, `internal/platform`, `internal/store` |
| `internal/hook` | Agent hooks: rewrite heavy commands to `jobgate run`, text only | — |
| `internal/runner` | `jobgate run`: acquire (daemon or lock fallback), run, release, record | all of the above |
| `scripts/` | Repository operations, such as managing `.env` | — |

Node.js is only used for dev tooling: [`@ryusenkei/conventions`](https://www.npmjs.com/package/@ryusenkei/conventions) (shared rules, Claude Code hooks and shared files such as `.editorconfig`, `.gitignore` and `.github/dependabot.yaml`; don't edit them by hand) and [`@ryusenkei/prose-lint`](https://www.npmjs.com/package/@ryusenkei/prose-lint) (Markdown, comments and commit messages).

## Commands

```sh
go build ./cmd/jobgate         # build the CLI
go test ./...                  # tests
pnpm install                   # dev tooling and the commit-msg hook
pnpm lint                      # typecheck of scripts/, prose-lint, shared-file check, go vet
pnpm test                      # go test ./...
npx ryusenkei-conventions sync # write the shared files from @ryusenkei/conventions
node scripts/env.ts run <cmd>  # run a command with .env loaded
pnpm env:push [--dry-run]      # push .env values to GitHub Variables / Secrets
pnpm repo:settings             # apply GitHub repository settings (squash merge only)
```

CI (`.github/workflows/ci.yaml`) runs `go vet` and `go test` on Linux, macOS and Windows, cross-compiles for all three, and checks prose, the PR title and secrets.

## License

[MIT](LICENSE)
