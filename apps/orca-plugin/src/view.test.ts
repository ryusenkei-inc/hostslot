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

it('says when the machine is idle, with memory and slots as bars', () => {
  const v = view(status(), '', [], 5, now);
  expect(v).toMatchObject({ headline: 'Idle: heavy commands start at once', tone: 'success', checks: 5 });
  expect(v.memory).toEqual({ usedPct: 50, label: 'Memory 50% used of 16.0 GB', breakdown: '8.0 GB free · 1.0 GB kept for apps', tone: 'neutral' });
  expect(v.slots).toEqual({ usedPct: 0, label: '0 of 4 slots · room for 4 more' });
  expect(v.facts).toContainEqual({ label: 'Ceiling per job', value: '12.0 GB' });
  expect(JSON.parse(JSON.stringify(v))).toEqual(v);
});

it('shows what runs, how long it usually takes, and what waits and why', () => {
  const waitingSlots = { ...job, id: 8, reason: 'slots', started: undefined, dir: '/w/fix-cart' };
  const behind = { ...job, id: 9, reason: 'queue', started: undefined, source: undefined };
  const v = view(status({ room: 0 }, { used: 4, heldMB: 1536, running: [job], waiting: [waitingSlots, behind] }), '', [], 0, now);
  expect(v.headline).toBe('2 waiting for room');
  expect(v.memory?.breakdown).toBe('8.0 GB free · 1.5 GB held for running jobs · 1.0 GB kept for apps');
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

it('says when a job runs longer than usual, and why a job waits for memory', () => {
  const slow = { ...job, started: '2026-11-06T10:00:00Z' };
  const v = view(status({}, { used: 1, heldMB: 7000, running: [slow], waiting: [{ ...job, id: 8, reason: 'memory' }] }), '', [], 0, now);
  expect(v.running[0]?.left).toBe('longer than usual');
  // 8 GB free, but 6.8 GB of it is held for the running job.
  expect(v.waiting[0]?.why).toBe('needs ~2.0 GB, 1.2 GB available');
});

it('lists recent jobs, with stops by a guard called out', () => {
  const recent = [
    { command: 'x', dir: '/dev/shop', key: 'git:/dev/shop/.git\n.\nvitest run', outcome: 'ceiling', exitCode: 77, peakMB: 12500, durationMs: 61_000, ended: '2026-11-06T10:04:00Z' },
    { command: 'y', dir: '/dev/blog', key: 'git:/dev/blog/.git\n.\npnpm lint', outcome: 'failed', exitCode: 1, peakMB: 900, durationMs: 12_000, ended: '2026-11-06T10:03:00Z' },
    { command: 'z', dir: '/dev/blog', key: 'git:/dev/blog/.git\n.\npnpm test', outcome: 'gave up', exitCode: 0, ended: '2026-11-06T10:02:00Z' },
  ];
  const v = view(status({ recent }), '', [], 0, now);
  expect(v.recent.map((r) => [r.title, r.where, r.outcome, r.tone, r.peak, r.took])).toEqual([
    ['vitest run', 'shop', 'ceiling', 'danger', '12.2 GB', '1m'],
    ['pnpm lint', 'blog', 'failed (1)', 'warning', '900 MB', '12s'],
    ['pnpm test', 'blog', 'gave up', 'neutral', '–', '–'],
  ]);
  expect(v.recent[0]?.outcomeDetail).toBe('Used more than its memory ceiling and was stopped');
  expect(v.recent[1]?.outcomeDetail).toBe('Exited with an error (exit 1)');
  expect(v.guardStops).toEqual([
    {
      key: 'ceiling',
      title: 'Over the memory ceiling per job',
      explanation: 'Each job may use up to 12.0 GB; these used more and were stopped.',
      rows: [{ key: '2026-11-06T10:04:00Z#0', title: 'vitest run', where: 'shop', peak: '12.2 GB', ended: Date.parse('2026-11-06T10:04:00Z') }],
      more: 0,
    },
  ]);
  const stops = Array.from({ length: 5 }, (_, i) => ({
    command: 'x',
    dir: '/dev/shop',
    key: 'git:/dev/shop/.git\n.\nvitest run',
    outcome: 'machine',
    exitCode: 77,
    ended: `2026-11-06T10:0${4 - i}:00Z`,
  }));
  const many = view(status({ recent: stops }), '', [], 0, now).guardStops[0];
  expect([many?.title, many?.rows.length, many?.more]).toEqual(['Machine nearly out of memory', 3, 2]);
});

it('groups jobs by repository', () => {
  const blog = { ...job, id: 9, key: 'git:/dev/blog/.git\napps/web\npnpm build', dir: '/dev/blog/apps/web', currentMB: 1024 };
  const v = view(status({}, { used: 2, running: [job, blog], waiting: [{ ...job, id: 10, reported: false }] }), '', [], 0, now);
  expect(v.byRepo).toEqual([
    { key: 'shop', repo: 'shop', summary: '2 jobs, 512 MB now' },
    { key: 'blog', repo: 'blog', summary: '1 job, 1.0 GB now' },
  ]);
});

it('surfaces setup problems and nearly exhausted memory', () => {
  const problem = { level: 'warn' as const, text: 'claude: hook not registered. Run: loadslot setup' };
  expect(view(status(), '', [problem], 4, now)).toMatchObject({ tone: 'warning', headline: '1 setup problem', problems: [problem] });
  const low = view(status({ memory: { totalMB: 16384, freeMB: 1000 } }, { used: 1, running: [job] }), '', [], 0, now);
  expect(low).toMatchObject({ headline: 'Memory nearly gone', tone: 'danger' });
  expect(low.memory?.tone).toBe('danger');
});

it('explains a missing loadslot', () => {
  const v = view(undefined, 'loadslot: not found');
  expect(v.problem).toEqual({ title: 'loadslot is not installed or not reachable', detail: 'loadslot: not found', link: 'https://github.com/ryusenkei-inc/loadslot#install' });
});
