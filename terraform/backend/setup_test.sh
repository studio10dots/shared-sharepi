#!/usr/bin/env bash
# Runs setup.sh end to end against stand-ins for gcloud, terraform and sleep, and
# checks what the owner sees and what is left in the log:
#   bash setup_test.sh
# Nothing here touches Google Cloud or the network.
set -uo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fail=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1" >&2; fail=1; }
contains() { # name text needle
  if grep -qF -- "$3" <<< "$2"; then ok "$1"; else bad "$1: [$3] not found in: $(head -c 300 <<< "$2")"; fi
}
lacks() { # name text needle
  if grep -qF -- "$3" <<< "$2"; then bad "$1: [$3] should not appear"; else ok "$1"; fi
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# A JWT whose payload carries a user id, which is what the gcloud stand-in prints.
payload="$(printf '%s' '{"sub":"100000000000000000001","email":"a@b.c"}' | base64 | tr -d '\n=' | tr '+/' '-_')"
fake_jwt="eyJhbGciOiJSUzI1NiJ9.${payload}.c2ln"

# --- stand-ins, steered by FAKE_* variables
mkdir "$work/bin"
cat > "$work/bin/gcloud" << EOF
#!/usr/bin/env bash
echo "gcloud \$*" >> "\$FAKE_CALLS"
case "\$1 \$2" in
  "projects describe") echo "\${FAKE_PROJECT:-hoge-vfltdc}"; exit "\${FAKE_PROJECT_EXIT:-0}" ;;
  "billing projects") echo "\${FAKE_BILLING:-True}"; exit "\${FAKE_BILLING_EXIT:-0}" ;;
  "services enable")
    if [ -n "\${FAKE_ENABLE_ERROR:-}" ]; then echo "\$FAKE_ENABLE_ERROR" >&2; exit 1; fi ;;
  "auth print-identity-token") echo "$fake_jwt" ;;
  "storage buckets")
    if [ "\$3" = "describe" ]; then exit "\${FAKE_BUCKET_DESCRIBE_EXIT:-1}"; fi
    # A bucket made during the run is the project's from then on.
    if [ "\$3" = "create" ]; then echo "\${4#gs://}" >> "\$FAKE_DIR/created_buckets"; exit 0; fi
    # The buckets of the project: FAKE_PROJECT_BUCKETS (space-separated) and the ones made above.
    if [ "\$3" = "list" ]; then
      for b in \${FAKE_PROJECT_BUCKETS:-}; do echo "\$b"; done
      cat "\$FAKE_DIR/created_buckets" 2> /dev/null
      exit 0
    fi ;;
  "storage rm")
    # The bucket named last is gone.
    gone="\${@: -1}"; gone="\${gone#gs://}"
    if [ -f "\$FAKE_DIR/created_buckets" ]; then
      grep -vxF "\$gone" "\$FAKE_DIR/created_buckets" > "\$FAKE_DIR/created_buckets.tmp" || true
      mv "\$FAKE_DIR/created_buckets.tmp" "\$FAKE_DIR/created_buckets"
    fi
    exit 0 ;;
  "storage ls")
    # An object exists when "bucket/path" is in FAKE_OBJECTS (space-separated).
    for o in \${FAKE_OBJECTS:-}; do if [ "gs://\$o" = "\$3" ]; then exit 0; fi; done
    exit 1 ;;
  "storage rsync") exit "\${FAKE_RSYNC_EXIT:-0}" ;;
