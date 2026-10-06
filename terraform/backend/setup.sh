#!/usr/bin/env bash
# Sets up, or updates, an owner's backend in one go. docs/SETUP.md and the app's
# setup guide give the one command that runs it from a fresh clone:
#
#   bash setup.sh <project_id> <area code> [language]
#
# The language is optional: "ja" for Japanese, anything else (or nothing) for the
# language the shell is set to, else English.
#
# What it does, in order (each is one line on the screen; everything the commands
# print goes to a log file, and a failure says what to do next: setup_ui.sh):
#   1. Checks the project exists and has a billing account linked.
#   2. Makes sure Terraform is there (Cloud Shell no longer ships it).
#   3. Turns on the Google Cloud APIs the stack needs.
#   4. Makes a small bucket "<project_id>-tfstate" for Terraform's state if it is
#      not there yet, and points Terraform at it (backend.tf, written here and
#      never committed). The state lives in that bucket rather than in Cloud
#      Shell's home, which is not permanent: when the home is reset, running the
#      same command again finds the state and changes only what needs changing
#      (no "already exists" errors, nothing to import).
#   5. Finds the Google user id of the account running this script and passes it
#      as admin_subs, so the backend recognises its administrator by that id
#      (which never changes hands) rather than by email. If the id cannot be
#      read it says so and the backend falls back to the email.
#   6-7. terraform init, then terraform apply.
#   8. Reads the backend's URL and prints it as the very last line.
#
# The command is the same for the first setup and for every update: each run
# starts from a fresh clone of the repository, so it brings the newest
# backend_image with it. Use the same area code every time; another one would
# move the backend to another region.

set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "usage: bash setup.sh <project_id> <area code> [language]" >&2
  exit 2
fi

project_id="$1"
area="$2"
if [ "$#" -eq 3 ]; then
  export SHAREPI_LANG="$3"
fi

# Same rule as variables.tf, checked here first because a bucket is created
# from the ID before Terraform ever runs.
if ! [[ "$project_id" =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ ]]; then
  echo "project_id must be the project's ID (6-30 lowercase letters, digits or hyphens), not its display name." >&2
  exit 2
fi

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=region.sh
source ./region.sh
# shellcheck source=google_user_id.sh
source ./google_user_id.sh
# shellcheck source=ensure_terraform.sh
source ./ensure_terraform.sh
# shellcheck source=setup_ui.sh
source ./setup_ui.sh

if ! region="$(region_for "$area")"; then
  exit 2
fi
state_bucket="${project_id}-tfstate"

# Plain text in the log, and no questions from Terraform.
export TF_IN_AUTOMATION=1
export TF_CLI_ARGS="-no-color"

SP_PROJECT="$project_id"
SP_REGION="$region"
ui_lang
ui_init
ui_total 8

# ---- 1. the project
ui_step "$(ui_t step_project)"
ui_run gcloud projects describe "$project_id" --format='value(projectId)' || ui_fail
# A project with no billing account is the most common first failure, and it is
# cheaper to say so here than to let the first API call fail. If the answer
# cannot be had (the Billing API may be off), carry on: a later step reports it.
billing="$(gcloud billing projects describe "$project_id" --format='value(billingEnabled)' 2>> "$SP_LOG" || echo unknown)"
case "$billing" in
  True | true | unknown) ;;
  *)
    echo "BILLING_DISABLED: no billing account is linked to project $project_id" >> "$SP_LOG"
    ui_fail
    ;;
esac
ui_done

# ---- 2. Terraform
# Cloud Shell's stand-in for Terraform exits successfully, so a missing one has
# to be caught here, before anything is created.
ui_step "$(ui_t step_terraform)"
ui_run ensure_terraform || ui_fail
ui_done

# ---- 3. APIs
# The APIs the stack needs, turned on here and not only by Terraform: Terraform
# itself needs the Service Usage API to list and enable services, and a new
# project can have none of them on (one made through the API has no default
# APIs at all). Keep this list in step with google_project_service.required in
# main.tf, plus Service Usage and Cloud Resource Manager, which Terraform needs.
ui_step "$(ui_t step_apis)"
ui_run gcloud services enable \
  serviceusage.googleapis.com \
  cloudresourcemanager.googleapis.com \
  storage.googleapis.com \
  logging.googleapis.com \
  run.googleapis.com \
  iam.googleapis.com \
  iamcredentials.googleapis.com \
  --project="$project_id" || ui_fail
ui_done

# ---- 4. where Terraform keeps its state
ui_step "$(ui_t step_state)"
if ! gcloud storage buckets describe "gs://${state_bucket}" --project="$project_id" > /dev/null 2>&1; then
  ui_run gcloud storage buckets create "gs://${state_bucket}" \
    --project="$project_id" \
    --location="$region" \
    --uniform-bucket-level-access \
    --public-access-prevention || ui_fail
  # Keeps the earlier states, so a bad apply can be recovered from.
  ui_run gcloud storage buckets update "gs://${state_bucket}" --versioning || ui_fail
  created="$(ui_t created)"
else
  created="$(ui_t already_there)"
fi

cat > backend.tf << EOF
terraform {
  backend "gcs" {
    bucket = "${state_bucket}"
    prefix = "sharepi-backend"
  }
}
EOF
ui_done "$created"

# ---- 5. the administrator
# The administrator is whoever runs this: they hold the GCP contract. Pin that
# to their Google user id; without it the backend uses their email instead.
ui_step "$(ui_t step_admin)"
admin_sub="$(google_user_id 2>> "$SP_LOG" || true)"
admin_args=()
if [ -n "$admin_sub" ]; then
  admin_args=(-var="admin_subs=[\"${admin_sub}\"]")
  ui_done "$(ui_t admin_account)"
else
  ui_done "$(ui_t admin_no_id)"
  ui_t admin_no_id_note
  echo
fi

# ---- 6. terraform init
ui_step "$(ui_t step_init)"
ui_run terraform init -input=false || ui_fail
ui_done

# ---- 7. terraform apply
# An API enabled a moment ago can still answer "not used before or disabled" from
# some parts of Google for a minute or two. That is the only failure worth
# waiting out; any other error stops at once, and so does a third attempt.
ui_step "$(ui_t step_apply)"
attempt=1
while true; do
  mark="$(wc -c < "$SP_LOG" | tr -d ' ')"
  if ui_run_progress terraform apply -auto-approve \
    -var="project_id=${project_id}" \
    -var="region=${region}" \
    ${admin_args[@]+"${admin_args[@]}"}; then
    break
  fi
  if [ "$attempt" -lt 3 ] && tail -c +"$((mark + 1))" "$SP_LOG" | grep -q "SERVICE_DISABLED"; then
    ui_t retrying
    sleep 60
    attempt=$((attempt + 1))
    continue
  fi
  ui_fail
done
ui_done

# What this run did to the backend: made it, left it as it was, or changed it.
apply_output="$(tail -c +"$((mark + 1))" "$SP_LOG")"
if grep -q 'google_cloud_run_v2_service.backend: Creation complete' <<< "$apply_output"; then
  outcome="created"
elif grep -qE 'Resources: 0 added, 0 changed, 0 destroyed|No changes\.' <<< "$apply_output"; then
  outcome="unchanged"
else
  outcome="updated"
fi

# ---- 8. the result
ui_step "$(ui_t step_result)"
backend_url="$(terraform output -raw backend_url 2>> "$SP_LOG")" || ui_fail
admin_check="$(terraform output -raw administrator_check 2>> "$SP_LOG" || echo "unknown")"
ui_done

ui_final "$outcome" "$backend_url" "$admin_check"
