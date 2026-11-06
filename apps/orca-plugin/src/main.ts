// Orca plugin worker: shows loadslot's machine capacity in the status bar.
// Orca runs it in a Node process while the plugin is enabled and transpiles
// this file itself, so it has no build step and no runtime dependencies.
import { execFile } from 'node:child_process';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { type Item, type Status, summarize, unavailable } from './status.ts';
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

/** Where loadslot is looked for when it is not on the PATH Orca was started with. */
export function candidates(env: NodeJS.ProcessEnv = process.env, home = homedir(), platform = process.platform): string[] {
  const exe = platform === 'win32' ? 'loadslot.exe' : 'loadslot';
  const list = [env.LOADSLOT_BIN, 'loadslot', join(home, '.local', 'bin', exe), join(home, 'go', 'bin', exe)];
  if (platform === 'win32' && env.LOCALAPPDATA) list.push(join(env.LOCALAPPDATA, 'Microsoft', 'WinGet', 'Links', exe));
  if (platform === 'darwin') list.push('/opt/homebrew/bin/loadslot', '/usr/local/bin/loadslot');
  return list.filter((p): p is string => Boolean(p));
}

function run(file: string, args: string[]): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(file, args, { timeout: TIMEOUT_MS, windowsHide: true }, (err, stdout) => (err ? reject(err) : resolve(stdout)));
  });
}

export default function activate(orca: OrcaWorkerApi): void {
  let found: string | undefined;
  let last: Status | undefined;
  let reason = 'loadslot has not been checked yet';
  let current: View = view(undefined, reason);

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
    const item = last ? summarize(last) : unavailable(reason);
    current = view(last, reason);
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
