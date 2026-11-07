// What the details panel shows, built by the worker from `hostslot status
// --json` and posted to the panel. Pure, so it is tested without Orca.
//
// The panel answers, in this order: is the machine OK; what runs and when it
// ends; what waits, why and for how long; what a guard stopped; what just
// happened; which repositories use the machine; is the setup sound. Each
// fact appears once.
import { origin } from './origin.ts';
import type { Job, PortEntry, Problem, Status } from './status.ts';

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

/** A dev server's port. */
export type PortRow = {
  key: string;
  name: string;
  /** Repository (and worktree). */
  where: string;
  url: string;
  running: boolean;
  /** Set when it listens elsewhere than its port. */
  note?: string;
};

/** Ports of dev servers not running, by repository. */
export type AssignedGroup = {
  repo: string;
  /** The main checkout first, then worktrees and services by name. */
  rows: { key: string; worktree: string; name: string; port: string }[];
};

/** What one guard stopped recently, summed up; each job is a row of Recent. */
export type GuardStops = {
  key: string;
  title: string;
  /** `4 jobs · api ×2, shop` */
  summary: string;
  /** When the latest was stopped, ms since the epoch. */
  last: number;
  explanation: string;
};

/** A setup problem in brief. */
export type SetupProblem = { key: string; tone: 'warning' | 'danger'; title: string; detail?: string };

/** The one button that fixes what it can; the worker acts only when it is pressed. */
export type SetupAction = { request: 'install' | 'setup'; label: string; note: string };

/** The outcome of an install or setup, item by item, and what is left to do. */
export type ActionResult = {
  ok: boolean;
  title: string;
  items: { label: string; value: string }[];
  /** A step only the person can take, such as trusting a hook in Codex. */
  next?: string;
  /** Why it failed. */
  text?: string;
};

/** Whether hostslot is installed and wired up. */
export type Setup = {
  problems: SetupProblem[];
  action?: SetupAction;
  /** What the worker is doing now. */
  progress?: string;
  result?: ActionResult;
  /** About this machine and hostslot, when there are no problems. */
  facts: { label: string; value: string }[];
  /** How many checks ran (0 before the first `doctor`). */
  checks: number;
};

/** Everything the panel draws; plain JSON, as it crosses from the worker. */
export type View = {
  headline: string;
  tone: Tone;
  /** Missing while hostslot can't be read. */
  machine?: {
    memory: { usedPct: number; label: string; note?: string; tone: Tone };
    slots: { usedPct: number; label: string };
    /** Missing when the OS can't tell. */
    cpu?: { usedPct: number; label: string; tone: Tone };
    /** Missing without an NVIDIA GPU. */
    gpu?: { usedPct: number; label: string };
  };
  running: JobRow[];
  waiting: WaitRow[];
  /** Dev servers' ports, running first. */
  ports: PortRow[];
  assigned: AssignedGroup[];
  stops: GuardStops[];
  recent: RecentRow[];
  byRepo: { key: string; repo: string; summary: string }[];
  setup: Setup;
};

const lowMemoryShare = 0.1;

// Short, for a table column in a narrow sidebar; `outcomeDetail` explains.
const outcomes: Record<string, [label: string, tone: Tone, detail: string]> = {
  ok: ['ok', 'success', 'Finished'],
  failed: ['failed', 'warning', 'Exited with an error'],
  timeout: ['timed out', 'warning', 'Ran longer than its timeout and was stopped'],
  ceiling: ['over ceiling', 'danger', 'Used more than its memory ceiling and was stopped'],
  machine: ['out of memory', 'danger', 'Stopped because the machine was nearly out of memory'],
  lost: ['lost', 'warning', 'The client went away without reporting'],
  'gave up': ['gave up', 'neutral', 'Left the queue before it started'],
};

