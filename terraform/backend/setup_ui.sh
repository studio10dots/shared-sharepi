# What setup.sh shows the owner, and where everything else goes.
#
# The screen carries only the banner and one line per step. Everything the
# commands print (gcloud, terraform) is appended to a log file instead, and when a
# step fails the owner gets what happened, what to do next (with commands they
# can copy), the last lines of that step's output and the log's path.
#
# Source this file. setup.sh sets SP_PROJECT and calls, in order: ui_init,
# ui_total, then per step ui_step / ui_run (or ui_run_progress) / ui_done, and
# ui_fail when a step fails.
#
# The messages are English: this runs in Cloud Shell for owners everywhere.

SP_PROJECT=""
SP_LOG=""
SP_TOTAL=0
SP_N=0
SP_STEP_OFFSET=0
SP_KNOWN=1

ui_banner() {
  cat << 'BANNER'

   _____ __                    ____  _
  / ___// /_  ____ _________  / __ \(_)
  \__ \/ __ \/ __ `/ ___/ _ \/ /_/ / /
 ___/ / / / / /_/ / /  /  __/ ____/ /
/____/_/ /_/\__,_/_/   \___/_/   /_/

BANNER
}

# The banner, and the log file the rest of the output goes to.
ui_init() {
  SP_LOG="${SHAREPI_SETUP_LOG:-$HOME/sharepi-setup-$(date +%Y%m%d-%H%M%S).log}"
  mkdir -p "$(dirname "$SP_LOG")"
  : > "$SP_LOG"
  chmod 600 "$SP_LOG" 2> /dev/null || true # it holds the project and the account's id
  ui_banner
  echo "Setting up your SharePi backend. The details are written to:"
  echo "  $SP_LOG"
  echo
}

ui_total() { SP_TOTAL="$1"; }

# Starts a step: "[2/7] Doing a thing ... " with no newline; ui_done or ui_fail
# ends the line.
ui_step() {
  SP_N=$((SP_N + 1))
  printf '=== [%d/%d] %s\n' "$SP_N" "$SP_TOTAL" "$1" >> "$SP_LOG"
  SP_STEP_OFFSET="$(wc -c < "$SP_LOG" | tr -d ' ')"
  printf '[%d/%d] %s ... ' "$SP_N" "$SP_TOTAL" "$1"
}

ui_done() { echo "ok${1:+ ($1)}"; }

# Runs a command with everything it prints going to the log. Functions run in
# this shell, so a PATH change they make stays.
ui_run() { "$@" >> "$SP_LOG" 2>&1; }

# The same, for a long command: a dot on the screen every few seconds, so it is
# clear that it is working.
ui_run_progress() {
  "$@" >> "$SP_LOG" 2>&1 &
  local pid=$!
  while kill -0 "$pid" 2> /dev/null; do
    printf '.'
    sleep "${SP_PROGRESS_INTERVAL:-5}"
  done
  wait "$pid"
}

# What the current step has written to the log so far.
ui_step_log() { tail -c +"$((SP_STEP_OFFSET + 1))" "$SP_LOG"; }

# Prints "what happened" and "what to do" for the text of a failed step ($1).
# The first pattern that matches wins, so the specific ones come first.
ui_explain() {
  local text="$1" p="${SP_PROJECT:-<PROJECT_ID>}"
  SP_KNOWN=1 # cleared below when nothing matched: then the raw lines are shown too

  if grep -qiE 'billing account.*(disabled|absent)|BILLING_DISABLED|billing must be enabled|billing has not been enabled|requires billing' <<< "$text"; then
    cat << EOF
What happened
  This project has no billing account linked, and Google Cloud will not create
  anything without one. Nothing has been created yet.

What to do
  1. Open https://console.cloud.google.com/billing/linkedaccount?project=$p
  2. Click "Link a billing account" and choose your billing account.
     (No billing account yet? Create one at https://console.cloud.google.com/billing
     and link it to the project.)
  Or here, in Cloud Shell:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

Then run the same setup command again. It is safe to repeat.
EOF
  elif grep -qE 'cannot destroy service without setting deletion_protection' <<< "$text"; then
    cat << EOF
What happened
  An existing Cloud Run service has to be replaced, and it is protected against
  deletion.

What to do
  Delete it by hand, then run the same setup command again:
     gcloud run services list --project=$p
     gcloud run services delete <SERVICE_NAME> --region=<REGION> --project=$p
EOF
  elif grep -qiE 'Terraform is not installed|installed Terraform does not run|does not match the expected checksum|Could not download' <<< "$text"; then
    cat << EOF
What happened
  Terraform is not installed in this Cloud Shell, and it could not be fetched
  automatically.

What to do
  Install it by following https://developer.hashicorp.com/terraform/install,
  then run the same setup command again.
EOF
  elif grep -qiE 'SERVICE_DISABLED|has not been used in project|accessNotConfigured' <<< "$text"; then
    cat << EOF
What happened
  A Google Cloud API that was turned on a moment ago is not active everywhere yet.
  This usually clears within a few minutes.

What to do
  Wait two or three minutes, then run the same setup command again.
EOF
  elif grep -qiE 'quota.*exceeded|RESOURCE_EXHAUSTED|failed to initialize in this region' <<< "$text"; then
    cat << EOF
What happened
  Google Cloud refused to start Cloud Run in this region for now (a per-project
  limit on how many regions a project may start using in a short time).

What to do
  Wait a few minutes and run the same setup command again. If it keeps
  happening, try a different area code (the second argument) in a new project.
EOF
  elif grep -qiE 'bucket.*(already exists|already own|not available)|409.*bucket|The requested bucket name is not available' <<< "$text"; then
    cat << EOF
What happened
  The name for the setup-state bucket ("$p-tfstate") is already taken by
  another project. Bucket names are shared by everyone on Google Cloud.

What to do
  Use a project whose ID has not been used for a bucket before. Do not try to
  reuse a bucket you do not own.
EOF
  elif grep -qiE 'project.*(not found|does not exist)|may not exist|Could not find project|invalid project' <<< "$text"; then
    cat << EOF
What happened
  Google Cloud cannot find a project with the ID "$p", or this account cannot see it.

What to do
  1. List the projects this account can see, and copy the ID (not the name):
     gcloud projects list
  2. Make sure Cloud Shell is signed in with the account that created the project.
  3. Run the setup command again with the right project ID.
EOF
  elif grep -qiE 'permission|PERMISSION_DENIED|forbidden|403' <<< "$text"; then
    cat << EOF
What happened
  This account is not allowed to do that in project "$p".

What to do
  1. Use the account that created the project (or that has the Owner role on it).
     It is shown at the top right of the Cloud Shell window.
  2. Check that the project ID is the one you meant:
     gcloud projects list
  3. Run the same setup command again.
EOF
  elif grep -qiE 'Could not resolve|Connection refused|i/o timeout|TLS handshake|Temporary failure|no such host|network is unreachable' <<< "$text"; then
    cat << EOF
What happened
  A network request failed.

What to do
  Wait a minute and run the same setup command again.
EOF
  else
    SP_KNOWN=0
    cat << EOF
What happened
  A step failed, and the cause is not one this script knows.

What to do
  Run the same setup command again; many failures clear on a second try. If it
  fails the same way, send the log file named below to the person who gave you
  this command. It does not contain passwords or tokens.
EOF
  fi
}

# Ends the current step as failed: the explanation, the last lines of what the
# step printed, the log's path. Stops the script.
ui_fail() {
  echo "failed"
  echo
  ui_explain "$(ui_step_log)"
  echo
  # For a cause the script knows, the explanation is enough; for the rest the
  # raw lines are what someone needs to see.
  if [ "$SP_KNOWN" -eq 0 ]; then
    echo "The last lines of what the step printed:"
    ui_step_log | grep -v '^[[:space:]]*$' | tail -n 8 | cut -c1-200 | sed 's/^/  | /'
    echo
  fi
  echo "Full log: $SP_LOG"
  exit 1
}
