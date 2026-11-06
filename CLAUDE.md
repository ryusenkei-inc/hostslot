# CLAUDE.md

jobgate（重いジョブの実行をマシン全体で調整する Go の CLI）。構成・コマンドは [README.md](README.md)

## このリポの規約

- 公開の OSS: 社内の事情・個人名・秘密値を書かない
- 英語: README・issue・PR の本文・コードのコメント（共通の規約の「日本語」より優先）
- 本体: Go のモジュール。Node.js は開発の道具だけ（prose-lint・共通の規約・`scripts/`）
- テスト: Go の `testing`、対象と同じディレクトリに `*_test.go`（共通規約の Vitest の代わり）
- OS ごとの実装: `internal/platform` のビルドタグのファイルだけ。`internal/gate` は OS に依存しない
- 完了の条件: `go vet` を含む `pnpm lint` と、`go test ./...` を実行する `pnpm test` が通過
- 共通の規約: 下の import（`pnpm install` の実行後に有効）

@node_modules/@ryusenkei/conventions/rules.md