/** The view of a status and the setup's problems, or of why hostslot could not be read. */
/** `dismissedAt`: stops that ended at or before it (ms since the epoch) leave the alert. */
export function view(s: Status | undefined, reason = '', problems: Problem[] = [], checks = 0, now = Date.now(), dismissedAt = 0): View {
  if (!s) {
    return {
      headline: "Hostslot isn't installed",
      tone: 'warning',
      running: [],
      waiting: [],
      ports: [],
      assigned: [],
      stops: [],
      recent: [],
      byRepo: [],
      setup: {
        problems: [{ key: 'missing', tone: 'warning', title: 'Hostslot not found', detail: reason || 'Not on the PATH or in the usual places.' }],
        facts: [],
        checks,
      },
    };
  }
  const { slots, used, running, waiting } = s.snapshot;
  const { totalMB, freeMB } = s.memory;
  const low = freeMB < totalMB * lowMemoryShare;
  const setupProblems = problems.map(brief);

  let tone: Tone = used > 0 ? 'accent' : 'success';
  let headline = used > 0 ? `Running ${running.length} heavy ${plural(running.length, 'command')}` : 'Idle';
  if (setupProblems.length > 0) {
    tone = 'warning';
    headline = 'Setup needs attention';
  }
  if (waiting.length > 0) {
    tone = 'warning';
    headline = `${waiting.length} waiting for room`;
  }
  if (low) {
    tone = 'danger';
    headline = 'Memory nearly gone';
  }

  const usedPct = pct(totalMB - freeMB, totalMB);
  const note = [
    ...(s.snapshot.heldMB ? [`${size(s.snapshot.heldMB)} held for running jobs`] : []),
    ...(s.reserveMB ? [`${size(s.reserveMB)} kept for apps`] : []),
  ].join(' · ');
  // Room says more than the free slots only when memory is what limits it.
  const free = slots - used;
  const room = s.room !== undefined && s.room < free ? ` · room for ${s.room} more` : '';

  // Typical time left of each running job, soonest first: the waiting jobs
  // start roughly as these end.
  const lefts = running.map((j) => remaining(j, now)).filter((ms): ms is number => ms !== undefined && ms > 0);
  lefts.sort((a, b) => a - b);

  return {
    headline,
    tone,
    machine: {
      memory: {
        usedPct,
        label: `Memory: ${size(freeMB)} free of ${size(totalMB)}`,
        ...(note ? { note } : {}),
        tone: low ? 'danger' : usedPct > 80 ? 'warning' : 'neutral',
      },
      slots: { usedPct: pct(used, slots), label: `Slots: ${used} of ${slots} in use${room}` },
      ...(s.cpuBusyPct === undefined
        ? {}
        : {
            cpu: {
              usedPct: s.cpuBusyPct,
              label: `CPU: ${s.cpuBusyPct}% busy`,
              tone: s.cpuBusyLimitPct !== undefined && s.cpuBusyPct >= s.cpuBusyLimitPct ? 'warning' : 'neutral',
            },
          }),
      ...(s.gpu
        ? { gpu: { usedPct: pct(s.gpu.totalMB - s.gpu.freeMB, s.gpu.totalMB), label: `GPU: ${size(s.gpu.freeMB)} free of ${size(s.gpu.totalMB)}` } }
        : {}),
    },
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
        why: why(j, freeMB, s.snapshot.heldMB ?? 0, s.reserveMB ?? 0, i),
        since: time(j.queued, now),
        ...(eta === undefined ? {} : { eta: `~${duration(eta)}` }),
      };
    }),
    ports: portRows(s.ports ?? []).filter((p) => p.running),
    assigned: assignedGroups(portRows(s.ports ?? []).filter((p) => !p.running)),
    stops: guardStops(s, now, dismissedAt),
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
    byRepo: byRepo([...running, ...waiting]),
    setup: {
      problems: setupProblems,
      facts: [
        { label: 'Hostslot', value: `${s.version ?? 'unknown version'}${s.mode === 'daemon' ? '' : ` (${s.mode})`}` },
        { label: 'CPUs', value: String(s.cpus) },
        ...(s.ceilingMB ? [{ label: 'Memory ceiling per job', value: size(s.ceilingMB) }] : []),
      ],
      checks,
    },
  };
}

