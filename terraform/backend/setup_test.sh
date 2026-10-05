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
payload="$(printf '%s' '{"sub":"110571000531995686849","email":"a@b.c"}' | base64 | tr -d '\n=' | tr '+/' '-_')"
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
  "storage buckets") if [ "\$3" = "describe" ]; then exit "\${FAKE_BUCKET_DESCRIBE_EXIT:-1}"; fi ;;
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
    n_file="$FAKE_DIR/apply_count"; n="$(( $(cat "$n_file" 2>/dev/null || echo 0) + 1 ))"; echo "$n" > "$n_file"
    case "${FAKE_APPLY:-ok}" in
      ok) echo "Apply complete! Resources: 9 added."; exit 0 ;;
      flaky) if [ "$n" -lt 2 ]; then echo "Error 403: Service Usage API has not been used (SERVICE_DISABLED)"; exit 1; fi; echo "Apply complete!"; exit 0 ;;
      always_disabled) echo "Error 403: SERVICE_DISABLED"; exit 1 ;;
      other) echo "Error: something else entirely"; exit 1 ;;
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
  rm -f "$d/apply_count"
  log="$d/setup.log"
  out="$(env "$@" FAKE_CALLS="$d/calls" FAKE_DIR="$d" SHAREPI_SETUP_LOG="$log" SP_PROGRESS_INTERVAL=0.05 \
    PATH="$work/bin:$PATH" HOME="$d" bash "$d/scripts/setup.sh" hoge-vfltdc ea 2>&1)"
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
contains "success: says what to do next" "$out" 'register the URL above'
contains "success: names the log" "$out" "$log"
lacks "success: no command output on the screen" "$out" 'Terraform has been successfully initialized'
lacks "success: no resource lines on the screen" "$out" 'Creating...'
contains "success: command output is in the log" "$(cat "$log")" 'Terraform has been successfully initialized'
contains "success: the user id was passed on" "$calls" 'admin_subs=["110571000531995686849"]'

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

# ---------------------------------------------------------------- the state bucket
run_setup bucket_new FAKE_BUCKET_DESCRIBE_EXIT=1
contains "state bucket: created when missing" "$calls" 'gcloud storage buckets create gs://hoge-vfltdc-tfstate'
contains "state bucket: says so" "$out" 'ok (created)'
run_setup bucket_old FAKE_BUCKET_DESCRIBE_EXIT=0
lacks "state bucket: not created again" "$calls" 'buckets create'
contains "state bucket: says so" "$out" 'ok (already there)'

# SETUP_TEST_SHOW=1 bash setup_test.sh shows what the owner sees in two cases.
if [ -n "${SETUP_TEST_SHOW:-}" ]; then
  echo; echo "================ what the owner sees: success ================"; echo "$out_success"
  echo; echo "================ what the owner sees: no billing account ================"; echo "$out_billing"
fi

exit "$fail"
