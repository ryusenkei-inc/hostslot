// Orca plugin worker: shows hostslot's machine capacity in the status bar.
// Orca runs it in a Node process while the plugin is enabled and transpiles
// this file itself, so it has no build step and no runtime dependencies.
import { execFile } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { dataDir, install, installedPath, offer, setupItems } from './install.ts';
import { type Item, type PortEntry, type Problem, type Status, summarize, unavailable } from './status.ts';
import { older } from './version.ts';
import { type ActionResult, type Setup, type View, view } from './view.ts';

/** The part of Orca's worker API this plugin uses (`orca/worker`). */
type OrcaWorkerApi = {
  host: { call(method: 'statusBar.update', params: { id: string } & Partial<Item>): Promise<unknown> };
  panels: {
    handle(name: 'view.get' | 'install' | 'setup', handler: () => unknown): void;
    post(name: 'view', payload: View): void;
  };
  log(message: string): void;
};

const ITEM = 'capacity';
const POLL_MS = 5000;
const TIMEOUT_MS = 3000;
// `hostslot doctor` reads settings files and the daemon, so it runs once a
// minute, and whenever the binary changes.
const DOCTOR_EVERY = 12;
const SETUP_MS = 15_000;

/**
 * A made-up status to show instead of the live one, for screenshots of the
 * panel: `panel-fixture.json` in Hostslot's data directory, while it exists.
 */
function readFixture(): Status | undefined {
  try {
    return JSON.parse(readFileSync(join(dataDir(), 'panel-fixture.json'), 'utf8')) as Status;
  } catch {
    return undefined;
  }
}

/** This plugin's version, from the manifest at the repository root. */
function pluginVersion(): string {
  try {
    const manifest = new URL('../../../orca-plugin.json', import.meta.url);
    return (JSON.parse(readFileSync(manifest, 'utf8')) as { version?: string }).version ?? '';
  } catch {
    return '';
  }
}

/**
 * Where hostslot is looked for: the copy this plugin installed comes before
 * the PATH Orca was started with, since the plugin installs only when that
 * one is missing or older.
 */
export function candidates(env: NodeJS.ProcessEnv = process.env, home = homedir(), platform = process.platform): string[] {
  const exe = platform === 'win32' ? 'hostslot.exe' : 'hostslot';
  const list = [env.HOSTSLOT_BIN, installedPath(env, home, platform), 'hostslot', join(home, '.local', 'bin', exe), join(home, 'go', 'bin', exe)];
  if (platform === 'win32' && env.LOCALAPPDATA) list.push(join(env.LOCALAPPDATA, 'Microsoft', 'WinGet', 'Links', exe));
  if (platform === 'darwin') list.push('/opt/homebrew/bin/hostslot', '/usr/local/bin/hostslot');
  return list.filter((p): p is string => Boolean(p));
}

// A command that exits non-zero with output (`doctor --json` with warnings)
// still resolves with that output.
function run(file: string, args: string[], timeout = TIMEOUT_MS): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(file, args, { timeout, windowsHide: true }, (err, stdout) =>
      err && !stdout ? reject(err) : resolve(stdout),
    );
  });
}

/**
 * Warnings and errors from `hostslot doctor --json`, plus a binary older than
 * the plugin, how many checks ran, and the binary's version.
 */
async function checkSetup(file: string, plugin: string): Promise<{ problems: Problem[]; checks: number; binary: string }> {
  const problems: Problem[] = [];
  let checks = 0;
  let binary = '';
  try {
    const all = JSON.parse(await run(file, ['doctor', '--json'])) as Problem[];
    checks = all.length;
    problems.push(...all.filter((c) => c.level === 'warn' || c.level === 'error'));
  } catch {
    // Before hostslot 0.5 there is no `doctor --json`; the version check below covers it.
  }
  try {
    binary = (await run(file, ['version'])).replace(/^hostslot\s+/, '').trim();
    if (plugin && older(binary, plugin)) {
      problems.push({
        level: 'warn',
        text: `hostslot ${binary} is older than this plugin (${plugin}). Install Hostslot ${plugin} or later.`,
        title: 'Hostslot is older than this plugin',
        detail: `Hostslot ${binary}; the plugin needs ${plugin} or later.`,
      });
    }
  } catch {
    // The status call already reports an unreachable binary.
  }
  return { problems, checks, binary };
}

