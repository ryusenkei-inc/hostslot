// What the status bar item and the status command show, from `loadslot
// status --json`. Pure functions, so they are tested without Orca.

/** One job in `loadslot status --json`. */
export type Job = {
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
};

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger';

/** A status bar item's fields, as `statusBar.update` takes them. */
export type Item = { text: string; tone: Tone; icon: 'gauge' | 'warning'; tooltip: string };

// Below this share of memory free, the item turns red: the next heavy job
// would likely page or be killed.
const lowMemoryShare = 0.1;

/** The item for a status. */
export function summarize(s: Status): Item {
  const { slots, used, waiting } = s.snapshot;
  const parts = [`${used}/${slots}`];
  if (waiting.length > 0) parts.push(`${waiting.length} waiting`);
  parts.push(`${gb(s.memory.freeMB)} free`);
  let tone: Tone = used > 0 ? 'accent' : 'neutral';
  if (waiting.length > 0) tone = 'warning';
  if (s.memory.freeMB < s.memory.totalMB * lowMemoryShare) tone = 'danger';
  return { text: `loadslot ${parts.join(' · ')}`, tone, icon: 'gauge', tooltip: describe(s) };
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

function gb(mb: number): string {
  return `${(mb / 1024).toFixed(1)} GB`;
}

function age(since: string | undefined, now: number): string {
  const ms = since ? now - Date.parse(since) : 0;
  const s = Math.max(0, Math.round(ms / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`;
}
