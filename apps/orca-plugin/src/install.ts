// Installs the loadslot release that matches the plugin's version, for
// people who start from Orca without a terminal. The person starts it from
// the panel; nothing is written before that.
import { createHash } from 'node:crypto';
import { chmod, mkdir, rename, rm, writeFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import type { Problem } from './status.ts';
import { older } from './version.ts';
import type { SetupAction } from './view.ts';

const releases = 'https://github.com/ryusenkei-inc/loadslot/releases/download';
const DOWNLOAD_MS = 120_000;

/**
 * The bare executable of a release for an OS and CPU (see .goreleaser.yaml),
 * or undefined when there is none.
 */
export function assetName(version: string, platform: string = process.platform, arch: string = process.arch): string | undefined {
  const os = { win32: 'windows', darwin: 'darwin', linux: 'linux' }[platform as string];
  const cpu = { x64: 'amd64', arm64: 'arm64' }[arch as string];
  if (!os || !cpu) return undefined;
  return `loadslot_${version}_${os}_${cpu}${os === 'windows' ? '.exe' : ''}`;
}

/** loadslot's data directory, as the CLI resolves it (internal/platform/paths.go). */
export function dataDir(env: NodeJS.ProcessEnv = process.env, home = homedir(), platform = process.platform): string {
  if (env.LOADSLOT_HOME) return env.LOADSLOT_HOME;
  if (platform === 'win32') return join(env.LOCALAPPDATA || join(home, 'AppData', 'Local'), 'loadslot');
  if (platform === 'darwin') return join(home, 'Library', 'Application Support', 'loadslot');
  return join(env.XDG_DATA_HOME || join(home, '.local', 'share'), 'loadslot');
}

/**
 * Where the plugin installs loadslot: a `bin` folder in its data directory,
 * apart from copies people installed themselves.
 */
export function installedPath(env: NodeJS.ProcessEnv = process.env, home = homedir(), platform = process.platform): string {
  return join(dataDir(env, home, platform), 'bin', platform === 'win32' ? 'loadslot.exe' : 'loadslot');
}

/** The SHA-256 of each file in a GoReleaser `checksums.txt`. */
export function parseChecksums(text: string): Map<string, string> {
  const sums = new Map<string, string>();
  for (const line of text.split('\n')) {
    const [sum, name] = line.trim().split(/\s+/);
    if (sum && name && /^[0-9a-f]{64}$/.test(sum)) sums.set(name, sum);
  }
  return sums;
}

/**
 * The one button to offer: installing the plugin's own version when loadslot
 * is missing or older (and a release exists for this OS), which also runs
 * setup; otherwise setup when `doctor` says it fixes a problem.
 */
export function offer(o: {
  found: boolean;
  binary: string;
  plugin: string;
  problems: Problem[];
  target: string;
  platform?: NodeJS.Platform;
  arch?: string;
}): SetupAction | undefined {
  const outdated = o.found && o.binary !== '' && older(o.binary, o.plugin);
  if (o.plugin && assetName(o.plugin, o.platform, o.arch) && (!o.found || outdated)) {
    return {
      request: 'install',
      label: outdated ? `Update loadslot to ${o.plugin}` : `Install loadslot ${o.plugin}`,
      note: `Downloads it from GitHub to ${o.target}, checks it against the release's checksums, then connects your agents.`,
    };
  }
  if (o.found && o.problems.some((p) => p.fix === 'setup')) {
    return { request: 'setup', label: 'Fix with loadslot setup', note: 'Connects your agents and replaces an older daemon.' };
  }
  return undefined;
}

const agentNames: Record<string, string> = { claude: 'Claude Code', codex: 'Codex' };
const setupResults: Record<string, string> = {
  unchanged: 'already set up',
  added: 'hook added',
  updated: 'hook updated',
  removed: 'hook removed',
};

/**
 * The items of `loadslot setup --json` output, and the step left to the
 * person: Codex runs a new or changed hook only after it is trusted.
 * Output that isn't JSON (an older loadslot) becomes one item.
 */
export function setupItems(out: string): { items: { label: string; value: string }[]; next?: string } {
  let report: { agents?: { agent: string; result: string }[]; retired?: boolean };
  try {
    report = JSON.parse(out);
  } catch {
    return { items: [{ label: 'Setup', value: out.trim() || 'finished' }] };
  }
  const agents = report.agents ?? [];
  const items = agents.map((a) => ({ label: agentNames[a.agent] ?? a.agent, value: setupResults[a.result] ?? a.result }));
  if (items.length === 0) items.push({ label: 'Agents', value: 'none found (Claude Code, Codex)' });
  if (report.retired) items.push({ label: 'Daemon', value: 'older one retired; the next run starts this version' });
  const codexChanged = agents.some((a) => a.agent === 'codex' && (a.result === 'added' || a.result === 'updated'));
  return codexChanged ? { items, next: 'Open Codex and trust the loadslot hook with /hooks. Until then Codex skips it.' } : { items };
}

type Fetch = (url: string) => Promise<{ ok: boolean; status: number; arrayBuffer(): Promise<ArrayBuffer>; text(): Promise<string> }>;

export type InstallOptions = {
  version: string;
  target: string;
  platform?: NodeJS.Platform;
  arch?: string;
  fetch?: Fetch;
};

/**
 * Downloads loadslot `version` for this OS and CPU from GitHub Releases,
 * checks it against the release's `checksums.txt`, and puts it at `target`.
 * A running loadslot keeps its file open on Windows, so an existing one is
 * moved aside first (its daemon goes on until it is retired).
 */
export async function install(o: InstallOptions): Promise<string> {
  const get = o.fetch ?? ((url: string) => fetch(url, { signal: AbortSignal.timeout(DOWNLOAD_MS) }));
  const name = assetName(o.version, o.platform, o.arch);
  if (!name) throw new Error(`no loadslot release for ${o.platform ?? process.platform}/${o.arch ?? process.arch}`);
  const base = `${releases}/v${o.version}`;

  const sums = await get(`${base}/checksums.txt`);
  if (sums.status === 404) {
    // A tag's release takes a few minutes to build, so a plugin update can come first.
    throw new Error(`loadslot ${o.version} is not released on GitHub yet. Try again in a few minutes.`);
  }
  if (!sums.ok) throw new Error(`checksums.txt of v${o.version}: HTTP ${sums.status}`);
  const expected = parseChecksums(await sums.text()).get(name);
  if (!expected) throw new Error(`checksums.txt of v${o.version} has no ${name}`);

  const res = await get(`${base}/${name}`);
  if (!res.ok) throw new Error(`${name}: HTTP ${res.status}`);
  const bytes = Buffer.from(await res.arrayBuffer());
  const actual = createHash('sha256').update(bytes).digest('hex');
  if (actual !== expected) throw new Error(`${name} does not match checksums.txt; not installed`);

  await mkdir(join(o.target, '..'), { recursive: true });
  const fresh = `${o.target}.new`;
  await writeFile(fresh, bytes, { mode: 0o755 });
  await chmod(fresh, 0o755);
  // Moving a running executable aside works on every OS; replacing it in
  // place fails on Windows.
  const aside = `${o.target}.old-${Date.now()}`;
  await rename(o.target, aside).catch(() => undefined);
  await rename(fresh, o.target);
  await rm(aside, { force: true }).catch(() => undefined);
  return o.target;
}