esac
exit 0
EOF
cat > "$work/bin/terraform" << 'EOF'
#!/usr/bin/env bash
echo "terraform $*" >> "$FAKE_CALLS"
case "$1" in
  version) echo "Terraform v1.15.9"; exit 0 ;;
  init) echo "Terraform has been successfully initialized!"; exit 0 ;;
  apply)
    echo "google_project_service.required[\"run.googleapis.com\"]: Creating..."
    # So that a spinner has time to turn (only the terminal test sets this).
    if [ -n "${FAKE_APPLY_SLEEP:-}" ]; then sleep "$FAKE_APPLY_SLEEP"; fi
    n_file="$FAKE_DIR/apply_count"; n="$(( $(cat "$n_file" 2>/dev/null || echo 0) + 1 ))"; echo "$n" > "$n_file"
    case "${FAKE_APPLY:-ok}" in
      ok)
        echo "google_cloud_run_v2_service.backend: Creation complete after 20s"
        echo "Apply complete! Resources: 9 added, 0 changed, 0 destroyed."; exit 0 ;;
      nochange)
        echo "No changes. Your infrastructure matches the configuration."
        echo "Apply complete! Resources: 0 added, 0 changed, 0 destroyed."; exit 0 ;;
      updated) echo "Apply complete! Resources: 0 added, 1 changed, 0 destroyed."; exit 0 ;;
      flaky) if [ "$n" -lt 2 ]; then echo "Error 403: Service Usage API has not been used (SERVICE_DISABLED)"; exit 1; fi; echo "Apply complete!"; exit 0 ;;
      always_disabled) echo "Error 403: SERVICE_DISABLED"; exit 1 ;;
      other) echo "Error: something else entirely"; exit 1 ;;
      # Google made the photos bucket but the answer was lost; the retry is told it is ours.
      owned409|owned409_always)
        if [ "${FAKE_APPLY}" = owned409_always ] || [ "$n" -lt 2 ]; then
          echo "google_storage_bucket.photos: Creating..."
          echo
          echo "Error: googleapi: Error 409: Your previous request to create the named bucket succeeded and you already own it., conflict"
          echo
          echo "  with google_storage_bucket.photos,"
          echo "  on main.tf line 49, in resource \"google_storage_bucket\" \"photos\":"
          exit 1
        fi
        echo "google_cloud_run_v2_service.backend: Creation complete after 20s"
        echo "Apply complete! Resources: 1 added, 0 changed, 0 destroyed."; exit 0 ;;
    esac ;;
  import)
    if [ "${FAKE_IMPORT_EXIT:-0}" -ne 0 ]; then echo "Error: Cannot import non-existent remote object"; exit "$FAKE_IMPORT_EXIT"; fi
    echo "google_storage_bucket.photos: Import prepared!"; exit 0 ;;
  state)
    case "$2" in
      show)
        # The photos bucket the state holds, if any (FAKE_STATE_PHOTOS is its name).
        if [ -z "${FAKE_STATE_PHOTOS:-}" ]; then echo "No instance found for the given address!" >&2; exit 1; fi
        echo "# google_storage_bucket.photos:"
        echo 'resource "google_storage_bucket" "photos" {'
        echo "    location                    = \"ASIA-NORTHEAST1\""
        echo "    name                        = \"$FAKE_STATE_PHOTOS\""
        echo "}"; exit 0 ;;
      rm) echo "Successfully removed 1 resource instance(s)."; exit 0 ;;
    esac ;;
  output)
    case "$3" in
      backend_url) echo -n "https://sharepi-backend-123.asia-northeast1.run.app" ;;
      administrator_check) echo -n "user id" ;;
    esac; exit 0 ;;
esac
EOF
printf '#!/bin/sh\nexit 0\n' > "$work/bin/sleep"
chmod +x "$work/bin/"*

