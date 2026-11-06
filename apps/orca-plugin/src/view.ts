// What the details panel shows, built by the worker from `loadslot status
// --json` and posted to the panel. Pure, so it is tested without Orca.
//
// The panel answers, in this order: is the machine OK; what runs and when it
// ends; what waits, why and for how long; what just happened (including jobs
// a guard stopped); which repositories use the machine; is the setup sound.
import { origin } from './origin.ts';
import type { Job, Problem, Status } from './status.ts';

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger';

/** A running job. */
export type JobRow = {
  key: string;
  title: string;
  /** Repository (and worktree) · agent or terminal. */
  detail: string;
  memory: string;
  /** When it started, ms since the epoch, for `RelativeTime`. */
  since: number;
  /** `~1m left` from its typical duration, or `longer than usual`. */
  left?: string;
};

/** A waiting job. */
export type WaitRow = {
  key: string;
  position: number;
  title: string;
  detail: string;
  /** Why it waits, in words. */
  why: string;
  /** When it was queued, ms since the epoch. */
  since: number;
  /** Rough time until it starts, from the running jobs' typical durations. */
  eta?: string;
};

/** A job that ended. */
export type RecentRow = {
  key: string;
  title: string;
  where: string;
  outcome: string;
  /** The outcome in a sentence. */
  outcomeDetail: string;
  tone: Tone;
  peak: string;
  took: string;
  /** When it ended, ms since the epoch. */
  ended: number;
};

/** What the panel offers to fix; the worker acts only when the person presses its button. */
export type Offer = {
  /** Download loadslot `version` to `to` and run its setup; `update` when an older one is there. */
  install?: { version: string; to: string; update: boolean };
  /** Register the agents' hooks (`loadslot setup`). */
  setup?: boolean;
  /** What the worker is doing now. */
  progress?: string;
  /** How the last install or setup went. */
  result?: { ok: boolean; text: string };
};

/** Everything the panel draws; plain JSON, as it crosses from the worker. */
export type View = {
  headline: string;
  tone: Tone;
  memory?: { usedPct: number; label: string; breakdown: string; tone: Tone };
  slots?: { usedPct: number; label: string };
  facts: { label: string; value: string }[];
  running: JobRow[];
  waiting: WaitRow[];
  recent: RecentRow[];
  /** Recent jobs a guard stopped, in sentences, so they are not missed. */
  guardStops: string[];
  byRepo: { key: string; repo: string; summary: string }[];
  problem?: { title: string; detail: string; link: string };
  /** Warnings and errors about the setup, with what to do. */
  problems: Problem[];
  /** How many setup checks ran (0 before the first `doctor`). */
  checks: number;
  offer?: Offer;
};

const installLink = 'https://github.com/ryusenkei-inc/loadslot#install';
const lowMemoryShare = 0.1;

// Short, for a table column in a narrow sidebar; `outcomeDetail` explains.
const outcomes: Record<string, [label: string, tone: Tone, detail: string]> = {
  ok: ['ok', 'success', 'Finished'],
  failed: ['failed', 'warning', 'Exited with an error'],
  timeout: ['timed out', 'warning', 'Ran longer than its timeout and was stopped'],
  ceiling: ['ceiling', 'danger', 'Used more than its memory ceiling and was stopped'],
  machine: ['out of memory', 'danger', 'Stopped because the machine was nearly out of memory'],
  lost: ['lost', 'warning', 'The client went away without reporting'],
  'gave up': ['gave up', 'neutral', 'Left the queue before it started'],
};

