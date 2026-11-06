import { expect, it } from 'vitest';
import type { Status } from './status.ts';
import { view } from './view.ts';

const job = {
  id: 7,
  command: "sh -c 'pnpm test'",
  key: 'git:/r/.git\n.\npnpm test',
  source: 'claude',
  estimateMB: 2048,
  currentMB: 512,
  reported: true,
  queued: '2026-11-06T10:00:00Z',
  started: '2026-11-06T10:00:05Z',
};

const status = (over: Partial<Status['snapshot']> = {}, freeMB = 8192): Status => ({
  mode: 'daemon',
  version: 'v0.4.0',
  memory: { totalMB: 16384, freeMB },
  cpus: 8,
  snapshot: { slots: 4, used: 0, running: [], waiting: [], ...over },
});

it('says when the machine is idle', () => {
  const v = view(status());
  expect(v).toMatchObject({ headline: 'Idle: heavy commands start at once', tone: 'success', running: [], waiting: [] });
  expect(v.facts).toContainEqual({ label: 'Free memory', value: '8.0 GB of 16.0 GB' });
  expect(JSON.parse(JSON.stringify(v))).toEqual(v);
});

it('lists running and waiting jobs by their learning name', () => {
  const v = view(status({ used: 4, running: [job], waiting: [{ ...job, id: 8, reported: false, source: undefined }] }));
  expect(v.headline).toBe('1 waiting for room');
  expect(v.tone).toBe('warning');
  expect(v.running[0]).toEqual({ key: '7', title: 'pnpm test', source: 'claude', memory: '512 MB of ~2.0 GB', since: Date.parse(job.started) });
  expect(v.waiting[0]).toMatchObject({ key: '8', source: 'terminal', memory: '~2.0 GB', since: Date.parse(job.queued) });
});

it('flags memory that is nearly gone', () => {
  const v = view(status({ used: 1, running: [job] }, 1000));
  expect(v).toMatchObject({ headline: 'Memory nearly gone', tone: 'danger' });
});

it('explains a missing loadslot', () => {
  const v = view(undefined, 'loadslot: not found');
  expect(v.problem).toEqual({ title: 'loadslot is not installed or not reachable', detail: 'loadslot: not found', link: 'https://github.com/ryusenkei-inc/loadslot#install' });
});

it('shows room and setup problems', () => {
  const v = view({ ...status(), room: 3 }, '', [{ level: 'warn', text: 'claude: hook not registered. Run: loadslot setup' }]);
  expect(v.facts).toContainEqual({ label: 'Room now', value: '3 more heavy jobs' });
  expect(v).toMatchObject({ tone: 'warning', headline: '1 setup problem' });
  expect(v.problems).toHaveLength(1);
});
