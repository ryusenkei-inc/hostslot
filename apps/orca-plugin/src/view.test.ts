import { expect, it } from 'vitest';
import type { Status } from './status.ts';
import { view } from './view.ts';

const now = Date.parse('2026-11-06T10:05:00Z');

const job = {
  id: 7,
  command: "sh -c 'pnpm test'",
  key: 'git:/dev/shop/.git\n.\npnpm test',
  dir: '/dev/shop',
  source: 'claude',
  estimateMB: 2048,
  currentMB: 512,
  reported: true,
  queued: '2026-11-06T10:00:00Z',
  started: '2026-11-06T10:04:00Z',
  expectedMs: 90_000,
};

const status = (over: Partial<Status> = {}, snap: Partial<Status['snapshot']> = {}): Status => ({
  mode: 'daemon',
  version: 'v0.5.0',
  memory: { totalMB: 16384, freeMB: 8192 },
  cpus: 8,
  room: 4,
  reserveMB: 1024,
  ceilingMB: 12288,
  ...over,
  snapshot: { slots: 4, used: 0, heldMB: 0, running: [], waiting: [], ...snap },
});

it('says when the machine is idle, each number once', () => {
  const v = view(status(), '', [], 5, now);
  expect(v).toMatchObject({ headline: 'Idle', tone: 'success' });
  expect(v.machine).toEqual({
    memory: { usedPct: 50, label: 'Memory: 8.0 GB free of 16.0 GB', note: '1.0 GB kept for apps', tone: 'neutral' },
    // Room adds nothing while every free slot fits in memory.
    slots: { usedPct: 0, label: 'Slots: 0 of 4 in use' },
  });
  expect(v.setup).toMatchObject({ problems: [], checks: 5 });
  expect(v.setup.facts).toContainEqual({ label: 'Memory ceiling per job', value: '12.0 GB' });
  expect(JSON.parse(JSON.stringify(v))).toEqual(v);
});

it('shows what runs, how long it usually takes, and what waits and why', () => {
  const waitingSlots = { ...job, id: 8, reason: 'slots', started: undefined, dir: '/w/fix-cart' };
  const behind = { ...job, id: 9, reason: 'queue', started: undefined, source: undefined };
  const v = view(status({ room: 0 }, { used: 4, heldMB: 1536, running: [job], waiting: [waitingSlots, behind] }), '', [], 0, now);
  expect(v.headline).toBe('2 waiting for room');
  expect(v.machine?.memory.note).toBe('1.5 GB held for running jobs · 1.0 GB kept for apps');
  expect(v.running[0]).toEqual({
    key: '7',
    title: 'pnpm test',
    detail: 'shop · claude',
    memory: '512 MB of ~2.0 GB',
    since: Date.parse(job.started),
    left: '~30s left',
  });
  expect(v.waiting[0]).toMatchObject({ position: 1, detail: 'shop (fix-cart) · claude', why: 'all slots busy', eta: '~30s' });
  expect(v.waiting[1]).toMatchObject({ position: 2, detail: 'shop · terminal', why: 'behind #1' });
  expect(v.waiting[1]?.eta).toBeUndefined();
});

it('says when a job runs longer than usual, why a job waits for memory, and when memory limits room', () => {
  const slow = { ...job, started: '2026-11-06T10:00:00Z' };
  const v = view(status({ room: 0 }, { used: 1, heldMB: 7000, running: [slow], waiting: [{ ...job, id: 8, reason: 'memory' }] }), '', [], 0, now);
  expect(v.headline).toBe('1 waiting for room');
  expect(v.running[0]?.left).toBe('longer than usual');
  // 8 GB free, but 6.8 GB of it is held for the running job and 1 GB is kept for apps.
  expect(v.waiting[0]?.why).toBe('needs ~2.0 GB, 168 MB available');
  expect(v.machine?.slots.label).toBe('Slots: 1 of 4 in use · room for 0 more');
  const busy = view(status({ room: 3 }, { used: 1, running: [job] }), '', [], 0, now);
  expect(busy.headline).toBe('Running 1 heavy command');
});