run_setup() { # name, then FAKE_* assignments as arguments; sets $out, $rc, $log
  local name="$1"; shift
  local d="$work/run_$name"
  mkdir -p "$d/scripts"
  cp "$here"/*.sh "$d/scripts/"
  : > "$d/calls"
  rm -f "$d/apply_count" "$d/created_buckets"
  log="$d/setup.log"
  # FAKE_LANG_ARG=ja adds the optional third argument; LANG is set to something
  # that is not Japanese so that the language is chosen by the test alone.
  out="$(env LANG=C LC_ALL= LC_MESSAGES= "$@" FAKE_CALLS="$d/calls" FAKE_DIR="$d" SHAREPI_SETUP_LOG="$log" SP_PROGRESS_INTERVAL=0.05 \
    PATH="$work/bin:$PATH" HOME="$d" \
    bash "$d/scripts/setup.sh" hoge-vfltdc ea ${FAKE_LANG_ARG_VALUE:-} 2>&1)"
  rc=$?
  calls="$(cat "$d/calls")"
}

# ---------------------------------------------------------------- success
run_setup success
out_success="$out"
[ "$rc" -eq 0 ] && ok "success: exits 0" || bad "success: exit $rc: $out"
contains "success: the banner is shown" "$out" '/ ___// /_  ____ _________  / __ \(_)'
contains "success: steps are numbered" "$out" '[1/8] Checking your project ... ok'
contains "success: the last step" "$out" '[8/8] Reading the result ... ok'
contains "success: the URL is shown" "$out" 'https://sharepi-backend-123.asia-northeast1.run.app'
contains "success: says what to do next" "$out" 'register the URL below'
contains "success: a new backend is called ready" "$out" 'Your backend is ready.'
url='https://sharepi-backend-123.asia-northeast1.run.app'
# On a line of its own, nothing else on it, and the very last line: that is what
# makes it the one thing left to copy.
if grep -qxF "$url" <<< "$out"; then ok "success: the URL is on a line of its own"; else bad "success: the URL is not alone on its line"; fi
[ "$(grep -v '^[[:space:]]*$' <<< "$out" | tail -n 1)" = "$url" ] && ok "success: the URL is the last line" || bad "success: the last line is not the URL"
# The log's path comes before the URL, not after it.
log_line="$(grep -n -F "$log" <<< "$out" | tail -n 1 | cut -d: -f1)"
url_line="$(grep -n -x -F "$url" <<< "$out" | tail -n 1 | cut -d: -f1)"
[ -n "$log_line" ] && [ "$log_line" -lt "$url_line" ] && ok "success: the log path is shown before the URL" || bad "success: the log path ($log_line) is not before the URL ($url_line)"
contains "success: says where the log is" "$out" 'The details are in this log:'
contains "success: the step line has no stray characters" "$out" '[7/8] Creating your backend (this takes a few minutes) ... ok'
lacks "success: no escape codes without a terminal" "$out" $'\033'
contains "success: names the log" "$out" "$log"
lacks "success: no command output on the screen" "$out" 'Terraform has been successfully initialized'
lacks "success: no resource lines on the screen" "$out" 'Creating...'
contains "success: command output is in the log" "$(cat "$log")" 'Terraform has been successfully initialized'
contains "success: the user id was passed on" "$calls" 'admin_subs=["100000000000000000001"]'

# ---------------------------------------------------------------- what the run did to the backend
run_setup unchanged FAKE_APPLY=nochange
contains "outcome: nothing to change means it was already running" "$out" 'Your backend is already running.'
[ "$(grep -v '^[[:space:]]*$' <<< "$out" | tail -n 1)" = "$url" ] && ok "outcome: an existing backend still ends on its URL" || bad "outcome: the last line is not the URL"
run_setup updated FAKE_APPLY=updated
contains "outcome: a change means it was updated" "$out" 'Your backend has been updated.'

# ---------------------------------------------------------------- Japanese
FAKE_LANG_ARG_VALUE=ja run_setup ja_unchanged FAKE_APPLY=nochange
contains "ja: the steps are in Japanese" "$out" '[1/8] プロジェクトを確認しています ... 完了'
contains "ja: says where the log is" "$out" '詳細のログは以下パスです。'
contains "ja: an existing backend is called running" "$out" 'すでにバックエンドが起動済みです。以下のURLをモバイルアプリへ登録してください'
[ "$(grep -v '^[[:space:]]*$' <<< "$out" | tail -n 1)" = "$url" ] && ok "ja: the URL is the last line" || bad "ja: the last line is not the URL"
FAKE_LANG_ARG_VALUE=ja run_setup ja_created
contains "ja: a new backend" "$out" 'バックエンドを作成しました。以下のURLをモバイルアプリへ登録してください'
FAKE_LANG_ARG_VALUE=ja run_setup ja_billing FAKE_BILLING=False
contains "ja: the billing explanation" "$out" '請求先アカウントが紐づいていません'
contains "ja: the billing commands are unchanged" "$out" 'gcloud billing projects link hoge-vfltdc --billing-account=<ACCOUNT_ID>'
# The shell's own language is used when none is given.
run_setup ja_from_env LANG=ja_JP.UTF-8
contains "ja: chosen from the shell's language" "$out" '[1/8] プロジェクトを確認しています'
# A language with no translation is English.
FAKE_LANG_ARG_VALUE=it run_setup it
contains "a language with no translation: English" "$out" '[1/8] Checking your project'
run_setup regional LANG=pt_BR.UTF-8
contains "a regional shell language picks its language (pt_BR -> Portuguese)" "$out" '[1/8] Verificando seu projeto'

# ---------------------------------------------------------------- every language
# Each language runs the whole of setup.sh (the first step, a billing failure and the
# final lines) and keeps the owner's commands and the URL exactly as they are.
source "$here/setup_ui.sh"
SP_PROJECT=hoge-vfltdc
SP_REGION=asia-northeast1
for lang in es fr de pt ko; do
  SP_LANG=$lang
  first_step="$(ui_t step_project)"
  ok_word="$(ui_t ok)"
  FAKE_LANG_ARG_VALUE=$lang run_setup "all_$lang" FAKE_APPLY=nochange
  contains "$lang: the first step is in $lang" "$out" "[1/8] $first_step ... $ok_word"
  lacks "$lang: the first step is not English" "$out" '[1/8] Checking your project'
  contains "$lang: an existing backend is called running" "$out" "$(ui_t final_unchanged)"
  [ "$(grep -v '^[[:space:]]*$' <<< "$out" | tail -n 1)" = "$url" ] && ok "$lang: the URL is the last line" || bad "$lang: the last line is not the URL"
  FAKE_LANG_ARG_VALUE=$lang run_setup "billing_$lang" FAKE_BILLING=False
  contains "$lang: the billing commands are unchanged" "$out" 'gcloud billing projects link hoge-vfltdc --billing-account=<ACCOUNT_ID>'
  contains "$lang: the billing link is unchanged" "$out" 'https://console.cloud.google.com/billing/linkedaccount?project=hoge-vfltdc'
  lacks "$lang: the billing explanation is not English" "$out" 'has no billing account linked'
done

# The catalogues: every language has every key and every kind of failure, with the same
# placeholders and line breaks as the English, and the same commands and URLs.
keys="$(awk '/^ui_fmt_en\(\)/,/^}/' "$here/setup_msg_en.sh" | grep -oE '^    [a-z_]+\)' | tr -d ' )')"
kinds="billing protected terraform not_ready quota bucket_taken no_project permission network unknown"
count() { grep -oF -- "$1" <<< "$2" | wc -l | tr -d ' '; }
commands() { grep -E '^[[:space:]]+gcloud ' <<< "$1" | sed 's/^[[:space:]]*//' | sort; }
urls() { grep -oE 'https://[A-Za-z0-9./?=_-]+' <<< "$1" | sort; }
bad_catalogue=0
for lang in $SP_LANGUAGES; do
  for key in $keys; do
    en="$(ui_fmt_en "$key")"
    val="$("ui_fmt_$lang" "$key")"
    if [ -z "$val" ]; then bad "$lang: the key $key is missing"; bad_catalogue=1; continue; fi
    if [ "$(count '%s' "$en")" != "$(count '%s' "$val")" ]; then bad "$lang: $key has other placeholders than English"; bad_catalogue=1; fi
    if [ "$(count '\n' "$en")" != "$(count '\n' "$val")" ]; then bad "$lang: $key has other line breaks than English"; bad_catalogue=1; fi
  done
  for kind in $kinds; do
    SP_LANG=en
    en_body="$(ui_body_en "$kind")"
    SP_LANG=$lang
    body="$("ui_body_$lang" "$kind")"
    if [ -z "$body" ]; then bad "$lang: the explanation of $kind is missing"; bad_catalogue=1; continue; fi
    if [ "$(commands "$en_body")" != "$(commands "$body")" ]; then bad "$lang: $kind has other commands than English"; bad_catalogue=1; fi
    if [ "$(urls "$en_body")" != "$(urls "$body")" ]; then bad "$lang: $kind has other URLs than English"; bad_catalogue=1; fi
  done
done
[ "$bad_catalogue" -eq 0 ] && ok "catalogues: all ${SP_LANGUAGES// /, } have every key and every kind, with the same placeholders, commands and URLs"
SP_LANG=en

# ---------------------------------------------------------------- billing not linked
run_setup billing FAKE_BILLING=False
out_billing="$out"
[ "$rc" -ne 0 ] && ok "billing: stops" || bad "billing: did not stop"
contains "billing: explains" "$out" 'no billing account linked'
contains "billing: gives the link" "$out" 'https://console.cloud.google.com/billing/linkedaccount?project=hoge-vfltdc'
contains "billing: gives the commands" "$out" 'gcloud billing projects link hoge-vfltdc --billing-account=<ACCOUNT_ID>'
contains "billing: says it is safe to repeat" "$out" 'safe to repeat'
lacks "billing: nothing was enabled" "$calls" 'services enable'
lacks "billing: no raw lines for a known cause" "$out" 'The last lines of what the step printed'

# The check cannot be made (the Billing API may be off): carry on, and report it later.
run_setup billing_unknown FAKE_BILLING_EXIT=1 FAKE_ENABLE_ERROR="ERROR: (gcloud.services.enable) FAILED_PRECONDITION: The billing account for the owning project is disabled in state absent"
contains "billing unknown: a later step still explains it" "$out" 'no billing account linked'

# ---------------------------------------------------------------- other known failures
run_setup api FAKE_ENABLE_ERROR="Error 403: Service Usage API has not been used in project hoge-vfltdc before or it is disabled. SERVICE_DISABLED"
contains "service not ready: explains" "$out" 'Wait two or three minutes'

run_setup perm FAKE_ENABLE_ERROR="ERROR: (gcloud.services.enable) PERMISSION_DENIED: The caller does not have permission"
contains "permission: explains" "$out" 'not allowed to do that in project "hoge-vfltdc"'

run_setup noproject FAKE_PROJECT_EXIT=1 FAKE_PROJECT="ERROR: (gcloud.projects.describe) Project hoge-vfltdc may not exist"
contains "unknown project: explains" "$out" 'cannot find a project with the ID "hoge-vfltdc"'
contains "unknown project: points to gcloud projects list" "$out" 'gcloud projects list'

run_setup unknown FAKE_ENABLE_ERROR="something nobody has seen"
contains "unknown error: still says what to do" "$out" 'Run the same setup command again'
contains "unknown error: shows the last lines" "$out" 'something nobody has seen'
contains "unknown error: names the log" "$out" 'Full log:'

# ---------------------------------------------------------------- apply
run_setup flaky FAKE_APPLY=flaky
[ "$rc" -eq 0 ] && ok "apply: waits out a service that is not ready yet" || bad "apply flaky: exit $rc: $out"
contains "apply: says it is trying again" "$out" 'trying again in 60 seconds'
[ "$(cat "$work/run_flaky/apply_count")" = "2" ] && ok "apply: ran twice" || bad "apply flaky: ran $(cat "$work/run_flaky/apply_count") times"

run_setup always FAKE_APPLY=always_disabled
[ "$rc" -ne 0 ] && ok "apply: gives up after three tries" || bad "apply always_disabled did not stop"
[ "$(cat "$work/run_always/apply_count")" = "3" ] && ok "apply: three tries" || bad "apply always_disabled: ran $(cat "$work/run_always/apply_count") times"

run_setup other FAKE_APPLY=other
[ "$(cat "$work/run_other/apply_count")" = "1" ] && ok "apply: another error is not retried" || bad "apply other: retried"
contains "apply: another error is explained generically" "$out" 'cause is not one this script knows'

# ---------------------------------------------------------------- a bucket Google made but we never heard of
# The owner does nothing: the bucket goes into the state and the apply carries on.
run_setup adopt FAKE_APPLY=owned409
[ "$rc" -eq 0 ] && ok "adopt: carries on and finishes" || bad "adopt: exit $rc: $out"
contains "adopt: imports the photos bucket under the name main.tf derives" "$calls" 'google_storage_bucket.photos sharepi-hoge-vfltdc-photos'
contains "adopt: the import gets the same variables as the apply" "$calls" 'terraform import -input=false -var=project_id=hoge-vfltdc'
[ "$(cat "$work/run_adopt/apply_count")" = "2" ] && ok "adopt: the apply ran again after the import" || bad "adopt: ran $(cat "$work/run_adopt/apply_count") times"
contains "adopt: says what it is doing" "$out" 'picking up what an earlier run already made'
contains "adopt: a new backend is called ready" "$out" 'Your backend is ready.'
[ "$(grep -v '^[[:space:]]*$' <<< "$out" | tail -n 1)" = "$url" ] && ok "adopt: still ends on the URL" || bad "adopt: the last line is not the URL"
lacks "adopt: the error is not shown to the owner" "$out" 'you already own it'
FAKE_LANG_ARG_VALUE=ja run_setup adopt_ja FAKE_APPLY=owned409
contains "adopt: the message is in the owner's language" "$out" '前回の実行で作られたものを引き継いで、続けます'

# It is tried once. An import that fails, or a 409 that comes back, stops the run
# and is not blamed on somebody else's bucket.
run_setup adopt_bad_import FAKE_APPLY=owned409 FAKE_IMPORT_EXIT=1
[ "$rc" -ne 0 ] && ok "adopt: a failing import stops the run" || bad "adopt: a failing import did not stop it"
[ "$(cat "$work/run_adopt_bad_import/apply_count")" = "1" ] && ok "adopt: no second apply after a failed import" || bad "adopt: applied again after a failed import"
lacks "adopt: not reported as another project's bucket (import fails)" "$out" 'already taken by'
run_setup adopt_loop FAKE_APPLY=owned409_always
[ "$rc" -ne 0 ] && ok "adopt: a 409 that keeps coming stops the run" || bad "adopt: a repeating 409 did not stop it"
[ "$(cat "$work/run_adopt_loop/apply_count")" = "2" ] && ok "adopt: imported once, applied twice, no loop" || bad "adopt: ran $(cat "$work/run_adopt_loop/apply_count") times"
[ "$(count 'terraform import' "$calls")" = "1" ] && ok "adopt: the import ran once" || bad "adopt: the import ran $(count 'terraform import' "$calls") times"
lacks "adopt: not reported as another project's bucket (409 repeats)" "$out" 'already taken by'

# What each message means: another project holding the name is reported as such, the
# owner's own bucket ("you already own it") is not.
source "$here/setup_ui.sh"
[ "$(ui_classify 'Error 409: Your previous request to create the named bucket succeeded and you already own it., conflict')" != bucket_taken ] && ok "classify: 'you already own it' is not another project's bucket" || bad "classify: 'you already own it' reported as taken"
[ "$(ui_classify 'ERROR: HTTPError 409: The requested bucket name is not available. The bucket namespace is shared by all users of the system.')" = bucket_taken ] && ok "classify: a name held by somebody else is reported" || bad "classify: 'not available' not reported as taken"
[ "$(ui_classify "A Cloud Storage bucket named 'x' already exists. Try another name.")" = bucket_taken ] && ok "classify: 'already exists' is reported" || bad "classify: 'already exists' not reported as taken"

# ---------------------------------------------------------------- the state bucket
run_setup bucket_new FAKE_BUCKET_DESCRIBE_EXIT=1
contains "state bucket: created when missing" "$calls" 'gcloud storage buckets create gs://sharepi-hoge-vfltdc-tfstate'
contains "state bucket: says so" "$out" 'ok (created)'
run_setup bucket_old FAKE_BUCKET_DESCRIBE_EXIT=0
lacks "state bucket: not created again" "$calls" 'buckets create'
contains "state bucket: says so" "$out" 'ok (already there)'

# ---------------------------------------------------------------- buckets under the earlier names
# Buckets used to be "<project>-tfstate" and "<project>-photos"; they are now
# "sharepi-<project>-...". An owner who set up under the earlier names is moved over
# by running the same command again, with nothing to do and nothing deleted.
line_of() { grep -n -F -- "$2" <<< "$1" | head -n 1 | cut -d: -f1; }
earlier_state="hoge-vfltdc-tfstate"
earlier_photos="hoge-vfltdc-photos"
new_state="sharepi-hoge-vfltdc-tfstate"
new_photos="sharepi-hoge-vfltdc-photos"

run_setup earlier_none
lacks "earlier buckets: a fresh project copies nothing" "$calls" 'storage rsync'
lacks "earlier buckets: a fresh project leaves the state alone" "$calls" 'terraform state rm'
lacks "earlier buckets: a fresh project is not told about old buckets" "$out" 'with the earlier names'

# -- the state bucket
run_setup earlier_state FAKE_PROJECT_BUCKETS="$earlier_state" FAKE_OBJECTS="$earlier_state/sharepi-backend/default.tfstate"
[ "$rc" -eq 0 ] && ok "state move: finishes" || bad "state move: exit $rc: $out"
contains "state move: copies the state to the new bucket" "$calls" "gcloud storage rsync --recursive --exclude=.*\\.tflock\$ gs://$earlier_state gs://$new_state"
contains "state move: Terraform is pointed at the new bucket" "$(cat "$work/run_earlier_state/scripts/backend.tf")" "bucket = \"$new_state\""
lacks "state move: the earlier bucket is never deleted" "$calls" "storage rm --recursive gs://$earlier_state"
contains "state move: says which bucket was left" "$out" "gs://$earlier_state"
contains "state move: gives the command that deletes it" "$out" "gcloud storage rm --recursive gs://$earlier_state"
[ "$(grep -v '^[[:space:]]*$' <<< "$out" | tail -n 1)" = "$url" ] && ok "state move: still ends on the URL" || bad "state move: the last line is not the URL"
FAKE_LANG_ARG_VALUE=ja run_setup earlier_state_ja FAKE_PROJECT_BUCKETS="$earlier_state" FAKE_OBJECTS="$earlier_state/sharepi-backend/default.tfstate"
contains "state move: the note is in the owner's language" "$out" 'そのまま残してあります'

run_setup earlier_state_foreign FAKE_PROJECT_BUCKETS="$earlier_state"
lacks "state move: a bucket that only has the name is not copied" "$calls" 'storage rsync'
run_setup earlier_state_done FAKE_BUCKET_DESCRIBE_EXIT=0 FAKE_PROJECT_BUCKETS="$earlier_state" FAKE_OBJECTS="$earlier_state/sharepi-backend/default.tfstate"
lacks "state move: never copies over a state that already moved (it would be the stale one)" "$calls" 'storage rsync'
run_setup earlier_state_fails FAKE_PROJECT_BUCKETS="$earlier_state" FAKE_OBJECTS="$earlier_state/sharepi-backend/default.tfstate" FAKE_RSYNC_EXIT=1
[ "$rc" -ne 0 ] && ok "state move: a failed copy stops the run" || bad "state move: a failed copy did not stop it"
contains "state move: the half-made bucket is removed, so the next run starts again" "$calls" "storage rm --recursive gs://$new_state"
[ ! -f "$work/run_earlier_state_fails/apply_count" ] && ok "state move: nothing was applied after a failed copy" || bad "state move: applied after a failed copy"

# -- the photos bucket
run_setup earlier_photos FAKE_PROJECT_BUCKETS="$earlier_photos" FAKE_STATE_PHOTOS="$earlier_photos"
[ "$rc" -eq 0 ] && ok "photos move: finishes" || bad "photos move: exit $rc: $out"
c_create="$(line_of "$calls" "gcloud storage buckets create gs://$new_photos")"
c_copy="$(line_of "$calls" "gcloud storage rsync --recursive gs://$earlier_photos gs://$new_photos")"
c_rm="$(line_of "$calls" 'terraform state rm google_storage_bucket.photos')"
c_import="$(line_of "$calls" "google_storage_bucket.photos $new_photos")"
c_apply="$(line_of "$calls" 'terraform apply')"
if [ -n "$c_create" ] && [ -n "$c_copy" ] && [ -n "$c_rm" ] && [ -n "$c_import" ] && [ -n "$c_apply" ] \
  && [ "$c_create" -lt "$c_copy" ] && [ "$c_copy" -lt "$c_rm" ] && [ "$c_rm" -lt "$c_import" ] && [ "$c_import" -lt "$c_apply" ]; then
  ok "photos move: new bucket, copy, forget the old one in the state, take the new one in, and only then apply"
else
  bad "photos move: wrong order (create=$c_create copy=$c_copy rm=$c_rm import=$c_import apply=$c_apply)"
fi
lacks "photos move: the earlier bucket is never deleted" "$calls" "storage rm --recursive gs://$earlier_photos"
contains "photos move: says which bucket was left" "$out" "gs://$earlier_photos"
contains "photos move: says it is copying" "$out" 'copying your data under the new bucket names'
[ "$(grep -v '^[[:space:]]*$' <<< "$out" | tail -n 1)" = "$url" ] && ok "photos move: still ends on the URL" || bad "photos move: the last line is not the URL"

run_setup earlier_photos_secret FAKE_PROJECT_BUCKETS="$earlier_photos" FAKE_OBJECTS="$earlier_photos/_backend/secret.json"
contains "photos move: a bucket the backend wrote to is copied even when the state has none" "$calls" "gcloud storage rsync --recursive gs://$earlier_photos gs://$new_photos"
lacks "photos move: ... and there is nothing to forget in the state" "$calls" 'terraform state rm'
run_setup earlier_photos_foreign FAKE_PROJECT_BUCKETS="$earlier_photos"
lacks "photos move: a bucket that only has the name (empty, not in the state) is left alone" "$calls" 'storage rsync'
run_setup earlier_photos_state_only FAKE_PROJECT_BUCKETS="$earlier_photos $new_photos" FAKE_STATE_PHOTOS="$earlier_photos"
lacks "photos move: a copy that was already made is not made again" "$calls" 'storage rsync'
contains "photos move: ... but a run cut short before the state moved is finished" "$calls" "google_storage_bucket.photos $new_photos"
run_setup earlier_photos_done FAKE_PROJECT_BUCKETS="$earlier_photos $new_photos" FAKE_STATE_PHOTOS="$new_photos"
lacks "photos move: a bucket that already moved is not touched" "$calls" 'terraform state rm'
lacks "photos move: ... and not copied" "$calls" 'storage rsync'
lacks "photos move: ... and not announced again" "$out" 'with the earlier names'
run_setup earlier_photos_fails FAKE_PROJECT_BUCKETS="$earlier_photos" FAKE_STATE_PHOTOS="$earlier_photos" FAKE_RSYNC_EXIT=1
[ "$rc" -ne 0 ] && ok "photos move: a failed copy stops the run" || bad "photos move: a failed copy did not stop it"
contains "photos move: the half-made bucket is removed, so the next run starts again" "$calls" "storage rm --recursive gs://$new_photos"
lacks "photos move: the state is not touched after a failed copy" "$calls" 'terraform state rm'
[ ! -f "$work/run_earlier_photos_fails/apply_count" ] && ok "photos move: nothing was applied after a failed copy" || bad "photos move: applied after a failed copy"

# ---------------------------------------------------------------- the banner's colours
# shellcheck source=setup_ui.sh
source "$here/setup_ui.sh"
plain_art="$(printf '%s\n' "${SP_ART[@]}")"
strip_escapes() { sed $'s/\033\\[[0-9;]*m//g'; }

SP_COLOR=none
none_frame="$(ui_banner_frame 0)"
[ "$none_frame" = "$plain_art" ] && ok "banner: plain when colour is off" || bad "banner: plain output differs from the art"
lacks "banner: no escape codes when colour is off" "$none_frame" $'\033'

SP_COLOR=truecolor
tc0="$(ui_banner_frame 0)"
tc1="$(ui_banner_frame 3)"
contains "banner: truecolor uses 24-bit codes" "$tc0" $'\033[38;2;'
[ "$(strip_escapes <<< "$tc0")" = "$plain_art" ] && ok "banner: the letters are unchanged under the colours" || bad "banner: colouring altered the art"
[ "$tc0" != "$tc1" ] && ok "banner: the colours move from frame to frame" || bad "banner: two phases look the same"

SP_COLOR=256
c256="$(ui_banner_frame 0)"
contains "banner: 256-colour mode uses 256 codes" "$c256" $'\033[38;5;'
[ "$(strip_escapes <<< "$c256")" = "$plain_art" ] && ok "banner: 256-colour mode keeps the art" || bad "banner: 256-colour mode altered the art"

# The gradient runs from the landing page's blue (#3b82f6) to its violet (#a78bfa).
ui_gradient 0;   [ "$SP_R,$SP_G,$SP_B" = "59,130,246" ] && ok "gradient: starts at blue" || bad "gradient start: $SP_R,$SP_G,$SP_B"
ui_gradient 50;  [ "$SP_R,$SP_G,$SP_B" = "99,102,241" ] && ok "gradient: passes indigo" || bad "gradient middle: $SP_R,$SP_G,$SP_B"
ui_gradient 100; [ "$SP_R,$SP_G,$SP_B" = "167,139,250" ] && ok "gradient: ends at violet" || bad "gradient end: $SP_R,$SP_G,$SP_B"
unset SP_COLOR

# The explanation of a protected Cloud Run service names the region and the command.
SP_PROJECT=hoge-vfltdc
SP_REGION=asia-northeast1
protect="$(ui_explain "Error: cannot destroy service without setting deletion_protection=false")"
contains "protected service: names the old service" "$protect" 'gcloud run services delete chamagon-backend --region=asia-northeast1 --project=hoge-vfltdc --quiet'
contains "protected service: says the data stays" "$protect" 'Your photos, groups and members stay'

# ---------------------------------------------------------------- on a terminal
# The colours and the spinner only exist on a terminal, so this runs setup.sh in a
# pseudo-terminal (it was a bug that they never showed on a real one).
tty_python=""
for candidate in python3 python; do
  if "$candidate" -c 'import pty, os; os.waitstatus_to_exitcode' > /dev/null 2>&1; then tty_python="$candidate"; break; fi
done
if [ -z "$tty_python" ]; then
  echo "skip terminal tests: no python with the pty module here"
else
  d="$work/tty"
  mkdir -p "$d/scripts" "$d/bin"
  cp "$here"/*.sh "$d/scripts/"
  cp "$work/bin/gcloud" "$work/bin/terraform" "$d/bin/" # no stand-in sleep: the spinner needs real time
  : > "$d/calls"
  "$tty_python" "$here/setup_tty_test.py" "$d/scripts/setup.sh" "$d/bin" "$d" || fail=1
fi

# SETUP_TEST_SHOW=1 bash setup_test.sh shows what the owner sees in two cases.
if [ -n "${SETUP_TEST_SHOW:-}" ]; then
  echo; echo "================ what the owner sees: success ================"; echo "$out_success"
  echo; echo "================ what the owner sees: no billing account ================"; echo "$out_billing"
fi

exit "$fail"
