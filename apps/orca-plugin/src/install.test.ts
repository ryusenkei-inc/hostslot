import { createHash } from 'node:crypto';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, it } from 'vitest';
import { assetName, dataDir, install, installedPath, offer, parseChecksums, setupItems } from './install.ts';

it('names the bare executable of each release as GoReleaser publishes it', () => {
  expect(assetName('0.5.0', 'win32', 'x64')).toBe('loadslot_0.5.0_windows_amd64.exe');
  expect(assetName('0.5.0', 'darwin', 'arm64')).toBe('loadslot_0.5.0_darwin_arm64');
  expect(assetName('0.5.0', 'linux', 'x64')).toBe('loadslot_0.5.0_linux_amd64');
  expect(assetName('0.5.0', 'freebsd', 'x64')).toBeUndefined();
  expect(assetName('0.5.0', 'linux', 'ia32')).toBeUndefined();
});

it('installs into the data directory the CLI uses', () => {
  expect(dataDir({ LOADSLOT_HOME: '/x' }, '/h', 'linux')).toBe('/x');
  expect(dataDir({ LOCALAPPDATA: 'C:/L' }, '/h', 'win32')).toBe(join('C:/L', 'loadslot'));
  expect(dataDir({}, '/h', 'darwin')).toBe(join('/h', 'Library', 'Application Support', 'loadslot'));
  expect(dataDir({}, '/h', 'linux')).toBe(join('/h', '.local', 'share', 'loadslot'));
  expect(dataDir({ XDG_DATA_HOME: '/d' }, '/h', 'linux')).toBe(join('/d', 'loadslot'));
  expect(installedPath({ LOCALAPPDATA: 'C:/L' }, '/h', 'win32')).toBe(join('C:/L', 'loadslot', 'bin', 'loadslot.exe'));
});

it('reads checksums.txt', () => {
  const a = 'a'.repeat(64);
  const sums = parseChecksums(`${a}  loadslot_0.5.0_linux_amd64\n\nnot a line\n${'b'.repeat(64)}  checksums.sig\n`);
  expect(sums.get('loadslot_0.5.0_linux_amd64')).toBe(a);
  expect(sums.size).toBe(2);
});

it('offers one button: install when loadslot is missing or older, else setup when doctor says so', () => {
  const base = { binary: '', plugin: '0.5.0', problems: [], target: '/t', platform: 'linux' as const, arch: 'x64' };
  expect(offer({ ...base, found: false })).toMatchObject({ request: 'install', label: 'Install loadslot 0.5.0' });
  expect(offer({ ...base, found: true, binary: '0.4.0' })).toMatchObject({ request: 'install', label: 'Update loadslot to 0.5.0' });
  expect(offer({ ...base, found: false })?.note).toContain('/t');
  expect(offer({ ...base, found: true, binary: '0.5.0' })).toBeUndefined();
  expect(offer({ ...base, found: false, platform: 'freebsd' })).toBeUndefined();
  expect(offer({ ...base, found: false, plugin: '' })).toBeUndefined();
  const problems = [{ level: 'warn' as const, text: 'claude: hook not registered', fix: 'setup' as const }];
  expect(offer({ ...base, found: true, binary: '0.5.0', problems })).toMatchObject({ request: 'setup' });
  // Installing runs setup too, so one button covers both.
  expect(offer({ ...base, found: true, binary: '0.4.0', problems })).toMatchObject({ request: 'install' });
});

it('turns setup --json into items, with the Codex trust step', () => {
  const out = JSON.stringify({
    agents: [
      { agent: 'claude', file: '/h/.claude/settings.json', result: 'unchanged' },
      { agent: 'codex', file: '/h/.codex/hooks.json', result: 'added' },
    ],
    retired: true,
  });
  expect(setupItems(out)).toEqual({
    items: [
      { label: 'Claude Code', value: 'already set up' },
      { label: 'Codex', value: 'hook added' },
      { label: 'Daemon', value: 'older one retired; the next run starts this version' },
    ],
    next: 'Open Codex and trust the loadslot hook with /hooks. Until then Codex skips it.',
  });
  expect(setupItems(JSON.stringify({ agents: [{ agent: 'claude', result: 'added' }], retired: false }))).toEqual({
    items: [{ label: 'Claude Code', value: 'hook added' }],
  });
  expect(setupItems('/h/.claude/settings.json: already up to date\n')).toEqual({
    items: [{ label: 'Setup', value: '/h/.claude/settings.json: already up to date' }],
  });
});

function fakeFetch(files: Record<string, Buffer | string>) {
  const urls: string[] = [];
  const get = async (url: string) => {
    urls.push(url);
    const body = files[url.slice(url.lastIndexOf('/') + 1)];
    return {
      ok: body !== undefined,
      status: body === undefined ? 404 : 200,
      arrayBuffer: async () => {
        const b = Buffer.from(body ?? '');
        return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength) as ArrayBuffer;
      },
      text: async () => String(body ?? ''),
    };
  };
  return { get, urls };
}

it('downloads the release, checks its SHA-256 and replaces the old binary', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'loadslot-install-'));
  const target = join(dir, 'bin', 'loadslot');
  const exe = Buffer.from('new binary');
  const name = 'loadslot_0.5.0_linux_amd64';
  const sum = createHash('sha256').update(exe).digest('hex');
  const { get, urls } = fakeFetch({ 'checksums.txt': `${sum}  ${name}\n`, [name]: exe });

  await install({ version: '0.5.0', target, platform: 'linux', arch: 'x64', fetch: get });
  expect(await readFile(target, 'utf8')).toBe('new binary');
  expect(urls).toEqual([
    'https://github.com/ryusenkei-inc/loadslot/releases/download/v0.5.0/checksums.txt',
    `https://github.com/ryusenkei-inc/loadslot/releases/download/v0.5.0/${name}`,
  ]);

  await writeFile(target, 'old binary');
  await install({ version: '0.5.0', target, platform: 'linux', arch: 'x64', fetch: get });
  expect(await readFile(target, 'utf8')).toBe('new binary');
});

it('refuses a download that does not match checksums.txt', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'loadslot-install-'));
  const target = join(dir, 'loadslot');
  const name = 'loadslot_0.5.0_linux_amd64';
  const { get } = fakeFetch({ 'checksums.txt': `${'0'.repeat(64)}  ${name}\n`, [name]: 'tampered' });
  await expect(install({ version: '0.5.0', target, platform: 'linux', arch: 'x64', fetch: get })).rejects.toThrow(/does not match/);
  await expect(readFile(target)).rejects.toThrow();

  const missing = fakeFetch({});
  await expect(install({ version: '0.5.0', target, platform: 'linux', arch: 'x64', fetch: missing.get })).rejects.toThrow(/not released on GitHub yet/);
});
