# What setup.sh shows the owner, and where everything else goes.
#
# The screen carries only the banner and one line per step. Everything the
# commands print (gcloud, terraform) is appended to a log file instead, and when a
# step fails the owner gets what happened, what to do next (with commands they
# can copy), the last lines of that step's output and the log's path. The last
# line of a successful run is the backend's URL, so that it is the one thing left
# to copy.
#
# Source this file. setup.sh sets SP_PROJECT and SP_REGION and calls, in order:
# ui_lang, ui_init, ui_total, then per step ui_step / ui_run (or ui_run_progress)
# / ui_done, ui_fail when a step fails, and ui_final at the end.
#
# The words are English or Japanese (SP_LANG, see ui_lang): this runs in Cloud
# Shell for owners everywhere, and a language is added by adding a branch to ui_t
# and to ui_explain_text.

SP_PROJECT=""
SP_REGION=""
SP_LANG="en"
SP_LOG=""
SP_TOTAL=0
SP_N=0
SP_STEP_OFFSET=0
SP_STEP_LABEL=""
SP_KNOWN=1
# SP_K: how many rows the cursor is below the top of the banner (every line this
# file prints is counted), so the banner can be redrawn from wherever a long step
# has left the cursor. SP_PHASE: where in its colour cycle the banner is.
# SP_LIVE: whether it may be redrawn while a step runs (see ui_init).
SP_K=0
SP_PHASE=0
SP_LIVE=0

# Whether the screen is a terminal, decided once, here, where this file is
# sourced. It must not be asked again inside $( ... ): there the output is a pipe
# and the answer is always "no" (that is why the colours and the spinner once
# never showed).
if [ -z "${SP_TTY:-}" ]; then
  SP_TTY=0
  if [ -t 1 ]; then SP_TTY=1; fi
fi
SP_MODE="none"

# ---------------------------------------------------------------- language

# SP_LANG: the language given on the command line (SHAREPI_LANG), else the one the
# shell is set to, else English. Only "ja" has a translation; anything else is
# English.
ui_lang() {
  local want="${SHAREPI_LANG:-${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}}"
  case "$want" in
    ja | ja_* | ja-* | ja.*) SP_LANG="ja" ;;
    *) SP_LANG="en" ;;
  esac
}

