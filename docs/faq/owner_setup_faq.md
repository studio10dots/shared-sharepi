# オーナー向けセットアップ FAQ

`docs/SETUP.md` の手順(Cloud Shell でのコマンド)を実行したときに出るエラーと、その原因・対処です。
エラー文の原文(英語)は、そのまま載せています。

各項目の**確認状況**を見てください。「確認済み」は実際に起きて原因を確かめたもの、「推測」は未確認で、
断定していません。

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

## 8. アプリで管理者と表示されない / グループを作れない

**原因**: 管理者は、セットアップのコマンドを**実行した Google アカウント**で決まります。アプリでログインしているのと
別のアカウントで実行すると、アプリのアカウントは管理者として扱われません。

**対処**: アプリでログインしているアカウントで Cloud Shell を開き、**同じコマンドをもう一度**実行します。管理者が
入れ替わるだけで、写真のデータには触れません。

**確認状況**: **推測**(コードとテストから。実際の Cloud Shell と実機では、まだ再現していません)。

## 9. 「The billing account for the owning project is disabled in state absent」というエラーが出た

**原因**: このプロジェクトに、**課金アカウントが紐づいていません**(`absent` は、紐づけが無いという意味)。
課金アカウントを作ったことと、それをプロジェクトに紐づけることは、別の操作です。

**対処**: 紐づけてから、同じコマンドをもう一度実行します(最初のバケットの作成で止まるので、途中までの作成物はありません)。

```bash
gcloud billing projects describe <プロジェクトID>     # billingEnabled: false なら未紐づけ
gcloud billing accounts list                          # ACCOUNT_ID と OPEN を確認
gcloud billing projects link <プロジェクトID> --billing-account=<ACCOUNT_ID>
```

コマンドに入れたプロジェクト ID が、課金を設定したものと同じかも確認します(`gcloud projects list`)。

**確認状況**: 確認済み(2026-10)。

## 10. Terraform の「Follow the instructions at https://developer.hashicorp.com/terraform/install」という表示が出た

**原因**: Cloud Shell に、Terraform が入っていません(インストール方法を表示するだけのものが置かれていて、
表示したあと正常終了するため、気づかずに先へ進むことがあります)。

**対処**: セットアップのコマンドは、Terraform が無ければ、公式の配布元から取得します(チェックサムを確かめます)。
この表示が出る版は、古いものです。もう一度、最新のコマンドを実行してください。手で入れるなら、
上記の URL の手順に従います。

**確認状況**: 確認済み(2026-10。Cloud Shell で、表示のあとも処理が先へ進むことを確認)。取得する処理は、
テストで確認済みですが、実際の Cloud Shell では、まだ試していません。

## 11. 「data.google_client_openid_userinfo.me.email is null」というエラーが出た

```
Error: Invalid function argument
  on main.tf line 10, in locals:
  data.google_client_openid_userinfo.me.email is null
```

**原因**: Cloud Shell の認証情報に、メールアドレスが含まれていません。管理者は、実行したアカウントの
Google ユーザー ID で決まるので、メールは本来、必要ありませんが、古い版は、メールを必ず読もうとして止まっていました。

**対処**: 最新のコマンドを、もう一度実行します(メールが読めなくても、ユーザー ID が読めれば、そのまま進みます)。
ユーザー ID も読めないときは、「誰も管理者になれない」ことを知らせるエラーで止まります。そのときは、
アプリでログインしているアカウントの Cloud Shell で、もう一度実行してください。

**確認状況**: 確認済み(2026-10。Cloud Shell で再現)。修正後の動作は、テストと `terraform console` で確認済みですが、
実際の Cloud Shell では、まだ試していません。

## 12. 「Service Usage API has not been used in project ... before or it is disabled」(SERVICE_DISABLED)というエラーが出た

```
Error: Error when reading or editing Project Service : ... Error 403: Service Usage API has not been used in project ...
Error: Error creating Bucket: ... Cloud Logging API has not been used in project ...
```

**原因**: 新しいプロジェクトでは、Terraform が使う API(Service Usage、Cloud Logging など)が、まだ有効になっていません。
Terraform が API を有効にするには、その Service Usage API が、先に有効である必要があります。

**対処**: 最新のコマンドは、Terraform を動かす前に、必要な API を `gcloud` で有効にします。有効にした直後は、
数十秒〜数分のあいだ、このエラーが出ることがあるので、**最大 3 回、60 秒おきに自動でやり直します**。それでも
出るときは、数分待って、同じコマンドをもう一度実行してください。

手で有効にするなら、次のとおりです。

```bash
gcloud services enable serviceusage.googleapis.com cloudresourcemanager.googleapis.com \
  storage.googleapis.com logging.googleapis.com run.googleapis.com iam.googleapis.com \
  iamcredentials.googleapis.com --project=<プロジェクトID>
```

**確認状況**: 確認済み(2026-10。Cloud Shell で再現)。修正後の動作は、再試行の仕組みをテスト用の偽の応答で確認済みですが、
実際の Cloud Shell では、まだ試していません。
