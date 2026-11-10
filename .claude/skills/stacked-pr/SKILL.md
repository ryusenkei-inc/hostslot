---
name: stacked-pr
description: 前の PR のブランチから作った PR（スタックした PR）を、前の PR のマージの後に整理する手順。親の PR を squash でマージした後、子の PR のベースの付け替え、rebase、親のブランチの削除をするときに使う。epic のフローで dev のブランチを main へ squash でマージした後、epic を続けるときにも使う
---

# スタックした PR の整理

親の PR（`feat/a` → `main`）の上に、子の PR（`feat/b` → `feat/a`）を重ねている場合の手順。親を squash でマージすると、親のコミットは `main` 上の別のコミットになる。そのため、子のブランチには親の元のコミットが残り、そのままだと子の差分に親の変更が混ざる

## 手順

親の PR をマージした直後に、子の PR ごとに次を行う

1. 親の PR を squash でマージする。このとき `--delete-branch` は付けない。親のブランチは手順4で `rebase --onto` の基準に使う

   ```sh
   gh pr merge <親の番号> --squash
   ```

2. 子の PR のベースを、親のベース（`main`、epic のフローなら `dev/...`）に付け替える

   ```sh
   gh pr edit <子の番号> --base main
   ```

3. 最新のベースを取得する

   ```sh
   git fetch origin
   ```

4. 子のブランチから親の元のコミットを外し、ベースの上に載せ直す。`feat/a` は親のブランチの、マージの前の先端を指している必要がある（手元のブランチを消していなければそのまま使える）

   ```sh
   git rebase --onto origin/main feat/a feat/b
   ```

5. 子のブランチを push する。履歴を書き換えたので `--force-with-lease` を使う（`main` への force push はフックで止まる）

   ```sh
   git push --force-with-lease origin feat/b
   ```

6. 親のブランチを消す

   ```sh
   git branch -D feat/a
   git push origin --delete feat/a
   ```

## 多段のスタック

`feat/a` ← `feat/b` ← `feat/c` のように重ねている場合は、下から順に1段ずつ行う。`feat/b` を整理してマージしたら、`feat/c` について同じ手順を `feat/b` を親として繰り返す

## 確認

- 子の PR の差分に、子の変更だけが出ている（`gh pr diff <子の番号>`）
- 子の PR の CI が通っている（`gh pr checks <子の番号> --watch`）

## epic の dev のブランチを main へマージした後

epic のフローでは、デプロイのときに dev のブランチ（例: `dev/12-search`）を `main` へ squash でマージする。dev のブランチのコミットは `main` 上の1つのコミットになり、dev のブランチの履歴は `main` に残らない。epic がこれで終わるなら、追加の作業はない。デプロイの後も epic を続ける場合は、dev のブランチを親、開いている sub-issue の PR を子として、次を行う

1. マージの前に、dev のブランチの先端のコミットを控える。手順4で `rebase --onto` の基準に使う

   ```sh
   git fetch origin
   git rev-parse origin/dev/12-search
   ```

2. dev のブランチの PR を squash でマージする。リポの設定（マージの後にブランチを削除）で dev のブランチは消え、そこに向いていた sub-issue の PR のベースは GitHub が `main` に付け替える

   ```sh
   gh pr merge <dev の PR の番号> --squash
   ```

3. dev のブランチを最新の `main` から作り直して push する

   ```sh
   git fetch origin
   git switch -C dev/12-search origin/main
   git push -u origin dev/12-search
   ```

4. 開いている sub-issue の PR ごとに、ベースを dev のブランチに戻し、手順1のコミットより後のコミットだけを載せ直して push する。push していない手元の sub-issue のブランチも、同じ rebase で載せ直す

   ```sh
   gh pr edit <子の番号> --base dev/12-search
   git rebase --onto origin/dev/12-search <手順1のコミット> feat/b
   git push --force-with-lease origin feat/b
   ```

確認は上の「確認」と同じ。子の PR の差分に、すでに `main` に入った変更が出ていないことを見る
