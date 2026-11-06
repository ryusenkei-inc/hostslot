import { expect, it } from 'vitest';
import { origin } from './origin.ts';

it('names the repository from its common git directory', () => {
  expect(origin({ key: 'git:C:/dev/shop/.git\n.\npnpm test', dir: 'C:\\dev\\shop' })).toBe('shop');
  expect(origin({ key: 'git:/home/a/shop/.git\napps/site\npnpm test', dir: '/home/a/shop/apps/site' })).toBe('shop/apps/site');
});

it('adds the worktree folder for a linked worktree', () => {
  expect(origin({ key: 'git:C:/dev/shop/.git\n.\npnpm test', dir: 'C:\\dev\\worktrees\\fix-cart' })).toBe('shop (fix-cart)');
  expect(origin({ key: 'git:/home/a/shop/.git\napps/site\npnpm test', dir: '/w/feature-x/apps/site' })).toBe('shop/apps/site (feature-x)');
});

it('falls back to the folder outside a repository', () => {
  expect(origin({ key: 'dir:/tmp/scratch\nmake', dir: '/tmp/scratch' })).toBe('scratch');
  expect(origin({ dir: '/tmp/scratch' })).toBe('scratch');
  expect(origin({})).toBe('');
});