it('sums up guard stops; each stopped job stays a row of Recent', () => {
  const recent = [
    { command: 'x', dir: '/dev/shop', key: 'git:/dev/shop/.git\n.\nvitest run', outcome: 'ceiling', exitCode: 77, peakMB: 12500, durationMs: 61_000, ended: '2026-11-06T10:04:00Z' },
    { command: 'y', dir: '/dev/blog', key: 'git:/dev/blog/.git\n.\npnpm lint', outcome: 'failed', exitCode: 1, peakMB: 900, durationMs: 12_000, ended: '2026-11-06T10:03:00Z' },
    { command: 'z', dir: '/dev/blog', key: 'git:/dev/blog/.git\n.\npnpm test', outcome: 'gave up', exitCode: 0, ended: '2026-11-06T10:02:00Z' },
    { command: 'w', dir: '/dev/shop', key: 'git:/dev/shop/.git\n.\npnpm check', outcome: 'machine', exitCode: 77, peakMB: 1400, ended: '2026-11-06T10:01:00Z' },
    { command: 'w', dir: '/dev/shop', key: 'git:/dev/shop/.git\n.\npnpm check', outcome: 'machine', exitCode: 77, peakMB: 1300, ended: '2026-11-06T10:00:30Z' },
    { command: 'v', dir: '/dev/blog', key: 'git:/dev/blog/.git\n.\npnpm check', outcome: 'machine', exitCode: 77, peakMB: 900, ended: '2026-11-06T10:00:00Z' },
  ];
  const v = view(status({ recent }), '', [], 0, now);
  expect(v.recent.map((r) => [r.title, r.where, r.outcome, r.tone, r.peak, r.took])).toEqual([
    ['vitest run', 'shop', 'over ceiling', 'danger', '12.2 GB', '1m'],
    ['pnpm lint', 'blog', 'failed (1)', 'warning', '900 MB', '12s'],
    ['pnpm test', 'blog', 'gave up', 'neutral', '–', '–'],
    ['pnpm check', 'shop', 'out of memory', 'danger', '1.4 GB', '–'],
    ['pnpm check', 'shop', 'out of memory', 'danger', '1.3 GB', '–'],
    ['pnpm check', 'blog', 'out of memory', 'danger', '900 MB', '–'],
  ]);
  expect(v.recent[1]?.outcomeDetail).toBe('Exited with an error (exit 1)');
  expect(v.stops).toEqual([
    {
      key: 'machine',
      title: 'Machine nearly out of memory',
      summary: '3 jobs · shop ×2, blog',
      last: Date.parse('2026-11-06T10:01:00Z'),
      explanation: 'loadslot stopped the largest job running in the background. Run it again when there is room.',
    },
    {
      key: 'ceiling',
      title: 'Over the memory ceiling per job',
      summary: '1 job · shop',
      last: Date.parse('2026-11-06T10:04:00Z'),
      explanation: 'Each job may use up to 12.0 GB; these used more.',
    },
  ]);
});

it('groups jobs by repository', () => {
  const blog = { ...job, id: 9, key: 'git:/dev/blog/.git\napps/web\npnpm build', dir: '/dev/blog/apps/web', currentMB: 1024 };
  const v = view(status({}, { used: 2, running: [job, blog], waiting: [{ ...job, id: 10, reported: false }] }), '', [], 0, now);
  expect(v.byRepo).toEqual([
    { key: 'shop', repo: 'shop', summary: '2 jobs, 512 MB now' },
    { key: 'blog', repo: 'blog', summary: '1 job, 1.0 GB now' },
  ]);
});

it('lists setup problems briefly, and says when memory is nearly gone', () => {
  const problems = [
    { level: 'warn' as const, text: 'daemon pid 1 v0.4.1 …: older than this loadslot', fix: 'setup' as const, title: 'An older daemon is running', detail: 'v0.4.1 (pid 1). The next heavy command replaces it.' },
    { level: 'error' as const, text: 'codex: cannot read hooks.json' },
  ];
  const v = view(status(), '', problems, 4, now);
  expect(v).toMatchObject({ tone: 'warning', headline: 'Setup needs attention' });
  expect(v.setup.problems).toEqual([
    { key: '0', tone: 'warning', title: 'An older daemon is running', detail: 'v0.4.1 (pid 1). The next heavy command replaces it.' },
    { key: '1', tone: 'danger', title: 'codex: cannot read hooks.json' },
  ]);
  const low = view(status({ memory: { totalMB: 16384, freeMB: 1000 } }, { used: 1, running: [job] }), '', [], 0, now);
  expect(low).toMatchObject({ headline: 'Memory nearly gone', tone: 'danger' });
  expect(low.machine?.memory.tone).toBe('danger');
});

it('explains a missing loadslot', () => {
  const v = view(undefined, '');
  expect(v).toMatchObject({ headline: "loadslot isn't installed", tone: 'warning' });
  expect(v.machine).toBeUndefined();
  expect(v.setup.problems).toEqual([{ key: 'missing', tone: 'warning', title: 'loadslot not found', detail: 'Not on the PATH or in the usual places.' }]);
});
