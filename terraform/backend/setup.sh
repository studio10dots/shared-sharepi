#!/usr/bin/env bash
# Sets up, or updates, an owner's backend in one go. docs/SETUP.md and the app's
# setup guide give the one command that runs it from a fresh clone:
#
#   bash setup.sh <project_id> <area code>
#
# What it does, in order:
#   1. Makes a small bucket "<project_id>-tfstate" for Terraform's state if it is
#      not there yet. The state lives in that bucket rather than in Cloud
#      Shell's home, which is not permanent: when the home is reset, running the
#      same command again finds the state and changes only what needs changing
#      (no "already exists" errors, nothing to import).
#   2. Points Terraform at it (backend.tf, written here and never committed).
#   3. Finds the Google user id of the account running this script and passes it
#      as admin_subs, so the backend recognises its administrator by that id
#      (which never changes hands) rather than by email. If the id cannot be
#      read it says so and the backend falls back to the email.
#   4. Runs `terraform apply`. It prints backend_url at the end.
#
# The command is the same for the first setup and for every update: each run
# starts from a fresh clone of the repository, so it brings the newest
# backend_image with it. Use the same area code every time; another one would
# move the backend to another region.

set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: bash setup.sh <project_id> <area code>" >&2
  exit 2
fi

project_id="$1"
area="$2"

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
region="$(region_for "$area")"

# Before anything is created: Cloud Shell no longer has Terraform, and its
# stand-in exits successfully, so a missing Terraform must be caught here.
if ! ensure_terraform; then
  echo "Terraform is needed; see https://developer.hashicorp.com/terraform/install" >&2
  exit 1
fi

state_bucket="${project_id}-tfstate"

# The APIs the stack needs, turned on here and not only by Terraform: Terraform
# itself needs the Service Usage API to list and enable services, and a new
# project can have none of them on (one made through the API has no default
# APIs at all). Keep this list in step with google_project_service.required in
# main.tf, plus Service Usage and Cloud Resource Manager, which Terraform needs.
gcloud services enable \
  serviceusage.googleapis.com \
  cloudresourcemanager.googleapis.com \
  storage.googleapis.com \
  logging.googleapis.com \
  run.googleapis.com \
  iam.googleapis.com \
  iamcredentials.googleapis.com \
  --project="$project_id"

if ! gcloud storage buckets describe "gs://${state_bucket}" --project="$project_id" > /dev/null 2>&1; then
  gcloud storage buckets create "gs://${state_bucket}" \
    --project="$project_id" \
    --location="$region" \
    --uniform-bucket-level-access \
    --public-access-prevention
  # Keeps the earlier states, so a bad apply can be recovered from.
  gcloud storage buckets update "gs://${state_bucket}" --versioning
fi

cat > backend.tf << EOF
terraform {
  backend "gcs" {
    bucket = "${state_bucket}"
    prefix = "sharepi-backend"
  }
}
EOF

# The administrator is whoever runs this: they hold the GCP contract. Pin that
# to their Google user id; without it the backend uses their email instead.
admin_sub="$(google_user_id || true)"
admin_args=()
if [ -n "$admin_sub" ]; then
  admin_args=(-var="admin_subs=[\"${admin_sub}\"]")
else
  echo "Note: could not read your Google user id, so the administrator is recognised by email only." >&2
  echo "      Run this command again later, or set admin_subs (see terraform.tfvars.example)." >&2
fi

terraform init -input=false

# An API enabled a moment ago can still answer "not used before or disabled" from
# some parts of Google for a minute or two. That is the only failure worth
# waiting out; any other error stops at once, and so does a third attempt.
log="$(mktemp)"
attempt=1
until terraform apply -auto-approve \
  -var="project_id=${project_id}" \
  -var="region=${region}" \
  ${admin_args[@]+"${admin_args[@]}"} 2>&1 | tee "$log"; do
  if [ "$attempt" -ge 3 ] || ! grep -q "SERVICE_DISABLED" "$log"; then
    rm -f "$log"
    exit 1
  fi
  echo "An API that was just enabled is not active everywhere yet. Trying again in 60 seconds (attempt $((attempt + 1)) of 3)..." >&2
  sleep 60
  attempt=$((attempt + 1))
done
rm -f "$log"
