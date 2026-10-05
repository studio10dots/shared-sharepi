# SharePi backend

SharePi は、友人や知人とだけ写真・動画を共有するためのアプリです。写真はオーナー自身の Google Cloud (GCS) に保存されます。
このリポジトリは、そのサーバー側の資材です。アプリ本体 (Flutter) のコードは含みません。

SharePi is a private photo-sharing app. Photos live in the owner's own Google Cloud project.
This repository holds only the server-side materials; the app itself is not included.

## 中身 / Contents

| パス | 内容 |
|---|---|
| `backend/` | Cloud Run で動く小さなバックエンド (Go)。写真のバイト列は通さず、署名付き URL を発行します |
| `terraform/backend/` | オーナーが Cloud Shell で実行する Terraform。バケット、サービスアカウント、Cloud Run を作ります |
| `docs/SETUP.md` | オーナー向けのセットアップ手順 |
| `docs/BACKEND_DESIGN.md` | バックエンドの設計 (API、データ、認可) |
| `docs/SECURITY.md` | セキュリティの考え方 |
| `docs/faq/` | セットアップ中のエラーと対処 |

## 使い方 / Setup

手順は [docs/SETUP.md](docs/SETUP.md) を参照してください。コマンドはそこに書かれたものを使い、このファイルには複製しません。
Follow [docs/SETUP.md](docs/SETUP.md); the commands are kept only there.

## コンテナイメージ / Container image

`ghcr.io/studio10dots/chamagon-backend:<version>` として公開されます。リリースは `backend-vX.Y.Z` タグで行い、
動作中のバックエンドは、このリポジトリの最新タグと自分のバージョンを比べて、更新があればオーナーに知らせます。

### `chamagon` という名前について / About the name "chamagon"

プロジェクトの初期の名前が `chamagon` で、いまの名前は SharePi です。次の 2 か所には、`chamagon` が、愛称として残っています。

- コンテナイメージ (パッケージ) の名前: `chamagon-backend`、`chamagon-web`
- 招待コードの先頭の文字列: `chamagon1.`

どちらも、オーナーやメンバーの操作には、影響しません。名前を変えると、新しいパッケージを公開し直す手間と、
アプリとバックエンドが両方の接頭辞を受け付ける移行期間が必要になるため、そのままにしています。

The project was first called `chamagon`; it is SharePi now. The old name stays, as a nickname, in the container
image (package) names (`chamagon-backend`, `chamagon-web`) and in the prefix of invitation strings (`chamagon1.`).
Neither affects how owners or members use the app, and renaming them would need new packages and a period in which
the app and the backend accept both prefixes, so they are left as they are.

## ライセンス / License

ソースは閲覧と監査のために公開されていますが、オープンソースではありません。
オーナーが無改変のまま自分の環境で運用することだけを認めます。詳細は [LICENSE](LICENSE) を参照してください。

Source-available, not open source. You may read it and run the unmodified release in your own project. See [LICENSE](LICENSE).

## 免責 / Disclaimer

この資材は現状のまま提供されます。提供者は、これらの利用または利用できなかったことに関連して生じた、いかなるデータの損傷・消失・流出、
およびこれに伴う逸失利益その他一切の損害について、法令が許す最大限の範囲で責任を負いません。
保守・更新・公開の継続の義務もなく、いつでも変更・中止することがあります。
ご自身の Google Cloud の費用、セキュリティ、データは、オーナーの責任です。詳細は [LICENSE](LICENSE) の第4条を参照してください。

These materials are provided "as is". To the maximum extent permitted by law, the author is not liable for any damage to,
or loss or disclosure of, data, or any resulting lost profits or other loss, arising from their use or inability to use them.
There is no obligation to maintain, update or keep publishing them, and they may change or stop at any time.
Costs, security and data of your own Google Cloud project are your responsibility. See section 4 of [LICENSE](LICENSE).

© 2026 Kenji Koikeda (studio10dots)
