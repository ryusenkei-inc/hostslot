// GitHub のリポの設定（テンプレートから作成したリポには引き継がれない → 作成後に1回実行）
//   node scripts/repo.ts [owner/name]  省略時は作業ツリーのリポ
// - マージ: squash のみ → main の履歴 = PR のタイトル（CI が書式を検査）
// - squash のコミット: タイトル = PR のタイトル・本文は空（コミットに body を書かない規約）
// - マージ後にブランチを削除
import { exec } from './lib/exec.ts';
import { currentRepo } from './lib/repo.ts';

const SETTINGS = {
  allow_squash_merge: 'true',
  allow_merge_commit: 'false',
  allow_rebase_merge: 'false',
  squash_merge_commit_title: 'PR_TITLE',
  squash_merge_commit_message: 'BLANK',
  delete_branch_on_merge: 'true',
};

const repo = process.argv[2] ?? currentRepo();
// 真偽値は `-F`（型付き）、文字列は `-f`
const fields = Object.entries(SETTINGS).flatMap(([key, value]) => [/^(true|false)$/.test(value) ? '-F' : '-f', `${key}=${value}`]);
process.exitCode = exec('gh', ['api', '-X', 'PATCH', `repos/${repo}`, ...fields, '--jq', '{name, allow_squash_merge, allow_merge_commit, allow_rebase_merge, squash_merge_commit_title, squash_merge_commit_message, delete_branch_on_merge}']);
