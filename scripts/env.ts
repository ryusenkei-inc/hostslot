// ルート `.env` の操作 CLI
//   run [NAME=value ...] <cmd> [...args]  .env ロード後にコマンド実行（NAME=value は追加の環境変数）
//   push [--dry-run]     .env → GitHub Variables / GitHub Secrets へ配信
import type { SpawnSyncOptions } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { parseEnv } from 'node:util';
import { exec } from './lib/exec.ts';
import { currentRepo } from './lib/repo.ts';

type Target = 'github:variable' | 'github:secret';

// 変数ごとの配信先（配信しない変数は空配列）
//   `.env.example` に変数を追加したらここにも定義
const TARGETS: Record<string, Target[]> = {};

const envPath = new URL('../.env', import.meta.url);

function readEnv(): Record<string, string | undefined> {
  if (!existsSync(envPath)) {
    console.error('.env 未作成（.env.example を複製して値を設定）');
    process.exit(1);
  }
  return parseEnv(readFileSync(envPath, 'utf8'));
}

// 先頭の NAME=value は環境変数として受け渡し（Windows の cmd は `NAME=value cmd` の書き方を解釈しない）
async function run(args: string[]): Promise<number> {
  const start = args.findIndex((a) => !/^[A-Z_][A-Z0-9_]*=/.test(a));
  const [command, ...rest] = start === -1 ? [] : args.slice(start);
  if (!command) {
    console.error('usage: node scripts/env.ts run [NAME=value ...] <command> [...args]');
    return 1;
  }
  Object.assign(process.env, readEnv());
  for (const assign of args.slice(0, start)) {
    const [name, ...value] = assign.split('=');
    process.env[name!] = value.join('=');
  }
  return exec(command, rest);
}

// 値は stdin 経由で受け渡し（プロセス引数への露出回避）
function pushTo(repo: string, target: Target, key: string, value: string): number {
  const options: SpawnSyncOptions = { input: value, stdio: ['pipe', 'inherit', 'inherit'] };
  return exec('gh', [target === 'github:variable' ? 'variable' : 'secret', 'set', key, '--repo', repo], options);
}

async function push(args: string[]): Promise<number> {
  const dryRun = args.includes('--dry-run');
  // 配信先 = 作業ツリーのリポ
  const repo = dryRun ? '' : currentRepo();
  const values = readEnv();
  for (const key of Object.keys(values).filter((k) => !(k in TARGETS))) {
    console.warn(`skip  ${key}: 配信先未定義（TARGETS に追加）`);
  }
  let failed = false;
  for (const [key, targets] of Object.entries(TARGETS)) {
    const value = values[key];
    if (!value) {
      console.warn(`skip  ${key}: 値未設定`);
      continue;
    }
    for (const target of targets) {
      console.log(`${dryRun ? 'plan' : 'push'}  ${key} → ${target}`);
      if (!dryRun && pushTo(repo, target, key, value) !== 0) failed = true;
    }
  }
  return failed ? 1 : 0;
}

const [subcommand, ...rest] = process.argv.slice(2);
const commands: Record<string, (args: string[]) => Promise<number>> = { run, push };
const handler = subcommand ? commands[subcommand] : undefined;
if (!handler) {
  console.error('usage: node scripts/env.ts <run|push> ...');
  process.exit(1);
}
process.exit(await handler(rest));