// An older hostslot's doctor checks have only their full line.
function brief(p: Problem, i: number): SetupProblem {
  const tone = p.level === 'error' ? 'danger' : 'warning';
  if (!p.title) return { key: `${i}`, tone, title: p.text };
  return { key: `${i}`, tone, title: p.title, ...(p.detail ? { detail: p.detail } : {}) };
}

// Hostslot 0.9 listed assignments without key and state; those are skipped.
function portRows(entries: PortEntry[]): PortRow[] {
  const rows = entries.filter((e) => typeof e.key === 'string').map((e) => {
    const [head = '', top = ''] = e.key.split('\n');
    const where = origin({ key: `${head}\n.\n${e.name}`, dir: top || e.worktree });
    const elsewhere = e.running && e.listening?.length && !e.listening.includes(e.port);
    return {
      key: e.key,
      name: e.name,
      where,
      url: `http://localhost:${e.port}`,
      running: e.running,
      ...(elsewhere ? { note: `listens on ${e.listening?.join(', ')}, not ${e.port}: make it read PORT` } : {}),
    };
  });
  return rows.sort((a, b) => Number(b.running) - Number(a.running));
}

const mainCheckout = 'main checkout';

function assignedGroups(rows: PortRow[]): AssignedGroup[] {
  const groups = new Map<string, AssignedGroup['rows']>();
  for (const r of rows) {
    const m = /^(.*) \((.*)\)$/.exec(r.where);
    const repo = m?.[1] ?? r.where;
    const list = groups.get(repo) ?? [];
    list.push({ key: r.key, worktree: m?.[2] ?? mainCheckout, name: r.name, port: r.url.replace('http://localhost:', '') });
    groups.set(repo, list);
  }
  const order = (a: AssignedGroup['rows'][number], b: AssignedGroup['rows'][number]) =>
    Number(b.worktree === mainCheckout) - Number(a.worktree === mainCheckout) || a.worktree.localeCompare(b.worktree) || a.name.localeCompare(b.name);
  return [...groups]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([repo, list]) => ({ repo, rows: list.sort(order) }));
}

const guards: { outcome: string; title: string; explanation: (s: Status) => string }[] = [
  {
    outcome: 'machine',
    title: 'Machine nearly out of memory',
    explanation: () => 'Hostslot stopped the largest job running in the background. Run it again when there is room.',
  },
  {
    outcome: 'ceiling',
    title: 'Over the memory ceiling per job',
    explanation: (s) => `Each job may use up to ${s.ceilingMB ? size(s.ceilingMB) : 'its ceiling'}; these used more.`,
  },
];

// Older stops stay rows of Recent but no longer alarm.
const STOPS_SHOWN_MS = 30 * 60_000;

function guardStops(s: Status, now: number, dismissedAt: number): GuardStops[] {
  const recent = (s.recent ?? []).filter((f) => {
    const ended = time(f.ended, now);
    return ended > dismissedAt && now - ended < STOPS_SHOWN_MS;
  });
  return guards.flatMap((g) => {
    const stopped = recent.filter((f) => f.outcome === g.outcome);
    const latest = stopped[0];
    if (!latest) return [];
    const repos = new Map<string, number>();
    for (const f of stopped) {
      const repo = origin(f) || 'other';
      repos.set(repo, (repos.get(repo) ?? 0) + 1);
    }
    const where = [...repos].map(([repo, n]) => (n > 1 ? `${repo} ×${n}` : repo)).join(', ');
    return [
      {
        key: g.outcome,
        title: g.title,
        summary: `${stopped.length} ${plural(stopped.length, 'job')} · ${where}`,
        last: time(latest.ended, now),
        explanation: g.explanation(s),
      },
    ];
  });
}

// Memory as admission sees it: free memory minus what running jobs may
// still grow to and the reserve kept for apps.
function why(j: Job, freeMB: number, heldMB: number, reserveMB: number, i: number): string {
  if (j.reason === 'queue' || (j.reason === undefined && i > 0)) return `behind #${i}`;
  if (j.reason === 'slots') return 'all slots busy';
  if (j.reason === 'cpu') return 'CPUs busy';
  if (j.reason === 'gpu') return 'not enough free GPU memory';
  const available = Math.max(0, freeMB - heldMB - reserveMB);
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
