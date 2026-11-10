// CLI for the root `.env` (shared implementation in `@ryusenkei/scripts`)
//   run [NAME=value ...] <cmd> [...args]  load `.env`, then run the command (NAME=value adds variables)
//   push [--dry-run]                      send `.env` values to GitHub Variables / Secrets
import { envCli } from '@ryusenkei/scripts';

await envCli({
  root: new URL('..', import.meta.url),
  // Targets per variable (an empty list keeps it local)
  //   Add a variable here when you add it to `.env.example`
  targets: {},
});
