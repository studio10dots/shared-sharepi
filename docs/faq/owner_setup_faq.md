# オーナー向けセットアップ FAQ(元資料)

アプリ内のセットアップガイド(`SetupGuidePage`)と、アプリ紹介ページ(`landing/index.html` の FAQ)で
使う文章の**元資料**です。ここを正として、各所へ転記します(下の「使い方」)。

## 使い方

- **アプリ**: `lib/l10n/app_*.arb` の7言語に文字列として入れる(Agents.md section 8: 文言は ARB のみ)。
  エラー文の原文(英語)はそのまま表示される文字列なので、翻訳せず原文のまま載せる。
- **紹介ページ**: `landing/index.html` の `.faq` に `<details class="faq-item">` として追加する。
  紹介ページ向けは「紹介ページ用」の短い版を使い、エラー文の細部は載せない。
- 各項目の**確認状況**を必ず見ること。「確認済み」は実際に起きて原因を確かめたもの、「推測」は未確認。
  推測のものは、断定せずに書く。
- セットアップの手順やエラー対応が変わったら、ここを先に直す(Agents.md section 14)。

---

## 1. 組織(会社の Google Workspace など)のアカウントでも使えますか?

**紹介ページ用(短い版)**
使えます。会社などの組織アカウントでも、オーナーの Google Cloud にバックエンドを作れます。
ただし、組織の管理者が設定した「組織ポリシー」によっては、作成が止まることがあります
(下の項目 2〜3)。その場合は、組織の管理者に例外の設定をお願いするか、個人の Google アカウントの
プロジェクトで作成してください。個人のプロジェクトには組織ポリシーがありません。

**確認状況**: 確認済み(2026-10、組織配下のプロジェクトで全8リージョンの作成・HTTPS・削除まで成功)。

## 2. 「users named in the policy do not belong to a permitted customer」というエラーが出た

**エラー原文**
```
Error 400: One or more users named in the policy do not belong to a permitted customer,
perhaps due to an organization policy.
```

**原因**: 組織ポリシー「ドメイン制限共有」(`iam.allowedPolicyMemberDomains`)が、自社ドメイン以外への
権限付与を禁止しています。バックエンドを「誰でも呼び出せる」公開設定にするために `allUsers` へ
権限を付けようとして、これに止められます。

**対処**: 現在のバックエンド用 Terraform は `allUsers` を使わず、Cloud Run の「呼び出し元の
IAM チェックを無効化」(`invoker_iam_disabled`)で公開するため、**このエラーは出なくなりました**。
認証はバックエンド自身が Google の ID トークンで行うので、安全性は変わりません。
古い版の Terraform で出た場合は、リポジトリを最新にして `terraform apply` をやり直します。

**確認状況**: 確認済み(2026-10、組織ポリシーの許可値が自社ドメインのみのプロジェクトで再現し、
`invoker_iam_disabled` に変更後は成功)。

## 3. 他の組織ポリシーで止まった(例: リージョン制限、呼び出し元 IAM の強制)

**症状の例**: エラー文に `Organization Policy` や `constraints/...` が含まれる。

| 制約(例) | 起きること | 対処 |
| --- | --- | --- |
| `gcp.resourceLocations`(リソースの場所の制限) | 許可されていない地域を選ぶと作成に失敗する | 組織が許可している地域のコードを選ぶ |
| `run.requireInvokerIam`(呼び出し元 IAM の強制) | 公開設定(上の項目 2 の方式)が拒否される | 管理者に、このプロジェクトだけ例外を設定してもらう |

いずれも組織の管理者の設定です。オーナー側では回避できないので、管理者への依頼か、個人の
プロジェクトの利用になります。

**確認状況**: **推測**(制約名と挙動は Google の仕様から書いたもので、実際には発生を確認していない)。
載せる前に、実際に起きたエラー文で裏づけること。

## 4. 「Project failed to initialize in this region due to quota exceeded」というエラーが出た

**エラー原文**
```
Error waiting to create Service: Error code 13, message: Project failed to initialize in this region
due to quota exceeded.
```

**意味**: Cloud Run を、そのプロジェクトがそのリージョンで使い始めるための枠(クォータ)が足りません。
CPU やメモリの指定が原因ではありません(同じ指定が他のリージョンでは成功しています)。

**対処**
1. 別の地域(セットアップガイドのプルダウン)を選び直す。
2. どうしてもその地域を使いたい場合は、Google Cloud コンソールの「IAM と管理 → 割り当てとシステム上限」で
   Cloud Run のクォータの引き上げを申請する。

**確認状況**: 現象は確認済み、**原因は推測**。新規の個人プロジェクトで europe-west1 / us-central1 /
southamerica-east1 の3つだけで再現し(他の5地域は成功)、組織プロジェクトでは同じ3つが成功した。
新規の個人プロジェクトや課金アカウントは、需要の大きいリージョンの初期枠が小さいことがある、と
考えているが、クォータの実際の値は取得できていない。

## 5. 「the user does not have permission to access Project ... or it may not exist」というエラーが出た

**原因**: 次のどれかです。
- プロジェクトの**名前や番号**を入れている(必要なのは「プロジェクト ID」)。
- ログインしている Google アカウントに、そのプロジェクトの権限がない。
- ID の打ち間違い。

**対処**: コンソールのプロジェクト選択で、**ID** の列を確認して入れ直す。Cloud Shell の場合は、
上部に表示されているアカウントが、そのプロジェクトの持ち主であることを確認する。

**確認状況**: 確認済み(存在しない ID を渡して再現)。

## 6. 「The specified bucket does not exist」(バケットの IAM 設定で 404)が出た

**エラー原文**
```
Error setting IAM policy for storage bucket ...: googleapi: Error 404: The specified bucket does not exist.
```

**原因**: バケットを作った直後に、そのバケットへ権限を付ける処理が、まだバケットが見えない状態で
走ってしまう一時的な不整合です。同じ名前のバケットを消してすぐ作り直したときに起きました。

**対処**: もう一度 `terraform apply` を実行する(途中から続きが実行されます)。

**確認状況**: 現象は確認済み、原因は**推測**(名前を毎回変えて作るようにしたところ再現しなくなった。
ただし通常のオーナーは1回だけ作るので、実際に起きるかは不明)。

## 7. 「cannot destroy service without setting deletion_protection=false」というエラーが出た(削除したいとき)

**原因**: 誤って消さないように、Cloud Run サービスには**削除保護**が付いています(既定で有効)。

**対処**: 先に保護を外してから消します。値は保存済みの状態から読まれるため、`destroy` だけに
`false` を付けても効きません。

```bash
terraform apply -var="deletion_protection=false" ...   # これまでと同じ -var も付ける
terraform destroy ...
```

**確認状況**: 確認済み(2026-10)。