export default function activate(orca: OrcaWorkerApi): void {
  let found: string | undefined;
  let last: Status | undefined;
  let reason = 'hostslot has not been checked yet';
  let current: View = view(undefined, reason);
  const plugin = pluginVersion();
  let problems: Problem[] = [];
  let checks = 0;
  let binary = '';
  let checkedFile: string | undefined;
  let polls = 0;
  const target = installedPath();
  // The install or setup the person started from the panel.
  let task: Pick<Setup, 'progress' | 'result'> = {};

  // A candidate that doesn't exist says nothing worth showing; one that
  // exists but fails does.
  const status = async (): Promise<Status | undefined> => {
    reason = '';
    for (const file of found ? [found] : candidates()) {
      try {
        const s = JSON.parse(await run(file, ['status', '--json'])) as Status;
        found = file;
        return s;
      } catch (err) {
        if ((err as NodeJS.ErrnoException).code !== 'ENOENT') {
          reason = `${file}: ${err instanceof Error ? err.message : String(err)}`;
        }
      }
    }
    found = undefined;
    return undefined;
  };

  // Logged when it changes, for `orca plugin logs` when the item looks wrong.
  let state = '';
  const publish = () => {
    current = view(last, reason, problems, checks);
    const action = offer({ found: Boolean(found), binary, plugin, problems, target });
    current.setup = { ...current.setup, ...(action ? { action } : {}), ...task };
    // Posts reach the panel only while it is open; it asks for the latest when it opens.
    orca.panels.post('view', current);
  };

  // Runs one install or setup at a time, showing its progress, then checks
  // everything again (another binary may be the one to use now).
  const act = async (steps: (say: (progress: string) => void) => Promise<ActionResult>) => {
    if (task.progress) return;
    const say = (progress: string) => {
      task = { progress };
      publish();
    };
    try {
      task = { result: await steps(say) };
    } catch (err) {
      const text = err instanceof Error ? err.message : String(err);
      task = { result: { ok: false, title: 'Could not finish', items: [], text } };
    }
    orca.log(`${task.result?.title}: ${JSON.stringify(task.result)}`);
    found = undefined;
    checkedFile = undefined;
    await refresh().catch((err) => orca.log(`refresh: ${err}`));
  };

  // An older hostslot has no `setup --json`; its text output becomes one item.
  const setup = async (file: string) =>
    setupItems(await run(file, ['setup', '--json'], SETUP_MS).catch(() => run(file, ['setup'], SETUP_MS)));

  orca.panels.handle('install', () =>
    act(async (say) => {
      say(`Downloading Hostslot ${plugin}…`);
      await install({ version: plugin, target });
      say("Registering the agents' hooks…");
      const { items, next } = await setup(target);
      const result: ActionResult = { ok: true, title: `Installed Hostslot ${plugin}`, items: [{ label: 'Location', value: target }, ...items] };
      if (next) result.next = next;
      return result;
    }),
  );
  orca.panels.handle('setup', () =>
    act(async (say) => {
      if (!found) throw new Error('Hostslot is not installed');
      say("Registering the agents' hooks…");
      const { items, next } = await setup(found);
      const result: ActionResult = { ok: true, title: 'Setup done', items };
      if (next) result.next = next;
      return result;
    }),
  );

  const refresh = async () => {
    const fixture = readFixture();
    last = fixture ?? (await status());
    if (fixture) {
      problems = [];
      checks = 5;
    } else if (last && found) {
      // Hostslot before 0.10 has no port list; the panel then shows none.
      last.ports = await run(found, ['port', '--list', '--json'])
        .then((out) => JSON.parse(out) as PortEntry[])
        .catch(() => undefined);
      if (!last.ports) delete last.ports;
    }
    const next = last ? `using ${found} (${last.mode} ${last.version ?? ''})`.trim() : `hostslot unavailable: ${reason || 'not found'}`;
    if (next !== state) orca.log((state = next));
    if (!fixture && found && (found !== checkedFile || polls % DOCTOR_EVERY === 0)) {
      ({ problems, checks, binary } = await checkSetup(found, plugin));
      checkedFile = found;
    }
    polls++;
    const item = last ? summarize(last, problems) : unavailable(reason);
    publish();
    await orca.host.call('statusBar.update', { id: ITEM, ...item }).catch((err) => orca.log(`statusBar.update: ${err}`));
  };

  orca.panels.handle('view.get', () => current);

  // Each poll waits for the previous one, so a slow `hostslot status` never piles up.
  const loop = async () => {
    await refresh().catch((err) => orca.log(`refresh: ${err}`));
    setTimeout(loop, POLL_MS);
  };
  void loop();
}
