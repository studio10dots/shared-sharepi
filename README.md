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

## ライセンス / License

ソースは閲覧と監査のために公開されていますが、オープンソースではありません。
オーナーが無改変のまま自分の環境で運用することだけを認めます。詳細は [LICENSE](LICENSE) を参照してください。

Source-available, not open source. You may read it and run the unmodified release in your own project. See [LICENSE](LICENSE).

© 2026 Kenji Koikeda (studio10dots)