# A short message by key. Arguments (%s) follow the key.
ui_t() {
  local key="$1"
  shift
  local fmt
  case "$SP_LANG/$key" in
    en/intro) fmt='Setting up your SharePi backend. The details are written to:' ;;
    ja/intro) fmt='SharePi のバックエンドをセットアップします。詳しい内容は次のログに書き込みます:' ;;
    en/step_project) fmt='Checking your project' ;;
    ja/step_project) fmt='プロジェクトを確認しています' ;;
    en/step_terraform) fmt='Getting Terraform ready' ;;
    ja/step_terraform) fmt='Terraform を準備しています' ;;
    en/step_apis) fmt='Turning on the Google Cloud services it uses' ;;
    ja/step_apis) fmt='必要な Google Cloud のサービスを有効にしています' ;;
    en/step_state) fmt='Preparing the place that keeps the setup state' ;;
    ja/step_state) fmt='セットアップの状態を保存する場所を準備しています' ;;
    en/step_admin) fmt='Finding who the administrator is' ;;
    ja/step_admin) fmt='管理者を確認しています' ;;
    en/step_init) fmt='Preparing Terraform' ;;
    ja/step_init) fmt='Terraform を初期化しています' ;;
    en/step_apply) fmt='Creating your backend (this takes a few minutes)' ;;
    ja/step_apply) fmt='バックエンドを作成しています(数分かかります)' ;;
    en/step_result) fmt='Reading the result' ;;
    ja/step_result) fmt='結果を読み取っています' ;;
    en/ok) fmt='ok' ;;
    ja/ok) fmt='完了' ;;
    en/failed) fmt='failed' ;;
    ja/failed) fmt='失敗' ;;
    en/created) fmt='created' ;;
    ja/created) fmt='作成しました' ;;
    en/already_there) fmt='already there' ;;
    ja/already_there) fmt='すでにあります' ;;
    en/admin_account) fmt='your Google account' ;;
    ja/admin_account) fmt='あなたの Google アカウント' ;;
    en/admin_no_id) fmt='could not read your Google user id; your email will be used' ;;
    ja/admin_no_id) fmt='Google のユーザー ID を読み取れなかったため、メールアドレスで判定します' ;;
    en/admin_no_id_note) fmt='      Run this command again later to pin it to your user id, or set admin_subs\n      (see terraform.tfvars.example).' ;;
    ja/admin_no_id_note) fmt='      あとでこのコマンドをもう一度実行すると、ユーザー ID で判定するようになります。\n      または admin_subs を指定します(terraform.tfvars.example を参照)。' ;;
    en/retrying) fmt=' (a service is not ready yet; trying again in 60 seconds) ' ;;
    ja/retrying) fmt=' (サービスの準備がまだ終わっていません。60 秒後にやり直します) ' ;;
    en/what_happened) fmt='What happened' ;;
    ja/what_happened) fmt='何が起きたか' ;;
    en/what_to_do) fmt='What to do' ;;
    ja/what_to_do) fmt='次にやること' ;;
    en/last_lines) fmt="The last lines of what the step printed:" ;;
    ja/last_lines) fmt='このステップが最後に出力した内容:' ;;
    en/full_log) fmt='Full log: %s' ;;
    ja/full_log) fmt='詳しいログ: %s' ;;
    en/final_admin) fmt='Sign in to the app with the same Google account you used in this Cloud Shell:\nit is the administrator (recognised by: %s) and can create groups and invite people.' ;;
    ja/final_admin) fmt='アプリには、この Cloud Shell と同じ Google アカウントでログインしてください。\nそのアカウントが管理者(判定方法: %s)で、グループの作成と招待ができます。' ;;
    en/final_log) fmt='The details are in this log:' ;;
    ja/final_log) fmt='詳細のログは以下パスです。' ;;
    en/final_created) fmt='Your backend is ready. In the SharePi app, go to Profile and register the URL below:' ;;
    ja/final_created) fmt='バックエンドを作成しました。以下のURLをモバイルアプリへ登録してください' ;;
    en/final_unchanged) fmt='Your backend is already running. In the SharePi app, go to Profile and register the URL below:' ;;
    ja/final_unchanged) fmt='すでにバックエンドが起動済みです。以下のURLをモバイルアプリへ登録してください' ;;
    en/final_updated) fmt='Your backend has been updated. In the SharePi app, go to Profile and register the URL below:' ;;
    ja/final_updated) fmt='バックエンドを更新しました。以下のURLをモバイルアプリへ登録してください' ;;
    *) fmt="$key" ;;
  esac
  # shellcheck disable=SC2059
  printf "$fmt" "$@"
}

# ---------------------------------------------------------------- banner

