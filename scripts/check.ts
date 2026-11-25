// Pre-push check: type checks and tests for the change, plus this repository's fast checks.
// Run the whole repository with `pnpm check --all` (before a release).
import { runAffectedCheck } from '@ryusenkei/conventions';

const results = await runAffectedCheck({
  steps: () => [
    { name: 'prose-lint', command: 'pnpm', args: ['exec', 'prose-lint'] },
    { name: 'deps', command: 'node', args: ['scripts/deps.ts'] },
    { name: 'conventions', command: 'pnpm', args: ['exec', 'ryusenkei-conventions', 'check'] },
    { name: 'go vet', command: 'go', args: ['vet', './...'] },
    { name: 'go test', command: 'go', args: ['test', './...'] },
  ],
});
process.exitCode = results.every((r) => r.ok) ? 0 : 1;
