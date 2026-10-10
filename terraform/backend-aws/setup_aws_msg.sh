# The words of the AWS setup.sh's screen: English (the reference) and Japanese.
# Another language is added by adding its ui_fmt_<code> / ui_body_<code> pair and
# listing it in SP_LANGUAGES (setup_ui.sh); until then it is shown in English.
#
# These do not replace the Google Cloud ones. ui_fmt_aws KEY comes first and
# anything it does not have falls through to ui_fmt_<lang> and then English, so the
# shared keys (ok, failed, what_happened, ...) are the ones in setup_msg_*.sh.
#
# Commands, URLs, file names and the words in angle brackets are never translated:
# the owner copies them.

# Short messages. $SP_LANG picks the language; an unknown key is empty.
ui_fmt_aws_en() {
  case "$1" in
    step_env) printf '%s' "Checking this shell has what it needs" ;;
    step_tidy) printf '%s' "Making room for Terraform" ;;
    step_program) printf '%s' "Getting the backend program ready" ;;
    tidy_done) printf '%s' "Terraform's downloads go to %s" ;;
    admin_given) printf '%s' "the Google account the app signed in with" ;;
    program_have) printf '%s' "found" ;;
    program_downloaded) printf '%s' "downloaded and checked" ;;
    final_admin_aws) printf '%s' "Sign in to the app with the Google account the setup command came from:\nit is the administrator (recognised by: %s) and can create groups and invite people." ;;
  esac
}

ui_fmt_aws_ja() {
  case "$1" in
    step_env) printf '%s' "この画面に必要なものがあるか確かめています" ;;
    step_tidy) printf '%s' "Terraform のための場所を空けています" ;;
    step_program) printf '%s' "バックエンドのプログラムを用意しています" ;;
    tidy_done) printf '%s' "Terraform のダウンロード先: %s" ;;
    admin_given) printf '%s' "アプリでサインインした Google アカウント" ;;
    program_have) printf '%s' "見つかりました" ;;
    program_downloaded) printf '%s' "ダウンロードして確認しました" ;;
    final_admin_aws) printf '%s' "このセットアップのコマンドを出した Google アカウントでアプリにサインインしてください。\nそのアカウントが管理者(判定方法: %s)で、グループを作って人を招待できます。" ;;
  esac
}

# What to do about each kind of failure: the text under "What happened". The
# kinds are named by aws_classify (setup_aws.sh).
ui_body_aws_en() {
  local a="${SP_ACCOUNT:-<ACCOUNT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    no_space) cat << EOF
  This shell does not have enough free space for Terraform's downloads (the AWS
  provider alone is several hundred MB). AWS CloudShell's home directory is only
  1 GB. Nothing has been created yet.

