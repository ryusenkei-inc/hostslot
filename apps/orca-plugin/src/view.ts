// What the details panel shows, built by the worker from `loadslot status
// --json` and posted to the panel. Pure, so it is tested without Orca.
import type { Job, Status } from './status.ts';

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger';

/** One job row in the panel. */
export type JobRow = {
  key: string;
  title: string;
  source: string;
  memory: string;
  /** Milliseconds since the epoch, for `RelativeTime`. */
  since: number;
};

/** Everything the panel draws; plain JSON, as it crosses from the worker. */
export type View = {
  headline: string;
  tone: Tone;
  facts: { label: string; value: string; tone?: Tone }[];
  running: JobRow[];
  waiting: JobRow[];
  problem?: { title: string; detail: string; link: string };
};

const installLink = 'https://github.com/ryusenkei-inc/loadslot#install';
const lowMemoryShare = 0.1;

/** The view of a status, or of why loadslot could not be read. */
export function view(s: Status | undefined, reason = ''): View {
  if (!s) {
    return {
      headline: 'loadslot not found',
      tone: 'warning',
      facts: [],
      running: [],
      waiting: [],
      problem: { title: 'loadslot is not installed or not reachable', detail: reason, link: installLink },
    };
  }
  const { slots, used, running, waiting } = s.snapshot;
  const free = s.memory.freeMB;
  const low = free < s.memory.totalMB * lowMemoryShare;
  let tone: Tone = used > 0 ? 'accent' : 'success';
  let headline = used > 0 ? `${used} of ${slots} slots in use` : 'Idle: heavy commands start at once';
  if (waiting.length > 0) {
    tone = 'warning';
    headline = `${waiting.length} waiting for room`;
  }
  if (low) {
    tone = 'danger';
    headline = 'Memory nearly gone';
  }
  return {
    headline,
    tone,
    facts: [
      { label: 'Slots', value: `${used} / ${slots}` },
      // Orca refuses values that are not plain JSON, so no `tone: undefined`.
      { label: 'Free memory', value: `${gb(free)} of ${gb(s.memory.totalMB)}`, ...(low ? { tone: 'danger' as const } : {}) },
      { label: 'CPUs', value: String(s.cpus) },
      { label: 'Daemon', value: `${s.mode}${s.version ? ` ${s.version}` : ''}` },
    ],
    running: running.map((j) => row(j, j.started)),
    waiting: waiting.map((j) => row(j, j.queued)),
  };
}

function row(j: Job, since: string | undefined): JobRow {
  return {
    key: String(j.id ?? `${j.command}@${j.queued}`),
    title: j.key?.split('\n').at(-1) || j.command,
    source: j.source ?? 'terminal',
    memory: j.reported ? `${gb(j.currentMB ?? 0)} of ~${gb(j.estimateMB)}` : `~${gb(j.estimateMB)}`,
    since: since ? Date.parse(since) : Date.now(),
  };
}

// MB under 1 GB, so a small job doesn't read as 0.0 GB.
function gb(mb: number): string {
  return mb < 1024 ? `${Math.round(mb)} MB` : `${(mb / 1024).toFixed(1)} GB`;
}