/** The view of a status and the setup's problems, or of why loadslot could not be read. */
export function view(s: Status | undefined, reason = '', problems: Problem[] = [], checks = 0, now = Date.now()): View {
  if (!s) {
    return {
      headline: 'loadslot not found',
      tone: 'warning',
      facts: [],
      running: [],
      waiting: [],
      recent: [],
      guardStops: [],
      byRepo: [],
      problem: { title: 'loadslot is not installed or not reachable', detail: reason, link: installLink },
      problems: [],
      checks,
    };
  }
  const { slots, used, running, waiting } = s.snapshot;
  const { totalMB, freeMB } = s.memory;
  const low = freeMB < totalMB * lowMemoryShare;

  let tone: Tone = used > 0 ? 'accent' : 'success';
  let headline = used > 0 ? `${used} of ${slots} slots in use` : 'Idle: heavy commands start at once';
  if (waiting.length > 0) {
    tone = 'warning';
    headline = `${waiting.length} waiting for room`;
  }
  if (problems.length > 0 && tone !== 'warning') {
    tone = 'warning';
    headline = `${problems.length} setup ${plural(problems.length, 'problem')}`;
  }
  if (low) {
    tone = 'danger';
    headline = 'Memory nearly gone';
  }

  const usedPct = pct(totalMB - freeMB, totalMB);
  const breakdown = [
    `${size(freeMB)} free`,
    ...(s.snapshot.heldMB ? [`${size(s.snapshot.heldMB)} held for running jobs`] : []),
    ...(s.reserveMB ? [`${size(s.reserveMB)} kept for apps`] : []),
  ].join(' · ');

  // Typical time left of each running job, soonest first: the waiting jobs
  // start roughly as these end.
  const lefts = running.map((j) => remaining(j, now)).filter((ms): ms is number => ms !== undefined && ms > 0);
  lefts.sort((a, b) => a - b);

  return {
    headline,
    tone,
    memory: {
      usedPct,
      label: `Memory ${usedPct}% used of ${size(totalMB)}`,
      breakdown,
      tone: low ? 'danger' : usedPct > 80 ? 'warning' : 'neutral',
    },
    slots: {
      usedPct: pct(used, slots),
      label: `${used} of ${slots} slots${s.room !== undefined ? ` · room for ${s.room} more` : ''}`,
    },
    facts: [
      { label: 'CPUs', value: String(s.cpus) },
      ...(s.ceilingMB ? [{ label: 'Ceiling per job', value: size(s.ceilingMB) }] : []),
      { label: 'loadslot', value: `${s.version ?? 'unknown version'}${s.mode === 'daemon' ? '' : ` (${s.mode})`}` },
    ],
    running: running.map((j) => {
      const left = remaining(j, now);
      return {
        key: key(j),
        title: title(j),
        detail: detail(j),
        memory: j.reported ? `${size(j.currentMB ?? 0)} of ~${size(j.estimateMB)}` : `~${size(j.estimateMB)}`,
        since: time(j.started, now),
        ...(left === undefined ? {} : { left: left > 0 ? `~${duration(left)} left` : 'longer than usual' }),
      };
    }),
    waiting: waiting.map((j, i) => {
      const eta = lefts[i];
      return {
        key: key(j),
        position: i + 1,
        title: title(j),
        detail: detail(j),
        why: why(j, freeMB, s.snapshot.heldMB ?? 0, i),
        since: time(j.queued, now),
        ...(eta === undefined ? {} : { eta: `~${duration(eta)}` }),
      };
    }),
    recent: (s.recent ?? []).map((f, i) => {
      const [label, oTone, detail] = outcomes[f.outcome] ?? [f.outcome, 'neutral', f.outcome];
      return {
        key: `${f.ended}#${i}`,
        title: title(f),
        where: origin(f),
        outcome: f.outcome === 'failed' && f.exitCode ? `failed (${f.exitCode})` : label,
        outcomeDetail: f.outcome === 'failed' && f.exitCode ? `${detail} (exit ${f.exitCode})` : detail,
        tone: oTone,
        peak: f.peakMB ? size(f.peakMB) : '–',
        took: f.durationMs ? duration(f.durationMs) : '–',
        ended: time(f.ended, now),
      };
    }),
    guardStops: (s.recent ?? [])
      .filter((f) => f.outcome === 'ceiling' || f.outcome === 'machine')
      .map((f) => `${title(f)} (${origin(f)}): ${(outcomes[f.outcome]?.[2] ?? '').toLowerCase()}${f.peakMB ? ` at ${size(f.peakMB)}` : ''}`),
    byRepo: byRepo([...running, ...waiting]),
    problems,
    checks,
  };
}

// Memory as admission sees it: free memory minus what running jobs may
// still grow to.
function why(j: Job, freeMB: number, heldMB: number, i: number): string {
  if (j.reason === 'queue' || (j.reason === undefined && i > 0)) return `behind #${i}`;
  if (j.reason === 'slots') return 'all slots busy';
  const available = Math.max(0, freeMB - heldMB);
  return `needs ~${size(j.estimateMB)}, ${size(available)} available`;
}

function remaining(j: Job, now: number): number | undefined {
  if (!j.expectedMs || !j.started) return undefined;
  return j.expectedMs - (now - Date.parse(j.started));
}

function byRepo(jobs: Job[]): View['byRepo'] {
  const groups = new Map<string, { jobs: number; mb: number }>();
  for (const j of jobs) {
    const repo = origin(j).replace(/ \(.*\)$/, '').split('/')[0] || 'other';
    const g = groups.get(repo) ?? { jobs: 0, mb: 0 };
    g.jobs++;
    g.mb += j.reported ? (j.currentMB ?? 0) : 0;
    groups.set(repo, g);
  }
  return [...groups].map(([repo, g]) => ({
    key: repo,
    repo,
    summary: `${g.jobs} ${plural(g.jobs, 'job')}${g.mb ? `, ${size(g.mb)} now` : ''}`,
  }));
}

function key(j: Job): string {
  return String(j.id ?? `${j.command}@${j.queued}`);
}

// The name the job learns under (the last line of its key) is shorter than
// the wrapped command line, e.g. `pnpm test` for `sh -c 'cd x && pnpm test'`.
function title(j: { key?: string; command: string }): string {
  return j.key?.split('\n').at(-1) || j.command;
}

function detail(j: Job): string {
  return [origin(j), j.source ?? 'terminal'].filter(Boolean).join(' · ');
}

function time(at: string | undefined, now: number): number {
  const t = at ? Date.parse(at) : Number.NaN;
  return Number.isNaN(t) ? now : t;
}

function pct(part: number, whole: number): number {
  return whole > 0 ? Math.min(100, Math.max(0, Math.round((part / whole) * 100))) : 0;
}

function plural(n: number, word: string): string {
  return n === 1 ? word : `${word}s`;
}

// MB under 1 GB, so a small job doesn't read as 0.0 GB.
function size(mb: number): string {
  return mb < 1024 ? `${Math.round(mb)} MB` : `${(mb / 1024).toFixed(1)} GB`;
}

function duration(ms: number): string {
  const s = Math.max(1, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  return m < 60 ? `${m}m` : `${Math.floor(m / 60)}h${String(m % 60).padStart(2, '0')}m`;
}
