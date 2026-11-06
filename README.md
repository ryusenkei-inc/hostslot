# loadslot

Run more coding agents in parallel without freezing your machine.

loadslot gates heavy commands (type checks, test runners, builds) machine-wide: each starts when there is room in memory and CPU, the rest wait their turn, and a runaway is stopped before it takes the machine down.

```sh
loadslot run -- pnpm test     # waits for a slot, then runs pnpm test
```

## Quick start

1. Install: download the archive for your OS from [Releases](https://github.com/ryusenkei-inc/loadslot/releases) and put `loadslot` on your `PATH`, or `go install github.com/ryusenkei-inc/loadslot/cmd/loadslot@latest` (Go 1.26+).
2. Connect your agents: `loadslot setup` registers the Claude Code hook.
3. Check: `loadslot doctor`.

To upgrade, replace the binary (on Windows, rename the running `loadslot.exe` first); the next run retires the old daemon.

From then on, heavy commands your agents run go through loadslot. `loadslot status` shows what runs and waits; in [Orca](#orca), the status bar and a panel show it.

In Orca, the plugin does steps 1 and 2 for you: its panel offers to install loadslot and run its setup.

## Why

Several agents, each in its own worktree, start `tsc`, test suites and builds at the same time. On a 16 GB machine that exhausts memory: everything freezes and the OOM killer takes agents with it. Agent IDEs can cap the number of agents, but nothing coordinates the commands they run.

### Why at the process level

| Reason | |
| --- | --- |
| Tool limits don't add up | `--maxWorkers` or an IDE's agent limit each bound one program; four agents that respect theirs still start four type checks together |
| The cost is in commands | Agents cost little while they think. Gating their heavy commands, not the agents, keeps many agents working and lets only the heavy parts take turns |
| Only the OS sees it all | The process tree shows a command's real memory, children included, in any language; the OS can also act on it (priority, Job Objects, signals) |
| It works with anything | A command line in, the same command out: one gate for agents, people, git hooks and scripts |

The OS's own tools don't queue: cgroups and Job Object limits make a capped build fail instead of wait, early-OOM killers act after memory is gone, and the scheduler has no notion of jobs worth waiting for. loadslot uses those mechanisms and adds a queue, priorities and learned costs.

### loadslot and Orca

[Orca](https://github.com/stablyai/orca) shows and paces agents; loadslot schedules the heavy work the agents start, across the whole machine, and guards memory while it runs. As of Orca 1.4.25x:

| Need | Orca | loadslot |
| --- | --- | --- |
| See memory use | Resource Manager: per-session CPU and memory, kill a session by hand | Per-job memory, held memory, why jobs wait, recent outcomes |
| Limit agents | Orchestration runs 4 tasks at a time by default; restores pause when the process count grows fast | Advisory room for more work; agents are not gated |
| Keep heavy commands from piling up | Not covered | Admits each when slots and memory allow; the rest queue by priority |
| Know a command's cost | Not covered | Learns peak memory and duration per repository |
| Share CPU fairly | Not covered | Lower OS priority and inner parallelism per job |
| Stop a runaway | Not covered (on Linux, one OOM kill no longer takes every terminal) | Per-job ceiling, out-of-memory stop, cleanup of leftovers |
| Scope | Orca's terminals | The whole machine: any editor or terminal, agent hooks, git hooks, scripts |

## How it works

| | |
| --- | --- |
| **Slots** | `floor((total memory x 0.6 - reserve) / 2 GB)`, at most the CPU count: 4 on a 16 GB machine |
| **Admission** | A job needs free slots for its weight and free memory for its estimate. Running jobs that are still growing hold back the rest of their estimate. When nothing runs, the next job always starts |
| **Order** | Interactive jobs (a person at a terminal) before background jobs (agents, scripts), first come first served; the head of the queue blocks later jobs, so a heavy job is never starved |
| **Learning** | Each command's peak memory and duration, per repository (see [What it learns](#what-it-learns)) |
| **CPU share** | Background jobs run at a lower OS priority. Each job gets its share of CPUs in `VITEST_MAX_WORKERS`, `npm_config_workspace_concurrency`, `pnpm_config_workspace_concurrency` and `LOADSLOT_WORKERS` (not under `CI`, never overriding a set value) |
| **Nesting** | A `loadslot run` inside a gated job runs directly (`LOADSLOT_JOB` is set), so hooks, git hooks and scripts can wrap each other without deadlock |
| **Daemon** | Started by the first `loadslot run`, over a Unix socket or named pipe; exits after 10 idle minutes. A newer version retires an older daemon, which finishes its jobs first. Without a daemon, one machine-wide lock per slot (no queue order) |
| **Crash safety** | A job holds its slot through an open connection or OS lock; if it dies, the slot comes back |

### Guards

| Guard | What happens | Exit code |
| --- | --- | --- |
| Memory ceiling | A job whose process tree passes `jobCeilingFraction` of total memory (default 75%) is stopped | `77` |
| Out of memory | When free memory falls below half of `reserveMB`, the largest background job is stopped (at most one per 10 s; never interactive jobs) | `77` |
| Cleanup | Processes a job leaves running (test workers, browsers) are stopped when it ends; `--keep-children` keeps them | — |
| Wait limit | `--max-wait` gives up on waiting; the command never runs without a slot | `75` |
| Timeout | `--timeout` stops the job after it has run that long | `124` |

## What counts as heavy

You decide with `loadslot run`. The integrations decide like this:

| Integration | Gated |
| --- | --- |
| Agent hooks (Claude Code) | Each heavy command in the line, rewritten in place (below); the rest of the line runs as written |
| git pre-push | The push's `check` script |
| Package scripts | Whatever you wrap |

A command is heavy when it is one of:

- a package script named `check`, `test`, `build`, `lint`, `typecheck`, `test:*` or `build:*` (`pnpm`, `npm`, `yarn`, `bun`, also with `-r` / `--filter`)
- a package script whose body runs a heavy command, whatever its name (`pnpm tc:web` when `tc:web` is `tsc --noEmit …`), from the nearest `package.json`, up to 3 scripts deep
- `vitest`, `jest`, `tsc`, `playwright` (also through `npx`, `pnpm exec`, `bunx`)
- `go test|build|vet`, `cargo test|build|check|clippy`

The line is parsed as shell, so text in quotes, heredocs and comments never counts. Installs, git, formatters and prose linters stay ungated and never wait. `loadslot hook claude --match <regex>` replaces the list.

## What it learns

Each finished job adds a sample to `samples.json` in the data directory. Nothing leaves the machine.

| | |
| --- | --- |
| **Key** | Repository (its common git directory, shared by worktrees) + path inside it + the command (`pnpm test`, or `--as`) |
| **Sample** | Peak memory of the whole process tree (committed on Windows, resident on Linux and macOS), duration, exit code, outcome; the last 10 per key |
| **Estimate** | Under 3 samples: similar work (the same command elsewhere, else the same tool and subcommand, else the same tool), else the weight hint. From 3: the 90th percentile of its own peaks |
| **Used for** | Admission, memory held for growing jobs, weight in slots, time-left estimates in Orca |
| **Not learned** | Runs stopped by `--timeout`. Runs stopped for memory are kept: they need at least that much |

## Integrations

### Orca

This repository is also an Orca plugin (`orca-plugin.json` at the root, code in `apps/orca-plugin`).

- **Status bar:** `loadslot 2/4 · 1 waiting · 3.1 GB free`; yellow while jobs wait or the setup has a problem, red when memory is nearly gone
- **Panel** (press the item to open the right sidebar, then pick the loadslot tab once): memory and slots; running jobs with repository, worktree, agent and time left; waiting jobs with why and when they start; recent outcomes, with guard stops called out; jobs by repository; setup checks
- **Install from the panel:** when loadslot is missing or older than the plugin, the panel offers to install the plugin's version. On the press, it downloads the executable from the GitHub release, checks it against the release's `checksums.txt`, puts it in `bin` in loadslot's data directory and runs `loadslot setup`. When a hook is missing, it offers `loadslot setup` alone. Nothing is downloaded or written before a press
- It reads `loadslot status --json` every 5 seconds. It finds `loadslot` at `LOADSLOT_BIN`, in its own `bin`, on Orca's `PATH`, in `~/.local/bin`, `~/go/bin`, WinGet's `Links` or Homebrew's `bin`
- It only shows; gating comes from the agents' hooks
- Development: `orca plugin dev load .`, then `orca plugin logs local.loadslot`

### Claude Code

```sh
loadslot setup            # add or update the PreToolUse hook in ~/.claude/settings.json
loadslot setup --dry-run  # show what would change
loadslot setup --remove   # remove it
```

`setup` touches only its own entry (other keys keep their order, other tools' hooks stay), and points the hook at a path that survives upgrades when a package manager links one onto the `PATH`.

The hook rewrites only the heavy commands, in place:

```sh
cd apps/site && pnpm test 2>&1 | tail -20
# becomes (abridged; the real prefix also carries --weight, --max-wait and --timeout)
cd apps/site && '<loadslot>' run --priority background --source claude --as 'pnpm test' -- pnpm test 2>&1 | tail -20
```

- A slot is held only while `pnpm test` runs, and `cd` carries over to the agent's next command
- Waiting doesn't eat into the command's timeout: the hook raises the Bash tool's timeout to 10 minutes, keeps the command's own as `--timeout` and gives the rest to `--max-wait`. Out of time, the call exits `75` so the agent can retry
- Permission rules see the rewritten command, so an allow rule such as `Bash(pnpm test:*)` no longer matches it
- `LOADSLOT_DISABLE=1` in the environment or the command line turns it off

### git pre-push

```sh
#!/bin/sh
exec loadslot run --weight 2 -- pnpm check
```

The shared `pre-push` of [`@ryusenkei/conventions`](https://www.npmjs.com/package/@ryusenkei/conventions) does this when loadslot is on the `PATH`.

### Package scripts

```json
{ "scripts": { "test": "loadslot run -- vitest run" } }
```

Where loadslot is installed but not wanted (CI), set `LOADSLOT_DISABLE=1`.

## Reference

### Commands

| Command | |
| --- | --- |
| `loadslot run [flags] -- <command>` | Wait for admission, then run the command with stdin, stdout and stderr passed through; prints a status line to stderr every 5 s while waiting |
| `loadslot status [--json]` | Slots, memory, room for more, running, waiting and recent jobs. `--json` adds held memory, wait reasons and typical durations |
| `loadslot setup [claude] [--remove] [--dry-run] [--settings PATH]` | Register the hooks of the agents it finds |
| `loadslot doctor [--json]` | Check the binary, the `PATH`, the daemon and the hooks, with how to fix each problem; exits 1 on warnings |
| `loadslot hook claude [--weight N] [--match REGEX]` | The Claude Code hook (registered by `setup`) |
| `loadslot daemon [--idle 10m]` | Run the daemon in the foreground (not needed normally) |
| `loadslot version` | Print the version |

`run` flags:

| Flag | |
| --- | --- |
| `--weight N` | Expected cost in slots until the command is learned (default 1) |
| `--priority interactive\|background` | Default: interactive when stdin is a terminal |
| `--as LABEL` | Learn as this name instead of the command line |
| `--source NAME` | Who started it, shown in status (default `$LOADSLOT_SOURCE`) |
| `--max-wait 5m` | Give up waiting after 5 minutes |
| `--timeout 2m` | Stop the job after it has run 2 minutes |
| `--max-memory MB` | Memory ceiling for this run |
| `--keep-children` | Leave processes the command started running |

Exit codes: the command's own, or `128 + signal` (killed), `127` (command not found), `75` (no slot in time), `124` (timeout), `77` (over its memory ceiling, or stopped because memory ran out). Ctrl+C reaches the command; a second one kills its process tree.

### Configuration

Optional `config.json` in the data directory, reread every second:

```json
{ "slots": 0, "costMB": 2048, "reserveMB": 1024, "memoryFraction": 0.6, "jobCeilingFraction": 0.75, "minSamples": 3 }
```

| Variable | Effect |
| --- | --- |
| `LOADSLOT_SLOTS`, `LOADSLOT_COST_MB`, `LOADSLOT_RESERVE_MB` | Override `slots`, `costMB`, `reserveMB` (read when the daemon starts) |
| `LOADSLOT_PRIORITY` | Default priority |
| `LOADSLOT_DISABLE=1` | Run commands without the gate |
| `LOADSLOT_NO_DAEMON=1` | Use the lock fallback |
| `LOADSLOT_SOURCE` | Default `--source` |
| `LOADSLOT_HOME` | Data directory; separate homes don't share slots |

Data directory: `~/.local/share/loadslot` (Linux, or `$XDG_DATA_HOME`), `~/Library/Application Support/loadslot` (macOS), `%LOCALAPPDATA%\loadslot` (Windows). It holds `config.json`, `samples.json` and `daemon.log`.

### OS support

| | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Free memory | `/proc/meminfo`, cgroup v2 limits | `sysctl` and page counters | `GlobalMemoryStatusEx` |
| Job memory | Resident memory over `/proc`, every 250 ms | Resident memory from `ps`, every 250 ms | Job Object committed memory |
| Stop a tree, clean up | Signals to descendants | Signals to descendants | Job Object |
| Lower priority | `nice 10` | `nice 10` | Below-normal priority class |
| IPC | Unix socket (0600) | Unix socket (0600) | Named pipe per daemon, current user only |

### Limitations

- loadslot sees only commands that go through it (hooks, scripts, `loadslot run`); it can stop a program but not make it use less memory
- Memory is read every 0.25–2 s, so a very fast spike can pass a check. OS-enforced limits aren't used: a failed allocation ends a program with its own error, which an agent can't tell from a test failure
- One user on one machine: not containers, VMs or other users
- On Windows, a child started in the first instant before its parent joins the Job Object can escape tracking. Unix systems other than Linux and macOS track only the root process

### Upgrading from jobgate

loadslot was called `jobgate` until v0.2.0. `loadslot setup` replaces a hook registered under the old name; the first run carries over `samples.json` and `config.json`; `JOBGATE_*` variables are now `LOADSLOT_*`.

## Development

| Path | Purpose |
| --- | --- |
| `cmd/loadslot` | CLI |
| `internal/gate` | Scheduler core, OS-independent and pure: slots, admission, priority, estimates |
| `internal/platform` | OS ports: memory, process tracking, IPC, locks, paths (one file per OS) |
| `internal/daemon` | Daemon and client over IPC |
| `internal/runner` | `loadslot run`: acquire, run, release, record |
| `internal/store`, `internal/jobkey` | Learned samples and their keys |
| `internal/config` | `config.json` and environment overrides |
| `internal/hook`, `internal/setup` | Agent hooks and their registration |
| `apps/orca-plugin` | Orca plugin (TypeScript, no runtime dependencies) |
| `scripts/` | Repository operations |

```sh
go test ./...        # Go tests
pnpm install         # dev tooling and git hooks
pnpm lint            # typecheck, prose-lint, shared files, go vet
pnpm test            # go test and Vitest
pnpm check           # lint and test (run by pre-push, through loadslot when installed)
```

- CI runs `go vet` and `go test` on Linux, macOS and Windows, cross-compiles, and checks TypeScript, prose, the PR title and secrets
- Releases: pushing a `v*` tag tests, checks that `orca-plugin.json` has the same version (the tag is also the plugin's version) and publishes binaries with GoReleaser
- Dev tooling: [`@ryusenkei/conventions`](https://www.npmjs.com/package/@ryusenkei/conventions) (shared rules and files; don't edit them by hand) and [`@ryusenkei/prose-lint`](https://www.npmjs.com/package/@ryusenkei/prose-lint)

## License

[MIT](LICENSE)
