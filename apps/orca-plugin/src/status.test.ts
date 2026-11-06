import { describe as group, expect, it } from 'vitest';
import { type Status, describe, summarize, unavailable } from './status.ts';

const base: Status = {
  mode: 'daemon',
  version: 'v0.4.0',
  memory: { totalMB: 16384, freeMB: 8192 },
  cpus: 8,
  snapshot: { slots: 4, used: 0, running: [], waiting: [] },
};

const job = {
  command: "sh -c 'cd app && pnpm test 2>&1 | tail'",
  key: 'git:/r/.git\napp\npnpm test',
  source: 'claude',
  estimateMB: 2048,
  queued: '2026-11-06T10:00:00Z',
  started: '2026-11-06T10:00:00Z',
};

group('summarize', () => {
  it('shows slots and free memory when idle', () => {
    expect(summarize(base)).toMatchObject({ text: 'loadslot 0/4 · 8.0 GB free', tone: 'neutral', icon: 'gauge' });
  });

  it('warns when jobs wait', () => {
    const s = { ...base, snapshot: { ...base.snapshot, used: 4, running: [job], waiting: [job] } };
    expect(summarize(s)).toMatchObject({ text: 'loadslot 4/4 · 1 waiting · 8.0 GB free', tone: 'warning' });
  });

  it('turns red when memory is nearly gone', () => {
    const s = { ...base, memory: { totalMB: 16384, freeMB: 1000 }, snapshot: { ...base.snapshot, used: 1, running: [job] } };
    expect(summarize(s).tone).toBe('danger');
  });
});

group('describe', () => {
  it('lists jobs by their learning name, source, memory and age', () => {
    const s = {
      ...base,
      snapshot: { ...base.snapshot, used: 1, running: [{ ...job, currentMB: 512, reported: true }], waiting: [{ ...job, source: undefined }] },
    };
    const now = Date.parse('2026-11-06T10:01:05Z');
    expect(describe(s, now)).toBe(
      [
        'daemon v0.4.0: 1 of 4 slots used, 8.0 GB of 16.0 GB free, 8 CPUs',
        'Running:',
        '  pnpm test (claude, 512 MB of ~2.0 GB, 1m05s)',
        'Waiting:',
        '  pnpm test (terminal, ~2.0 GB, 1m05s)',
      ].join('\n'),
    );
  });
});

it('tells how to install loadslot when it is missing', () => {
  const item = unavailable('loadslot: not found on PATH');
  expect(item.tone).toBe('warning');
  expect(item.tooltip).toContain('https://github.com/ryusenkei-inc/loadslot#install');
});

it('warns in the status bar when the setup has problems', () => {
  const item = summarize({ ...base, room: 4 }, [{ level: 'error', text: 'the hook runs a missing loadslot' }]);
  expect(item).toMatchObject({ tone: 'warning', icon: 'warning' });
  expect(item.tooltip.split('\n')[0]).toBe('! the hook runs a missing loadslot');
  expect(item.tooltip).toContain('Room for 4 more heavy jobs now');
});

it("keeps the tooltip within Orca's 256 characters", () => {
  const many = Array.from({ length: 20 }, (_, i) => ({ ...job, command: `job ${i}`, key: `k\n.\n${'x'.repeat(50)} ${i}` }));
  const s = { ...base, snapshot: { ...base.snapshot, used: 4, running: many, waiting: many } };
  const item = summarize(s, [{ level: 'warn', text: 'y'.repeat(300) }, { level: 'warn', text: 'z' }]);
  expect(item.tooltip.length).toBeLessThanOrEqual(256);
  expect(item.tooltip).toMatch(/^! y+/);
});
