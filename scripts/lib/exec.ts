import type { SpawnSyncOptions } from 'node:child_process';
import spawn from 'cross-spawn';

// Windows の .cmd（pnpm・npx など）も引数を加工せずに指定
//   シェルの引用は cross-spawn が処理
export function exec(command: string, args: string[], options: SpawnSyncOptions = {}): number {
  const { status } = spawn.sync(command, args, { stdio: 'inherit', ...options });
  return status ?? 1;
}
