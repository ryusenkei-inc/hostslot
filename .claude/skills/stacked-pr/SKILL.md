---
name: stacked-pr
description: 前の PR のブランチから作った PR（スタックした PR）を、前の PR のマージの後に整理する手順。親の PR を squash でマージした後、子の PR のベースの付け替え、rebase、親のブランチの削除をするときに使う
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
