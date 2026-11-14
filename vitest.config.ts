// Vitest: the default exclude plus the shared patterns (agents' worktrees under .claude/, .terraform/)
import { TEST_EXCLUDE } from '@ryusenkei/conventions';
import { configDefaults, defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    exclude: [...configDefaults.exclude, ...TEST_EXCLUDE],
  },
});