$(ui_t what_to_do)
  1. See what is using the space, and delete what you do not need:
     du -sm ~/* ~/.[!.]* | sort -rn | head
  2. Run the same setup command again.
  (Files this setup made earlier were already removed.)
EOF
      ;;
    aws_login) cat << EOF
  The AWS command line has no sign-in that works in this shell (it may have
  expired).

$(ui_t what_to_do)
  Close this CloudShell, open a new one from the AWS console while signed in to
  the account you want the backend in, and run the same setup command again.
EOF
      ;;
    missing_tool) cat << EOF
  A tool this setup needs is missing in this shell (the line above names it).

$(ui_t what_to_do)
  Use AWS CloudShell (it has aws, curl, unzip and sha256sum), then run the same
  setup command again.
EOF
      ;;
    scp) cat << EOF
  Your AWS organization's service control policy refuses this action in account
  $a. That is a rule set above this account: nothing you do inside it can
  override it. Accounts made with AWS's simplified sign-up can only use their one
  home Region until "advanced features" are activated, and some services are
  limited until then.

$(ui_t what_to_do)
  1. Find the account's Region: open https://settings.aws.com, choose Project and
     look at "Additional info". Run the setup with an area code that matches it,
     or
  2. use an AWS account made with "Sign up for AWS (advanced)", or ask whoever
     administers your organization to allow the actions in the error above.
  Nothing was changed in your account by the step that failed.
EOF
      ;;
    permission) cat << EOF
  This AWS account's sign-in is not allowed to do something the setup needs (the
  lines below say what).

$(ui_t what_to_do)
  Use a sign-in that can create S3 buckets, Lambda functions and IAM roles
  (an administrator), then run the same setup command again.
EOF
      ;;
    bucket_taken) cat << EOF
  The name for the setup-state bucket is taken by another AWS account. Bucket
  names are shared by everyone on AWS.

$(ui_t what_to_do)
  Run the same setup command again after setting a different name:
     export SHAREPI_STATE_BUCKET=<a-unique-bucket-name>
EOF
      ;;
    concurrency) cat << EOF
  A new AWS account allows only a few Lambda functions to run at once, and the
  setup asked to reserve some of that. This should not happen with the default
  settings.

$(ui_t what_to_do)
  Run the same setup command again. If it keeps happening, tell the people who
  maintain SharePi (the lines below are what to send).
EOF
      ;;
    statement_exists) cat << EOF
  A permission this setup creates already exists in the account (for example one
  you added by hand with "aws lambda add-permission").

$(ui_t what_to_do)
  Remove the one that is there, then run the same setup command again:
     aws lambda remove-permission --region $r --function-name chamagon-backend --statement-id FunctionURLAllowPublicAccess
     aws lambda remove-permission --region $r --function-name chamagon-backend --statement-id FunctionURLInvokeAllowPublicAccess
EOF
      ;;
    no_program) cat << EOF
  The backend program was not found next to this setup, and no address to
  download it from is set.

$(ui_t what_to_do)
  Put the built program next to setup.sh as "bootstrap", or set where to get it:
     export SHAREPI_BACKEND_URL=<address of the program's zip>
     export SHAREPI_BACKEND_SHA256=<its SHA-256>
  then run the same setup command again.
EOF
      ;;
    *) ui_body_en "$1" ;;
  esac
}

ui_body_aws_ja() {
  local a="${SP_ACCOUNT:-<ACCOUNT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    no_space) cat << EOF
  この画面には、Terraform のダウンロード用の空き容量が足りません(AWS プロバイダだけで
  数百 MB あります)。AWS CloudShell のホームは 1 GB しかありません。まだ何も作って
  いません。

$(ui_t what_to_do)
  1. 容量を使っているものを調べて、要らないものを消してください:
     du -sm ~/* ~/.[!.]* | sort -rn | head
  2. 同じセットアップのコマンドをもう一度実行してください。
  (このセットアップが前に作ったファイルは、すでに消してあります。)
EOF
      ;;
    aws_login) cat << EOF
  AWS のコマンドラインに、この画面で使えるサインインがありません(期限切れかもしれ
  ません)。

$(ui_t what_to_do)
  この CloudShell を閉じ、バックエンドを作りたいアカウントにサインインしたまま
  AWS コンソールから新しい CloudShell を開いて、同じセットアップのコマンドをもう
  一度実行してください。
EOF
      ;;
    missing_tool) cat << EOF
  このセットアップに必要なツールが、この画面にありません(上の行に名前が出ています)。

$(ui_t what_to_do)
  AWS CloudShell(aws、curl、unzip、sha256sum が入っています)で、同じセットアップの
  コマンドをもう一度実行してください。
EOF
      ;;
    scp) cat << EOF
  AWS の組織のサービスコントロールポリシーが、アカウント $a でのこの操作を拒否して
  います。これはアカウントより上で決められたルールで、アカウントの中では解除でき
  ません。AWS の「簡易サインアップ」で作ったアカウントは、「高度な機能」を有効に
  するまで、最初に決まった 1 つのリージョンしか使えず、一部のサービスも制限されます。

$(ui_t what_to_do)
  1. アカウントのリージョンを確認します。https://settings.aws.com を開き、
     プロジェクト →「追加情報」を見てください。そのリージョンに合う地域コードで、
     セットアップを実行します。または
  2. 「Sign up for AWS (advanced)」で作った AWS アカウントを使うか、組織の管理者に、
     上のエラーの操作を許可してもらってください。
  失敗したステップで、アカウントは何も変更されていません。
EOF
      ;;
    permission) cat << EOF
  この AWS アカウントのサインインには、セットアップに必要な操作の権限がありません
  (下の行に内容が出ています)。

$(ui_t what_to_do)
  S3 のバケット、Lambda 関数、IAM ロールを作れるサインイン(管理者)で、同じ
  セットアップのコマンドをもう一度実行してください。
EOF
      ;;
    bucket_taken) cat << EOF
  設定の状態を置くバケットの名前が、別の AWS アカウントですでに使われています。
  バケットの名前は、AWS 全体で共有です。

$(ui_t what_to_do)
  別の名前を決めてから、同じセットアップのコマンドをもう一度実行してください:
     export SHAREPI_STATE_BUCKET=<他と重ならないバケット名>
EOF
      ;;
    concurrency) cat << EOF
  新しい AWS アカウントでは、同時に動かせる Lambda の数が少なく、セットアップが
  その一部を予約しようとして断られました。既定の設定では、起きないはずです。

$(ui_t what_to_do)
  同じセットアップのコマンドをもう一度実行してください。続けて起きる場合は、
  SharePi の開発者に連絡してください(下の行を送ってください)。
EOF
      ;;
    statement_exists) cat << EOF
  このセットアップが作る権限が、すでにアカウントにあります(たとえば、手作業で
  "aws lambda add-permission" を実行した場合です)。

$(ui_t what_to_do)
  あるほうを消してから、同じセットアップのコマンドをもう一度実行してください:
     aws lambda remove-permission --region $r --function-name chamagon-backend --statement-id FunctionURLAllowPublicAccess
     aws lambda remove-permission --region $r --function-name chamagon-backend --statement-id FunctionURLInvokeAllowPublicAccess
EOF
      ;;
    no_program) cat << EOF
  バックエンドのプログラムが、このセットアップの隣に見つからず、ダウンロード元も
  指定されていません。

$(ui_t what_to_do)
  作成済みのプログラムを "bootstrap" という名前で setup.sh の隣に置くか、入手先を
  指定してください:
     export SHAREPI_BACKEND_URL=<プログラムの zip のアドレス>
     export SHAREPI_BACKEND_SHA256=<その SHA-256>
  そのうえで、同じセットアップのコマンドをもう一度実行してください。
EOF
      ;;
    *) ui_body_ja "$1" ;;
  esac
}

# The AWS word for a key first, then the shared one (ui_t's own lookup). The
# shared ui_t asks ui_fmt_<lang>; this wraps it so the AWS keys are found too.
ui_t_aws() {
  local key="$1"
  shift
  local fmt=""
  # A language with no AWS words has no function of that name: English then.
  if declare -F "ui_fmt_aws_$SP_LANG" > /dev/null; then
    fmt="$("ui_fmt_aws_$SP_LANG" "$key")"
  fi
  if [ -z "$fmt" ]; then fmt="$(ui_fmt_aws_en "$key")"; fi
  if [ -n "$fmt" ]; then
    # shellcheck disable=SC2059
    printf "$fmt" "$@"
  else
    ui_t "$key" "$@"
  fi
}
