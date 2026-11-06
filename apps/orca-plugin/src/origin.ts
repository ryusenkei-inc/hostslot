// Where a job comes from, in words a person recognizes: the repository and,
// for another worktree of it, the worktree's folder.
import type { Job } from './status.ts';

function base(path: string): string {
  return path.replace(/[\\/]+$/, '').split(/[\\/]/).at(-1) ?? path;
}

function parent(path: string): string {
  return path.replace(/[\\/]+$/, '').replace(/[\\/][^\\/]*$/, '');
}

/**
 * `repo`, `repo/sub/dir` for a job below the repository root, and
 * `repo (worktree)` when it runs in a linked worktree. The learning key
 * names the repository by its common git directory (shared by all
 * worktrees), so the main checkout's folder names it; the job's directory
 * names the worktree.
 */
export function origin(j: Pick<Job, 'key' | 'dir'>): string {
  const [head = '', rel = '.'] = (j.key ?? '').split('\n');
  if (!head.startsWith('git:')) return j.dir ? base(j.dir) : '';
  const common = head.slice('git:'.length);
  const repo = base(common) === '.git' ? base(parent(common)) : base(common).replace(/\.git$/, '');
  const where = rel === '.' ? repo : `${repo}/${rel}`;
  if (!j.dir) return where;
  // The worktree's top is the job's directory without the path inside it.
  let top = j.dir.replace(/[\\/]+$/, '');
  for (let n = rel === '.' ? 0 : rel.split('/').length; n > 0; n--) top = parent(top);
  const worktree = base(top);
  return worktree === repo ? where : `${where} (${worktree})`;
}
