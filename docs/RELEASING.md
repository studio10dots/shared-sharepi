# リリースの流れ(公開者向け)

オーナーに新しいバックエンドを届ける手順です。**人がやるのは、タグを打つことと、できた PR をマージすることだけ**です。

## 手順

1. タグを打つ(または `main` の `backend/` を変更してマージする。後者はパッチ番号が自動で上がります)。

   ```bash
   git tag backend-v1.2.0 && git push origin backend-v1.2.0
   ```

2. ワークフロー `Release backend` が自動で行う。
   - テスト → GCP 用コンテナイメージのビルドと公開(`ghcr.io/<owner>/chamagon-backend:<版>`)
   - AWS 用プログラムのビルド(Linux x86-64、zip の中のファイル名は `bootstrap`)と、同じタグの GitHub Release への添付(`bootstrap-linux-amd64.zip`)
   - ボットが、次の2か所を新しい値に書き換える PR を作る。
     - `terraform/backend/publisher.auto.tfvars` の `backend_image`
     - `terraform/backend-aws/program.env` の `SHAREPI_BACKEND_URL` と `SHAREPI_BACKEND_SHA256`
3. **その PR をマージする。** オーナーが実行する `setup.sh` は既定ブランチを取得するので、**PR をマージして初めて、オーナーに新しい版が届きます**。タグを打った時点のコミットの `program.env` は古い値のままです。

## 守っていること

- zip の SHA-256 は、Release には添えません。`setup.sh` は、リポジトリの `program.env` の値と照合します。Release の zip だけが差し替えられても、検出できます(zip と同じ場所に値を置くと、両方が変わってしまうため)。
- ボットの PR が触るファイルは、新たなリリースを起動しません(`publisher.auto.tfvars` は除外、`terraform/backend-aws/` は監視対象外)。

## 最初に一度だけ(リポジトリの設定)

- Settings → Actions → General → **Allow GitHub Actions to create and approve pull requests** をオンにする。オフだと、リリースは成功しても、PR の作成だけが失敗します(その場合は `pointers` ジョブを再実行)。
- コンテナイメージのパッケージは公開にしておく(Cloud Run が匿名で取得します)。

## 動作確認(何も公開しない)

Actions → Release backend → Run workflow で、`dry_run` をオンにして実行する。イメージはビルドだけ(公開しない)、zip をビルドして SHA-256 をログに出し、そこで止まります。タグ・Release・PR は作られません。
