import { join } from 'node:path';
import { expect, it } from 'vitest';
import { candidates } from './main.ts';

it('looks for the copy the plugin installed, then the PATH, then where installers put it', () => {
  const list = candidates({ LOADSLOT_BIN: '/x/loadslot', LOCALAPPDATA: 'C:/L' }, '/h', 'win32');
  expect(list).toEqual([
    '/x/loadslot',
    join('C:/L', 'loadslot', 'bin', 'loadslot.exe'),
    'loadslot',
    join('/h', '.local', 'bin', 'loadslot.exe'),
    join('/h', 'go', 'bin', 'loadslot.exe'),
    join('C:/L', 'Microsoft', 'WinGet', 'Links', 'loadslot.exe'),
  ]);
  expect(candidates({}, '/h', 'darwin')).toContain('/opt/homebrew/bin/loadslot');
});
