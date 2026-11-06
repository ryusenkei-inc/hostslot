// What the status bar item and the status command show, from `hostslot
// status --json`. Pure functions, so they are tested without Orca.
import { origin } from './origin.ts';

/** One job in `hostslot status --json`. */
export type Job = {
  id?: number;
  command: string;
  dir?: string;
  key?: string;
  source?: string;
  estimateMB: number;
  currentMB?: number;
  reported?: boolean;
  queued: string;
  started?: string;
  /** Median duration of its recent runs (hostslot 0.5+). */
  expectedMs?: number;
  /** Memory admission holds back for it while it runs (hostslot 0.5+). */
  heldMB?: number;
  /** Why it waits: slots, memory or queue (hostslot 0.5+). */
  reason?: string;
};

/** A job that ended, from `status --json`'s `recent` (hostslot 0.5+). */
export type Finished = {
  command: string;
  dir: string;
  key?: string;
  source?: string;
  peakMB?: number;
  durationMs?: number;
  exitCode: number;
  outcome: string;
  ended: string;
};

/** The output of `hostslot status --json`. */
export type Status = {
  mode: string;
  version?: string;
  memory: { totalMB: number; freeMB: number };
  cpus: number;
  snapshot: { slots: number; used: number; heldMB?: number; running: Job[]; waiting: Job[] };
  /** How many more weight-1 jobs would start now; missing before hostslot 0.5. */
  room?: number;
  reserveMB?: number;
  ceilingMB?: number;
  recent?: Finished[];
};

/**
 * A check of `hostslot doctor --json`, or one the plugin adds: the full line,
 * the command that fixes it, and a title and detail for the panel.
 */
export type Problem = {
  level: 'ok' | 'info' | 'warn' | 'error';
  text: string;
  fix?: 'setup';
  title?: string;
  detail?: string;
};

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger';

/** A status bar item's fields, as `statusBar.update` takes them. */
export type Item = { text: string; tone: Tone; icon: 'gauge' | 'warning'; tooltip: string };

// Orca's limit for a status bar item's tooltip.
const TOOLTIP_MAX = 256;

function clamp(text: string, max: number): string {
  return text.length <= max ? text : `${text.slice(0, max - 1)}…`;
}

// Below this share of memory free, the item turns red: the next heavy job
// would likely page or be killed.
const lowMemoryShare = 0.1;

/** The item for a status and the setup's problems (warnings and errors). */
export function summarize(s: Status, problems: Problem[] = []): Item {
  const { slots, used, waiting } = s.snapshot;
  const parts = [`${used}/${slots}`];
  if (waiting.length > 0) parts.push(`${waiting.length} waiting`);
  parts.push(`${gb(s.memory.freeMB)} free`);
  let tone: Tone = used > 0 ? 'accent' : 'neutral';
  if (waiting.length > 0) tone = 'warning';
  if (problems.length > 0) tone = 'warning';
  if (s.memory.freeMB < s.memory.totalMB * lowMemoryShare) tone = 'danger';
  // The details live in the panel; the tooltip is a summary within Orca's
  // 256-character limit (longer ones are refused and the item stops updating).
  const lines = describe(s).split('\n');
  const summary = lines.filter((l) => !l.startsWith(' ') && !l.endsWith(':'));
  const first = problems[0];
  const issue = first ? [`! ${first.title ?? first.text}${problems.length > 1 ? ` (+${problems.length - 1} more)` : ''}`] : [];
  return {
    text: `Hostslot ${parts.join(' · ')}`,
    tone,
    icon: problems.length > 0 ? 'warning' : 'gauge',
    tooltip: clamp([...issue, ...summary, 'Click for details'].join('\n'), TOOLTIP_MAX),
  };
}

/** The item when the hostslot command cannot be run. */
export function unavailable(reason: string): Item {
  return {
    text: 'Hostslot: not found',
    tone: 'warning',
    icon: 'warning',
    tooltip: `${reason || 'Not on the PATH or in the usual places.'}\nInstall Hostslot: https://github.com/ryusenkei-inc/hostslot#quick-start`,
  };
}

/** A few lines on the machine and its jobs, for the tooltip and the command. */
export function describe(s: Status, now = Date.now()): string {
  const { slots, used, running, waiting } = s.snapshot;
  const lines = [
    `${s.mode}${s.version ? ` ${s.version}` : ''}: ${used} of ${slots} slots used, ${gb(s.memory.freeMB)} of ${gb(s.memory.totalMB)} free, ${s.cpus} CPUs`,
  ];
  // Room says more than the free slots only when memory is what limits it.
  if (s.room !== undefined && s.room < slots - used) {
    lines.push(`Room for ${s.room} more heavy ${s.room === 1 ? 'job' : 'jobs'} now (memory)`);
  }
  const list = (title: string, jobs: Job[], since: (j: Job) => string | undefined) => {
    if (jobs.length === 0) return;
    lines.push(`${title}:`);
    for (const j of jobs) {
      const memory = j.reported ? `${gb(j.currentMB ?? 0)} of ~${gb(j.estimateMB)}` : `~${gb(j.estimateMB)}`;
      lines.push(`  ${label(j)} (${[origin(j), j.source ?? 'terminal'].filter(Boolean).join(', ')}, ${memory}, ${age(since(j), now)})`);
    }
  };
  list('Running', running, (j) => j.started);
  list('Waiting', waiting, (j) => j.queued);
  return lines.join('\n');
}

// The name the job learns under (the last line of its key) is shorter than
// the wrapped command line, e.g. `pnpm test` for `sh -c 'cd x && pnpm test'`.
function label(j: Job): string {
  const name = j.key?.split('\n').at(-1) || j.command;
  return name.length > 60 ? `${name.slice(0, 57)}...` : name;
}

// MB under 1 GB, so a small job doesn't read as 0.0 GB.
function gb(mb: number): string {
  return mb < 1024 ? `${Math.round(mb)} MB` : `${(mb / 1024).toFixed(1)} GB`;
}

function age(since: string | undefined, now: number): string {
  const ms = since ? now - Date.parse(since) : 0;
  const s = Math.max(0, Math.round(ms / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`;
}
