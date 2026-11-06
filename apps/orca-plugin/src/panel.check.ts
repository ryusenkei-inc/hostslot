// Scenarios for `orca plugin check` and `orca plugin shot --scenario`: the
// panel in each state it has, from status data through the real view.
import { defineCheck } from 'orca/testing';
import type { Finished, Job, Problem, Status } from './status.ts';
import { type Setup, view } from './view.ts';

const now = Date.now();
const ago = (s: number) => new Date(now - s * 1000).toISOString();

const job = (id: number, repo: string, name: string, over: Partial<Job> = {}): Job => ({
  id,
  command: name,
  key: `git:/dev/${repo}/.git\n.\n${name}`,
  dir: `/dev/${repo}`,
  source: 'claude',
  estimateMB: 1500,
  currentMB: 900,
  reported: true,
  queued: ago(120),
  started: ago(40),
  expectedMs: 90_000,
  ...over,
});

const done = (repo: string, name: string, outcome: string, s: number, peakMB = 1200): Finished => ({
  command: name,
  key: `git:/dev/${repo}/.git\n.\n${name}`,
  dir: `/dev/${repo}`,
  outcome,
  exitCode: outcome === 'ok' ? 0 : outcome === 'failed' ? 1 : 77,
  peakMB,
  durationMs: 35_000,
  ended: ago(s),
});

const status = (over: Partial<Status> = {}, snap: Partial<Status['snapshot']> = {}): Status => ({
  mode: 'daemon',
  version: 'v0.7.0',
  memory: { totalMB: 16384, freeMB: 6200 },
  cpus: 8,
  room: 4,
  reserveMB: 1024,
  ceilingMB: 12288,
  recent: [done('shop', 'pnpm test', 'ok', 30), done('blog', 'pnpm lint', 'failed', 90, 600)],
  ...over,
  snapshot: { slots: 4, used: 0, heldMB: 0, running: [], waiting: [], ...snap },
});

const show = (s: Status | undefined, problems: Problem[] = [], setup: Partial<Setup> = {}, checks = 5) => {
  const v = view(s, '', problems, checks, now);
  return { requests: { 'view.get': { ...v, setup: { ...v.setup, ...setup } } } };
};

const install = {
  request: 'install' as const,
  label: 'Update Hostslot to 0.8.0',
  note: "Downloads it from GitHub to C:\\Users\\me\\AppData\\Local\\hostslot\\bin\\hostslot.exe, checks it against the release's checksums, then connects your agents.",
};

export default defineCheck({
  // The README's picture: work running and waiting, a guard stop, what just ended.
  readme: show(
    status(
      {
        room: 0,
        memory: { totalMB: 16384, freeMB: 3500 },
        ports: [
          { key: 'git:/dev/shop/.git\n/dev/shop\nshop', port: 5173, name: 'shop', worktree: '/dev/shop', running: true, listening: [5173] },
          { key: 'git:/dev/shop/.git\n/w/fix-cart\nshop', port: 21643, name: 'shop', worktree: '/w/fix-cart', running: true, listening: [21643] },
          { key: 'git:/dev/blog/.git\n/dev/blog\nblog', port: 3000, name: 'blog', worktree: '/dev/blog', running: false },
        ],
        recent: [
          done('api', 'pnpm run check', 'machine', 50, 1700),
          done('shop', 'pnpm test', 'ok', 80),
          done('docs', 'pnpm build', 'ok', 140, 900),
          done('blog', 'pnpm lint', 'failed', 260, 600),
        ],
      },
      {
        used: 3,
        heldMB: 1200,
        running: [job(1, 'shop', 'pnpm test'), job(2, 'blog', 'cargo build', { source: 'codex', started: ago(200) })],
        waiting: [job(4, 'api', 'pnpm check', { reason: 'memory', started: undefined })],
      },
    ),
  ),
  idle: show(status()),
  busy: show(
    status({ room: 0, memory: { totalMB: 16384, freeMB: 3500 } }, {
      used: 3,
      heldMB: 1200,
      running: [job(1, 'shop', 'pnpm test'), job(2, 'blog', 'cargo build', { source: 'codex', started: ago(200) }), job(3, 'shop', 'tsc -b', { reported: false })],
      waiting: [job(4, 'api', 'pnpm check', { reason: 'memory', started: undefined }), job(5, 'blog', 'pnpm test', { reason: 'queue', started: undefined })],
    }),
  ),
  stops: show(
    status({
      recent: [
        done('api', 'pnpm run check', 'machine', 40, 1700),
        done('api', 'pnpm run check', 'machine', 70, 1800),
        done('docs', 'pnpm run check', 'machine', 200, 1300),
        done('shop', 'vitest run', 'ceiling', 300, 12500),
        done('shop', 'pnpm test', 'ok', 400),
      ],
    }),
  ),
  problems: show(
    status(),
    [
      {
        level: 'warn',
        text: '',
        fix: 'setup',
        title: 'An older daemon is running',
        detail: 'v0.4.1 (pid 27416). The next heavy command replaces it.',
      },
      { level: 'warn', text: '', fix: 'setup', title: "Codex isn't connected", detail: 'No Hostslot hook in ~/.codex/hooks.json.' },
    ],
    { action: { request: 'setup', label: 'Fix with hostslot setup', note: 'Connects your agents and replaces an older daemon.' } },
  ),
  outdated: show(
    status(),
    [{ level: 'warn', text: '', title: 'Hostslot is older than this plugin', detail: 'Hostslot v0.7.0; the plugin needs 0.8.0 or later.' }],
    { action: install },
  ),
  missing: show(undefined, [], { action: { ...install, label: 'Install Hostslot 0.8.0' } }, 0),
  installed: show(status(), [], {
    result: {
      ok: true,
      title: 'Installed Hostslot 0.8.0',
      items: [
        { label: 'Location', value: 'C:\\Users\\me\\AppData\\Local\\hostslot\\bin\\hostslot.exe' },
        { label: 'Claude Code', value: 'already set up' },
        { label: 'Codex', value: 'hook added' },
        { label: 'Daemon', value: 'older one retired; the next run starts this version' },
      ],
      next: 'Open Codex and trust the hostslot hook with /hooks. Until then Codex skips it.',
    },
  }),
});
