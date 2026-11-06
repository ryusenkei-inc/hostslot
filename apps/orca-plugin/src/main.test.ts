import { join } from 'node:path';
import { expect, it } from 'vitest';
import { candidates } from './main.ts';

it('looks for loadslot on the PATH, then where installers put it', () => {
  const list = candidates({ LOADSLOT_BIN: '/x/loadslot', LOCALAPPDATA: 'C:/L' }, '/h', 'win32');
  expect(list).toEqual([
    '/x/loadslot',
    'loadslot',
    join('/h', '.local', 'bin', 'loadslot.exe'),
    join('/h', 'go', 'bin', 'loadslot.exe'),
    join('C:/L', 'Microsoft', 'WinGet', 'Links', 'loadslot.exe'),
  ]);
  expect(candidates({}, '/h', 'darwin')).toContain('/opt/homebrew/bin/loadslot');
});
