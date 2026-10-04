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
#   3. Runs `terraform apply`. It prints backend_url at the end.
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
region="$(region_for "$area")"

state_bucket="${project_id}-tfstate"

# New projects normally have Cloud Storage on already; this makes sure.
gcloud services enable storage.googleapis.com --project="$project_id"

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

terraform init -input=false
terraform apply -auto-approve \
  -var="project_id=${project_id}" \
  -var="region=${region}"
