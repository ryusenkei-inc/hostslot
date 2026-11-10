# CLAUDE.md

Hostslot（並列のエージェントが1台のマシンで取り合う資源のスケジューラーの CLI と、その Orca のプラグイン）。構成・コマンドは [README.md](README.md)

- 表記: 文章・UI の製品名は Hostslot、コマンド・コード・パス・パッケージは `hostslot`

## このリポの規約

- 公開の OSS（MIT）: 社内の事情・個人名・秘密値を書かない
- 外部への発信（Orca のリポへの issue・PR・コメント、Discussions、プラグインの一覧への掲載）: 実行しない。`#44` に下書きを置き、項目ごとの明示的な承認の後に限り実行
- 英語: README・issue・PR のタイトルと本文・コミットメッセージ・コードのコメント（共通の規約の「日本語」より優先）
  - prose-lint の `commit/subject-ja`: `prose-lint.json` で無効化
- 構成: ルートに CLI の Go のモジュール、`apps/*` に TypeScript のパッケージ（pnpm のワークスペース）
  - Go のモジュールはルートから移動しない（`go install …/cmd/hostslot@latest` と `v*` のタグを維持）
- Orca のプラグイン: マニフェストはリポの直下の `orca-plugin.json`、コードは `apps/orca-plugin`
  - 実行時の依存なし（Orca が TypeScript をそのまま読み込む）
  - リリース: `orca-plugin.json` の `version` をタグ（`vX.Y.Z`）と同じ値に更新
- テスト: 対象と同じディレクトリ
  - Go: `testing` の `*_test.go`
  - TypeScript: Vitest の `*.test.ts`
- OS ごとの実装: `internal/platform` のビルドタグのファイルだけ。`internal/gate` は OS に依存しない
- 完了の条件: `go vet` を含む `pnpm lint` と、`go test ./...`・Vitest を実行する `pnpm test` が通過
- 共通の規約: 下の import（`pnpm install` の実行後に有効）

@node_modules/@ryusenkei/conventions/rules.md
@node_modules/@ryusenkei/conventions/rules-ts.md
@node_modules/@ryusenkei/conventions/rules-go.md
