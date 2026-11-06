import { execFileSync } from 'node:child_process';

// 作業ツリーの GitHub のリポ（`owner/name`）
//   `gh` が git の remote から解決 → リポ名をコードに持たない（テンプレートから作成したリポでもそのまま動作）
export const currentRepo = () => execFileSync('gh', ['repo', 'view', '--json', 'nameWithOwner', '-q', '.nameWithOwner'], { encoding: 'utf8' }).trim();
