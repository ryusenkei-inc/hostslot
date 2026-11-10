import type { Exception, Rule } from '@ryusenkei/conventions';

// Layer rules for `runDepsCheck` (`pnpm lint:deps`)
//   The Orca plugin has no runtime dependencies (Orca loads its TypeScript as is)
export const RULES: Rule[] = [
  { name: 'orca-plugin', paths: ['apps/orca-plugin'], allow: [] },
  { name: 'scripts', paths: ['scripts'], allow: [] },
];
export const EXCEPTIONS: Exception[] = [];