# The banner's art, one array entry per line.
SP_ART=()
while IFS= read -r _sp_line; do SP_ART+=("$_sp_line"); done << 'BANNER'
   _____ __                    ____  _
  / ___// /_  ____ _________  / __ \(_)
  \__ \/ __ \/ __ `/ ___/ _ \/ /_/ / /
 ___/ / / / / /_/ / /  /  __/ ____/ /
/____/_/ /_/\__,_/_/   \___/_/   /_/
BANNER
unset _sp_line

# Sets SP_MODE to truecolor, 256 or none: what the terminal can show. Colour is
# off when the output is not a terminal (a pipe, a file) or NO_COLOR is set.
# SP_COLOR forces a mode (the tests do). It sets a variable rather than printing
# the answer, so that nobody calls it inside $( ... ).
ui_color_mode() {
  if [ -n "${SP_COLOR:-}" ]; then
    SP_MODE="$SP_COLOR"
  elif [ "$SP_TTY" -ne 1 ] || [ -n "${NO_COLOR:-}" ] || [ "${TERM:-dumb}" = "dumb" ]; then
    SP_MODE="none"
  elif [ -n "${CLOUD_SHELL:-}" ] || [ "${COLORTERM:-}" = "truecolor" ] || [ "${COLORTERM:-}" = "24bit" ]; then
    SP_MODE="truecolor"
  else
    SP_MODE="256"
  fi
}

# The text cursor is hidden while something is redrawn in place, or it is seen
# jumping about; it is always shown again, whatever ends the run (ui_init sets
# the traps).
ui_cursor_hide() { if [ "$SP_TTY" -eq 1 ]; then printf '\033[?25l'; fi; }
ui_cursor_show() { if [ "$SP_TTY" -eq 1 ]; then printf '\033[?25h'; fi; }

# A point on the landing page's blue -> indigo -> violet gradient, t in 0..100:
# sets SP_R, SP_G, SP_B (truecolor) and SP_P (the nearest 256-colour code).
ui_gradient() {
  local t="$1"
  local -a a b
  if [ "$t" -le 50 ]; then
    a=(59 130 246) b=(99 102 241) t=$((t * 2)) # #3b82f6 -> #6366f1
  else
    a=(99 102 241) b=(167 139 250) t=$(((t - 50) * 2)) # #6366f1 -> #a78bfa
  fi
  SP_R=$(((a[0] * (100 - t) + b[0] * t) / 100))
  SP_G=$(((a[1] * (100 - t) + b[1] * t) / 100))
  SP_B=$(((a[2] * (100 - t) + b[2] * t) / 100))
  local -a palette=(33 33 69 63 99 105 141 141)
  SP_P="${palette[$(($1 * 8 / 101))]}"
}

# One frame of the banner. The colour of each letter depends on where it is and on
# $1, the phase, so successive frames make the gradient slide along the letters.
ui_banner_frame() {
  local phase="${1:-0}" mode r c ch line out="" pos
  ui_color_mode
  mode="$SP_MODE"
  for r in "${!SP_ART[@]}"; do
    line="${SP_ART[$r]}"
    if [ "$mode" = none ]; then
      out+="$line"$'\n'
      continue
    fi
    for ((c = 0; c < ${#line}; c++)); do
      ch="${line:c:1}"
      if [ "$ch" = " " ]; then
        out+=" "
        continue
      fi
      pos=$(((c * 100 / 40 + r * 6 + phase * 8) % 200))
      [ "$pos" -gt 100 ] && pos=$((200 - pos))
      ui_gradient "$pos"
      if [ "$mode" = truecolor ]; then
        out+=$'\033[38;2;'"${SP_R};${SP_G};${SP_B}m${ch}"
      else
        out+=$'\033[38;5;'"${SP_P}m${ch}"
      fi
    done
    out+=$'\033[0m\n'
  done
  printf '%s' "$out"
}

# The banner. On a terminal the colours slide along the letters for about a
# second and a half, redrawn in place with the cursor hidden; anywhere else it is
# printed once, plain.
ui_banner() {
  echo
  ui_color_mode
  if [ "$SP_MODE" != none ] && [ "$SP_TTY" -eq 1 ]; then
    local lines=${#SP_ART[@]} f
    ui_cursor_hide
    ui_banner_frame 0
    for ((f = 1; f <= 24; f++)); do
      sleep 0.06
      printf '\033[%dA' "$lines"
      ui_banner_frame "$f"
    done
    ui_cursor_show
    SP_PHASE=24
  else
    ui_banner_frame 0
  fi
  echo
  SP_K=$((${#SP_ART[@]} + 1)) # the art, and the blank line after it
}

# The number of rows of the screen, 0 when it cannot be told.
ui_rows() {
  local r=""
  r="$(stty size 2> /dev/null | cut -d' ' -f1)" || true
  case "$r" in '' | *[!0-9]*) r="$(tput lines 2> /dev/null || true)" ;; esac
  case "$r" in '' | *[!0-9]*) r=0 ;; esac
  echo "$r"
}

# One more frame of the banner, drawn where it is: up SP_K rows, draw, back to
# where the cursor was (ESC 7 and ESC 8 save and restore it). This is what keeps
# the colours moving for as long as a step runs.
ui_banner_live() {
  printf '\e7\033[%dA\r' "$SP_K"
  ui_banner_frame "$SP_PHASE"
  printf '\e8'
  SP_PHASE=$((SP_PHASE + 1))
}

# Prints a message of one or more lines, and counts them.
ui_print_lines() {
  local text
  text="$(ui_t "$1")"
  printf '%s\n' "$text"
  SP_K=$((SP_K + $(printf '%s\n' "$text" | wc -l)))
}

# ---------------------------------------------------------------- the run

# The banner, and the log file the rest of the output goes to.
ui_init() {
  SP_LOG="${SHAREPI_SETUP_LOG:-$HOME/sharepi-setup-$(date +%Y%m%d-%H%M%S).log}"
  mkdir -p "$(dirname "$SP_LOG")"
  : > "$SP_LOG"
  chmod 600 "$SP_LOG" 2> /dev/null || true # it holds the project and the account's id
  ui_color_mode
  # What the screen was taken to be, so a colourless run can be understood.
  printf 'terminal: tty=%s colour=%s lang=%s TERM=%s COLORTERM=%s CLOUD_SHELL=%s\n' \
    "$SP_TTY" "$SP_MODE" "$SP_LANG" "${TERM:-}" "${COLORTERM:-}" "${CLOUD_SHELL:-}" >> "$SP_LOG"
  # Whatever ends the run, the cursor comes back.
  trap ui_cursor_show EXIT
  trap 'ui_cursor_show; exit 130' INT TERM
  ui_banner
  ui_t intro
  echo
  echo "  $SP_LOG"
  echo
  SP_K=$((SP_K + 3))
  # The banner is redrawn while steps run only where every row it needs is still on
  # the screen: the steps below it and the longest message must fit, or "up SP_K
  # rows" would land somewhere else. 26 rows is room for all of them.
  if [ "$SP_MODE" != none ] && [ "$SP_TTY" -eq 1 ] && [ "$(ui_rows)" -ge 26 ]; then
    SP_LIVE=1
  fi
  printf 'banner: live=%s rows=%s\n' "$SP_LIVE" "$(ui_rows)" >> "$SP_LOG"
}

ui_total() { SP_TOTAL="$1"; }

# Starts a step: "[2/7] Doing a thing ... " with no newline; ui_done or ui_fail
# ends the line.
ui_step() {
  SP_N=$((SP_N + 1))
  printf '=== [%d/%d] %s\n' "$SP_N" "$SP_TOTAL" "$1" >> "$SP_LOG"
  SP_STEP_OFFSET="$(wc -c < "$SP_LOG" | tr -d ' ')"
  SP_STEP_LABEL="$(printf '[%d/%d] %s ... ' "$SP_N" "$SP_TOTAL" "$1")"
  printf '%s' "$SP_STEP_LABEL"
}

ui_done() {
  ui_t ok
  if [ -n "${1:-}" ]; then printf ' (%s)' "$1"; fi
  echo
  SP_K=$((SP_K + 1))
}

# Runs a command with everything it prints going to the log. Functions run in
# this shell, so a PATH change they make stays.
ui_run() { "$@" >> "$SP_LOG" 2>&1; }

# The same, for a long command: a spinning bar (| / - \) in place, so it is clear
# that it is working. Without a terminal there is nothing to spin, and nothing is
# printed while it waits.
ui_run_progress() {
  "$@" >> "$SP_LOG" 2>&1 &
  local pid=$! i=0 frames='|/-\'
  ui_cursor_hide
  while kill -0 "$pid" 2> /dev/null; do
    if [ "$SP_TTY" -eq 1 ]; then
      if [ "$SP_LIVE" -eq 1 ]; then ui_banner_live; fi
      # The whole label is written again each time (\r goes back to the start of
      # the line) rather than a backspace being relied on.
      printf '\r%s%s' "$SP_STEP_LABEL" "${frames:i%4:1}"
      i=$((i + 1))
    fi
    sleep "${SP_PROGRESS_INTERVAL:-0.1}"
  done
  if [ "$SP_TTY" -eq 1 ]; then printf '\r%s' "$SP_STEP_LABEL"; fi
  ui_cursor_show
  wait "$pid"
}

# What the current step has written to the log so far.
ui_step_log() { tail -c +"$((SP_STEP_OFFSET + 1))" "$SP_LOG"; }

# ---------------------------------------------------------------- failures

# The kind of failure in the text of a failed step ($1), by the first pattern that
# matches (so the specific ones come first). Prints one word.
ui_classify() {
  local text="$1"
  if grep -qiE 'billing account.*(disabled|absent)|BILLING_DISABLED|billing must be enabled|billing has not been enabled|requires billing' <<< "$text"; then
    echo billing
  elif grep -qE 'cannot destroy service without setting deletion_protection' <<< "$text"; then
    echo protected
  elif grep -qiE 'Terraform is not installed|installed Terraform does not run|does not match the expected checksum|Could not download' <<< "$text"; then
    echo terraform
  elif grep -qiE 'SERVICE_DISABLED|has not been used in project|accessNotConfigured' <<< "$text"; then
    echo not_ready
  elif grep -qiE 'quota.*exceeded|RESOURCE_EXHAUSTED|failed to initialize in this region' <<< "$text"; then
    echo quota
  elif grep -qiE 'bucket.*(already exists|already own|not available)|409.*bucket|The requested bucket name is not available' <<< "$text"; then
    echo bucket_taken
  elif grep -qiE 'project.*(not found|does not exist)|may not exist|Could not find project|invalid project' <<< "$text"; then
    echo no_project
  elif grep -qiE 'permission|PERMISSION_DENIED|forbidden|403' <<< "$text"; then
    echo permission
  elif grep -qiE 'Could not resolve|Connection refused|i/o timeout|TLS handshake|Temporary failure|no such host|network is unreachable' <<< "$text"; then
    echo network
  else
    echo unknown
  fi
}

# "What happened" and "What to do" for a kind of failure, in SP_LANG.
ui_explain_text() {
  local kind="$1" p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  printf '%s\n' "$(ui_t what_happened)"
  case "$SP_LANG/$kind" in
    en/billing) cat << EOF
  This project has no billing account linked, and Google Cloud will not create
  anything without one. Nothing has been created yet.

$(ui_t what_to_do)
  1. Open https://console.cloud.google.com/billing/linkedaccount?project=$p
  2. Click "Link a billing account" and choose your billing account.
     (No billing account yet? Create one at https://console.cloud.google.com/billing
     and link it to the project.)
  Or here, in Cloud Shell:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

Then run the same setup command again. It is safe to repeat.
EOF
      ;;
    ja/billing) cat << EOF
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
    en/protected) cat << EOF
  The setup has to replace a Cloud Run service that already exists (for example
  the one an older version created, "chamagon-backend"), and that service is
  protected against deletion.

$(ui_t what_to_do)
  1. See which service it is:
     gcloud run services list --project=$p
  2. Delete it (the older one is called chamagon-backend):
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     Only the service is deleted. Your photos, groups and members stay in the
     bucket.
  3. Run the same setup command again.
EOF
      ;;
    ja/protected) cat << EOF
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
    en/terraform) cat << EOF
  Terraform is not installed in this Cloud Shell, and it could not be fetched
  automatically.

$(ui_t what_to_do)
  Install it by following https://developer.hashicorp.com/terraform/install,
  then run the same setup command again.
EOF
      ;;
    ja/terraform) cat << EOF
  この Cloud Shell には Terraform が入っておらず、自動での取得もできませんでした。

$(ui_t what_to_do)
  https://developer.hashicorp.com/terraform/install の手順で Terraform を入れてから、
  同じセットアップのコマンドをもう一度実行してください。
EOF
      ;;
    en/not_ready) cat << EOF
  A Google Cloud API that was turned on a moment ago is not active everywhere yet.
  This usually clears within a few minutes.

$(ui_t what_to_do)
  Wait two or three minutes, then run the same setup command again.
EOF
      ;;
    ja/not_ready) cat << EOF
  さきほど有効にした Google Cloud の API が、まだすべての場所で有効になっていません。
  通常は数分で解消します。

$(ui_t what_to_do)
  2〜3 分待ってから、同じセットアップのコマンドをもう一度実行してください。
EOF
      ;;
    en/quota) cat << EOF
  Google Cloud refused to start Cloud Run in this region for now (a per-project
  limit on how many regions a project may start using in a short time).

$(ui_t what_to_do)
  Wait a few minutes and run the same setup command again. If it keeps
  happening, try a different area code (the second argument) in a new project.
EOF
      ;;
    ja/quota) cat << EOF
  Google Cloud が、このリージョンで Cloud Run を開始することを、いまは拒否しました
  (短い時間に使い始められるリージョンの数に、プロジェクトごとの上限があります)。

$(ui_t what_to_do)
  数分待ってから、同じセットアップのコマンドをもう一度実行してください。
  何度も起きる場合は、新しいプロジェクトで、別の地域コード(2 番目の引数)を試してください。
EOF
      ;;
    en/bucket_taken) cat << EOF
  The name for the setup-state bucket ("$p-tfstate") is already taken by
  another project. Bucket names are shared by everyone on Google Cloud.

$(ui_t what_to_do)
  Use a project whose ID has not been used for a bucket before. Do not try to
  reuse a bucket you do not own.
EOF
      ;;
    ja/bucket_taken) cat << EOF
  セットアップの状態を保存するバケットの名前("$p-tfstate")は、すでに別のプロジェクトで
  使われています。バケット名は、Google Cloud の全員で共有されています。

$(ui_t what_to_do)
  バケットに使われたことのない ID のプロジェクトを使ってください。自分のものでない
  バケットを使おうとしないでください。
EOF
      ;;
    en/no_project) cat << EOF
  Google Cloud cannot find a project with the ID "$p", or this account cannot see it.

$(ui_t what_to_do)
  1. List the projects this account can see, and copy the ID (not the name):
     gcloud projects list
  2. Make sure Cloud Shell is signed in with the account that created the project.
  3. Run the setup command again with the right project ID.
EOF
      ;;
    ja/no_project) cat << EOF
  ID が "$p" のプロジェクトが見つからないか、このアカウントからは見えません。

$(ui_t what_to_do)
  1. このアカウントから見えるプロジェクトを一覧して、ID(名前ではなく)をコピーする:
     gcloud projects list
  2. Cloud Shell が、プロジェクトを作ったアカウントでログインしているか確認する。
  3. 正しいプロジェクト ID で、セットアップのコマンドをもう一度実行する。
EOF
      ;;
    en/permission) cat << EOF
  This account is not allowed to do that in project "$p".

$(ui_t what_to_do)
  1. Use the account that created the project (or that has the Owner role on it).
     It is shown at the top right of the Cloud Shell window.
  2. Check that the project ID is the one you meant:
     gcloud projects list
  3. Run the same setup command again.
EOF
      ;;
    ja/permission) cat << EOF
  このアカウントには、プロジェクト "$p" でその操作をする権限がありません。

$(ui_t what_to_do)
  1. プロジェクトを作ったアカウント(またはオーナーの役割を持つアカウント)を使う。
     Cloud Shell の画面の右上に表示されています。
  2. プロジェクト ID が、意図したものか確認する:
     gcloud projects list
  3. 同じセットアップのコマンドをもう一度実行する。
EOF
      ;;
    en/network) cat << EOF
  A network request failed.

$(ui_t what_to_do)
  Wait a minute and run the same setup command again.
EOF
      ;;
    ja/network) cat << EOF
  ネットワークの通信に失敗しました。

$(ui_t what_to_do)
  1 分ほど待ってから、同じセットアップのコマンドをもう一度実行してください。
EOF
      ;;
    en/*) cat << EOF
  A step failed, and the cause is not one this script knows.

$(ui_t what_to_do)
  Run the same setup command again; many failures clear on a second try. If it
  fails the same way, send the log file named below to the person who gave you
  this command. It does not contain passwords or tokens.
EOF
      ;;
    ja/*) cat << EOF
  ステップが失敗しましたが、原因は、このスクリプトが知っているものではありません。

$(ui_t what_to_do)
  同じセットアップのコマンドをもう一度実行してください。2 回目で解消する失敗も多くあります。
  同じように失敗する場合は、下に表示するログファイルを、このコマンドを教えてくれた人に
  送ってください。パスワードやトークンは含まれていません。
EOF
      ;;
  esac
}

# Prints "what happened" and "what to do" for the text of a failed step ($1) and
# sets SP_KNOWN to 0 when the cause is not one this script knows.
ui_explain() {
  local kind
  kind="$(ui_classify "$1")"
  SP_KNOWN=1
  if [ "$kind" = unknown ]; then SP_KNOWN=0; fi
  ui_explain_text "$kind"
}

# Ends the current step as failed: the explanation, the last lines of what the
# step printed, the log's path. Stops the script.
ui_fail() {
  ui_t failed
  echo
  echo
  ui_explain "$(ui_step_log)"
  echo
  # For a cause the script knows, the explanation is enough; for the rest the
  # raw lines are what someone needs to see.
  if [ "$SP_KNOWN" -eq 0 ]; then
    ui_t last_lines
    echo
    ui_step_log | grep -v '^[[:space:]]*$' | tail -n 8 | cut -c1-200 | sed 's/^/  | /'
    echo
  fi
  ui_t full_log "$SP_LOG"
  echo
  exit 1
}

# ---------------------------------------------------------------- the end

# The last thing printed: a note about the administrator, where the log is, what
# happened to the backend (created, already running, updated), and then the URL as
# the very last line, alone, flush left, so that selecting that line copies the URL
# and nothing else.
#   ui_final <created|unchanged|updated> <backend url> <how the administrator is recognised>
ui_final() {
  echo
  ui_t final_admin "$3"
  echo
  echo
  ui_t final_log
  echo
  echo "$SP_LOG"
  echo
  ui_t "final_$1"
  echo
  echo
  echo "$2"
}
