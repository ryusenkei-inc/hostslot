// What the status bar item and the status command show, from `loadslot
// status --json`. Pure functions, so they are tested without Orca.

/** One job in `loadslot status --json`. */
export type Job = {
  id?: number;
  command: string;
  key?: string;
  source?: string;
  estimateMB: number;
  currentMB?: number;
  reported?: boolean;
  queued: string;
  started?: string;
};

/** The output of `loadslot status --json`. */
export type Status = {
  mode: string;
  version?: string;
  memory: { totalMB: number; freeMB: number };
  cpus: number;
  snapshot: { slots: number; used: number; running: Job[]; waiting: Job[] };
  /** How many more weight-1 jobs would start now; missing before loadslot 0.5. */
  room?: number;
};

/** A check of `loadslot doctor --json`, or one the plugin adds. */
export type Problem = { level: 'ok' | 'info' | 'warn' | 'error'; text: string };

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
  const issue = first ? [`! ${first.text}${problems.length > 1 ? ` (+${problems.length - 1} more)` : ''}`] : [];
  return {
    text: `loadslot ${parts.join(' · ')}`,
    tone,
    icon: problems.length > 0 ? 'warning' : 'gauge',
    tooltip: clamp([...issue, ...summary, 'Click for details'].join('\n'), TOOLTIP_MAX),
  };
}

/** The item when the loadslot command cannot be run. */
export function unavailable(reason: string): Item {
  return {
    text: 'loadslot: not found',
    tone: 'warning',
    icon: 'warning',
    tooltip: `${reason}\nInstall loadslot: https://github.com/ryusenkei-inc/loadslot#install`,
  };
}

/** A few lines on the machine and its jobs, for the tooltip and the command. */
export function describe(s: Status, now = Date.now()): string {
  const { slots, used, running, waiting } = s.snapshot;
  const lines = [
    `${s.mode}${s.version ? ` ${s.version}` : ''}: ${used} of ${slots} slots used, ${gb(s.memory.freeMB)} of ${gb(s.memory.totalMB)} free, ${s.cpus} CPUs`,
  ];
  if (s.room !== undefined) lines.push(`Room for ${s.room} more heavy ${s.room === 1 ? 'job' : 'jobs'} now`);
  const list = (title: string, jobs: Job[], since: (j: Job) => string | undefined) => {
    if (jobs.length === 0) return;
    lines.push(`${title}:`);
    for (const j of jobs) {
      const memory = j.reported ? `${gb(j.currentMB ?? 0)} of ~${gb(j.estimateMB)}` : `~${gb(j.estimateMB)}`;
      lines.push(`  ${label(j)} (${j.source ?? 'terminal'}, ${memory}, ${age(since(j), now)})`);
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
