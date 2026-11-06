// Orca plugin worker: shows loadslot's machine capacity in the status bar.
// Orca runs it in a Node process while the plugin is enabled and transpiles
// this file itself, so it has no build step and no runtime dependencies.
import { execFile } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { type Item, type Problem, type Status, summarize, unavailable } from './status.ts';
import { older } from './version.ts';
import { type View, view } from './view.ts';

/** The part of Orca's worker API this plugin uses (`orca/worker`). */
type OrcaWorkerApi = {
  host: { call(method: 'statusBar.update', params: { id: string } & Partial<Item>): Promise<unknown> };
  panels: {
    handle(name: 'view.get', handler: () => View): void;
    post(name: 'view', payload: View): void;
  };
  log(message: string): void;
};

const ITEM = 'capacity';
const POLL_MS = 5000;
const TIMEOUT_MS = 3000;
// `loadslot doctor` reads settings files and the daemon, so it runs once a
// minute, and whenever the binary changes.
const DOCTOR_EVERY = 12;

/** This plugin's version, from the manifest at the repository root. */
function pluginVersion(): string {
  try {
    const manifest = new URL('../../../orca-plugin.json', import.meta.url);
    return (JSON.parse(readFileSync(manifest, 'utf8')) as { version?: string }).version ?? '';
  } catch {
    return '';
  }
}

/** Where loadslot is looked for when it is not on the PATH Orca was started with. */
export function candidates(env: NodeJS.ProcessEnv = process.env, home = homedir(), platform = process.platform): string[] {
  const exe = platform === 'win32' ? 'loadslot.exe' : 'loadslot';
  const list = [env.LOADSLOT_BIN, 'loadslot', join(home, '.local', 'bin', exe), join(home, 'go', 'bin', exe)];
  if (platform === 'win32' && env.LOCALAPPDATA) list.push(join(env.LOCALAPPDATA, 'Microsoft', 'WinGet', 'Links', exe));
  if (platform === 'darwin') list.push('/opt/homebrew/bin/loadslot', '/usr/local/bin/loadslot');
  return list.filter((p): p is string => Boolean(p));
}

// A command that exits non-zero with output (`doctor --json` with warnings)
// still resolves with that output.
function run(file: string, args: string[]): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(file, args, { timeout: TIMEOUT_MS, windowsHide: true }, (err, stdout) =>
      err && !stdout ? reject(err) : resolve(stdout),
    );
  });
}

/**
 * Warnings and errors from `loadslot doctor --json`, plus a binary older than
 * the plugin, and how many checks ran.
 */
async function checkSetup(file: string, plugin: string): Promise<{ problems: Problem[]; checks: number }> {
  const problems: Problem[] = [];
  let checks = 0;
  try {
    const all = JSON.parse(await run(file, ['doctor', '--json'])) as Problem[];
    checks = all.length;
    problems.push(...all.filter((c) => c.level === 'warn' || c.level === 'error'));
  } catch {
    // Before loadslot 0.5 there is no `doctor --json`; the version check below covers it.
  }
  try {
    const binary = (await run(file, ['version'])).replace(/^loadslot\s+/, '').trim();
    if (plugin && older(binary, plugin)) {
      problems.push({ level: 'warn', text: `loadslot ${binary} is older than this plugin (${plugin}). Install loadslot ${plugin} or later.` });
    }
  } catch {
    // The status call already reports an unreachable binary.
  }
  return { problems, checks };
}

export default function activate(orca: OrcaWorkerApi): void {
  let found: string | undefined;
  let last: Status | undefined;
  let reason = 'loadslot has not been checked yet';
  let current: View = view(undefined, reason);
  const plugin = pluginVersion();
  let problems: Problem[] = [];
  let checks = 0;
  let checkedFile: string | undefined;
  let polls = 0;

  const status = async (): Promise<Status | undefined> => {
    for (const file of found ? [found] : candidates()) {
      try {
        const s = JSON.parse(await run(file, ['status', '--json'])) as Status;
        found = file;
        return s;
      } catch (err) {
        reason = `${file}: ${err instanceof Error ? err.message : String(err)}`;
      }
    }
    found = undefined;
    return undefined;
  };

  // Logged when it changes, for `orca plugin logs` when the item looks wrong.
  let state = '';
  const refresh = async () => {
    last = await status();
    const next = last ? `using ${found} (${last.mode} ${last.version ?? ''})`.trim() : `loadslot unavailable: ${reason}`;
    if (next !== state) orca.log((state = next));
    if (found && (found !== checkedFile || polls % DOCTOR_EVERY === 0)) {
      ({ problems, checks } = await checkSetup(found, plugin));
      checkedFile = found;
    }
    polls++;
    const item = last ? summarize(last, problems) : unavailable(reason);
    current = view(last, reason, problems, checks);
    // Posts reach the panel only while it is open; it asks for the latest when it opens.
    orca.panels.post('view', current);
    await orca.host.call('statusBar.update', { id: ITEM, ...item }).catch((err) => orca.log(`statusBar.update: ${err}`));
  };

  orca.panels.handle('view.get', () => current);

  // Each poll waits for the previous one, so a slow `loadslot status` never piles up.
  const loop = async () => {
    await refresh().catch((err) => orca.log(`refresh: ${err}`));
    setTimeout(loop, POLL_MS);
  };
  void loop();
}
