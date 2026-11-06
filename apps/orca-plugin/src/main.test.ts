import { join } from 'node:path';
import { expect, it } from 'vitest';
import { candidates } from './main.ts';

it('looks for the copy the plugin installed, then the PATH, then where installers put it', () => {
  const list = candidates({ HOSTSLOT_BIN: '/x/hostslot', LOCALAPPDATA: 'C:/L' }, '/h', 'win32');
  expect(list).toEqual([
    '/x/hostslot',
    join('C:/L', 'hostslot', 'bin', 'hostslot.exe'),
    'hostslot',
    join('/h', '.local', 'bin', 'hostslot.exe'),
    join('/h', 'go', 'bin', 'hostslot.exe'),
    join('C:/L', 'Microsoft', 'WinGet', 'Links', 'hostslot.exe'),
  ]);
  expect(candidates({}, '/h', 'darwin')).toContain('/opt/homebrew/bin/hostslot');
});
