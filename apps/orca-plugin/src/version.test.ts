import { expect, it } from 'vitest';
import { older } from './version.ts';

it('compares release versions', () => {
  expect(older('v0.3.0', '0.4.0')).toBe(true);
  expect(older('v0.4.0', '0.4.0')).toBe(false);
  expect(older('v1.0.0', '0.4.0')).toBe(false);
  expect(older('v0.4.0-rc.1', '0.4.0')).toBe(true);
});

it('counts a build after a release as newer', () => {
  expect(older('v0.4.1-0.20261106151733-91860db70b65', '0.4.0')).toBe(false);
});

it('never calls a development build older', () => {
  expect(older('(devel)', '0.4.0')).toBe(false);
  expect(older('v0.3.0', '')).toBe(false);
});
