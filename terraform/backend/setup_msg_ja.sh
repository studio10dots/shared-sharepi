# Japanese words of setup.sh's screen. See setup_msg_en.sh.

ui_fmt_ja() {
  case "$1" in
    intro) printf '%s' "SharePi のバックエンドをセットアップします。詳しい内容は次のログに書き込みます:" ;;
    step_project) printf '%s' "プロジェクトを確認しています" ;;
    step_terraform) printf '%s' "Terraform を準備しています" ;;
    step_apis) printf '%s' "必要な Google Cloud のサービスを有効にしています" ;;
    step_state) printf '%s' "セットアップの状態を保存する場所を準備しています" ;;
    step_admin) printf '%s' "管理者を確認しています" ;;
    step_init) printf '%s' "Terraform を初期化しています" ;;
    step_apply) printf '%s' "バックエンドを作成しています(数分かかります)" ;;
    step_result) printf '%s' "結果を読み取っています" ;;
    ok) printf '%s' "完了" ;;
    failed) printf '%s' "失敗" ;;
    created) printf '%s' "作成しました" ;;
    already_there) printf '%s' "すでにあります" ;;
    admin_account) printf '%s' "あなたの Google アカウント" ;;
    admin_no_id) printf '%s' "Google のユーザー ID を読み取れなかったため、メールアドレスで判定します" ;;
    admin_no_id_note) printf '%s' "      あとでこのコマンドをもう一度実行すると、ユーザー ID で判定するようになります。\n      または admin_subs を指定します(terraform.tfvars.example を参照)。" ;;
    retrying) printf '%s' " (サービスの準備がまだ終わっていません。60 秒後にやり直します) " ;;
    what_happened) printf '%s' "何が起きたか" ;;
    what_to_do) printf '%s' "次にやること" ;;
    last_lines) printf '%s' "このステップが最後に出力した内容:" ;;
    full_log) printf '%s' "詳しいログ: %s" ;;
    final_admin) printf '%s' "アプリには、この Cloud Shell と同じ Google アカウントでログインしてください。\nそのアカウントが管理者(判定方法: %s)で、グループの作成と招待ができます。" ;;
    final_log) printf '%s' "詳細のログは以下パスです。" ;;
    final_created) printf '%s' "バックエンドを作成しました。以下のURLをモバイルアプリへ登録してください" ;;
    final_unchanged) printf '%s' "すでにバックエンドが起動済みです。以下のURLをモバイルアプリへ登録してください" ;;
    final_updated) printf '%s' "バックエンドを更新しました。以下のURLをモバイルアプリへ登録してください" ;;
  esac
}

ui_body_ja() {
  local p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    billing) cat << EOF
  このプロジェクトには請求先アカウントが紐づいていません。紐づいていないと、
  Google Cloud は何も作成できません。まだ何も作成されていません。

$(ui_t what_to_do)
  1. https://console.cloud.google.com/billing/linkedaccount?project=$p を開く
  2. 「請求先アカウントをリンク」を押して、請求先アカウントを選ぶ。
     (請求先アカウントがまだない場合は、https://console.cloud.google.com/billing
     で作成して、プロジェクトに紐づけます。)
  または、この Cloud Shell で:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

そのあと、同じセットアップのコマンドをもう一度実行してください。何度実行しても安全です。
EOF
      ;;
    protected) cat << EOF
  すでにある Cloud Run のサービス(古い版が作った "chamagon-backend" など)を作り直す
  必要がありますが、そのサービスは削除から保護されています。

$(ui_t what_to_do)
  1. どのサービスか確認する:
     gcloud run services list --project=$p
  2. 削除する(古い版のものは chamagon-backend という名前です):
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     削除されるのはサービスだけです。写真、グループ、メンバーはバケットに残ります。
  3. 同じセットアップのコマンドをもう一度実行する。
EOF
      ;;
    terraform) cat << EOF
  この Cloud Shell には Terraform が入っておらず、自動での取得もできませんでした。

$(ui_t what_to_do)
  https://developer.hashicorp.com/terraform/install の手順で Terraform を入れてから、
  同じセットアップのコマンドをもう一度実行してください。
EOF
      ;;
    not_ready) cat << EOF
  さきほど有効にした Google Cloud の API が、まだすべての場所で有効になっていません。
  通常は数分で解消します。

$(ui_t what_to_do)
  2〜3 分待ってから、同じセットアップのコマンドをもう一度実行してください。
EOF
      ;;
    quota) cat << EOF
  Google Cloud が、このリージョンで Cloud Run を開始することを、いまは拒否しました
  (短い時間に使い始められるリージョンの数に、プロジェクトごとの上限があります)。

$(ui_t what_to_do)
  数分待ってから、同じセットアップのコマンドをもう一度実行してください。
  何度も起きる場合は、新しいプロジェクトで、別の地域コード(2 番目の引数)を試してください。
EOF
      ;;
    bucket_taken) cat << EOF
  セットアップの状態を保存するバケットの名前("$p-tfstate")は、すでに別のプロジェクトで
  使われています。バケット名は、Google Cloud の全員で共有されています。

$(ui_t what_to_do)
  バケットに使われたことのない ID のプロジェクトを使ってください。自分のものでない
  バケットを使おうとしないでください。
EOF
      ;;
    no_project) cat << EOF
  ID が "$p" のプロジェクトが見つからないか、このアカウントからは見えません。

$(ui_t what_to_do)
  1. このアカウントから見えるプロジェクトを一覧して、ID(名前ではなく)をコピーする:
     gcloud projects list
  2. Cloud Shell が、プロジェクトを作ったアカウントでログインしているか確認する。
  3. 正しいプロジェクト ID で、セットアップのコマンドをもう一度実行する。
EOF
      ;;
    permission) cat << EOF
  このアカウントには、プロジェクト "$p" でその操作をする権限がありません。

$(ui_t what_to_do)
  1. プロジェクトを作ったアカウント(またはオーナーの役割を持つアカウント)を使う。
     Cloud Shell の画面の右上に表示されています。
  2. プロジェクト ID が、意図したものか確認する:
     gcloud projects list
  3. 同じセットアップのコマンドをもう一度実行する。
EOF
      ;;
    network) cat << EOF
  ネットワークの通信に失敗しました。

$(ui_t what_to_do)
  1 分ほど待ってから、同じセットアップのコマンドをもう一度実行してください。
EOF
      ;;
    *) cat << EOF
  ステップが失敗しましたが、原因は、このスクリプトが知っているものではありません。

$(ui_t what_to_do)
  同じセットアップのコマンドをもう一度実行してください。2 回目で解消する失敗も多くあります。
  同じように失敗する場合は、下に表示するログファイルを、このコマンドを教えてくれた人に
  送ってください。パスワードやトークンは含まれていません。
EOF
      ;;
  esac
}
