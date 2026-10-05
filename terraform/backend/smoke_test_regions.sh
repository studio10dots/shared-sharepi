#!/usr/bin/env bash
# Smoke-tests terraform/backend/ once per region region.sh offers: apply,
# confirm the backend answers over HTTPS, then destroy — regardless of
# whether the apply or the HTTPS check succeeded. Meant to be run after a
# change to main.tf/variables.tf, to catch a region that does not actually
# support one of the resources this module creates (Cloud Run v2, Autoclass,
# IAM Credentials signBlob) before an owner hits it in docs/SETUP.md.
#
#   bash smoke_test_regions.sh <project_id> [backend_image]
#
# <project_id> is never your production project if you have one deployed
# there already: this creates and destroys a real bucket/service account/
# Cloud Run service/log-bucket-config in it, once per region, in sequence.
# Use a disposable or test project.
#
# [backend_image] defaults to Google's public "hello" sample
# (us-docker.pkg.dev/cloudrun/container/hello), which needs no setup of your
# own: this script only checks that the *infrastructure* deploys and answers
# over HTTPS in each region, not your backend's own logic, so a placeholder
# image is enough. Pass your own image to test with it instead; either way,
# GOOGLE_CLIENT_ID is set to a placeholder, since nothing here signs in.
#
# Runs in an isolated temp copy of the *.tf files, with its own fresh
# Terraform state — never in this directory, so it can never touch a real
# deployment's terraform.tfstate (gitignored, so `git status` will not show
# one even if it exists here from a real `terraform apply`).
#
# Set CODES to a space-separated subset of region.sh's codes to re-check only
# those (e.g. `CODES="me af" bash smoke_test_regions.sh my-project`).
#
# A Cloud Run "Project failed to initialize in this region due to quota
# exceeded" on one of the later regions is likely a per-project limit on how
# many regions a project can start using in a short time, not that region
# being unsupported: re-run just that region on its own (CODES="sa") or in
# another project before concluding anything.
#
# Requires: gcloud (already authenticated with billing-project access),
# terraform, curl.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

project_id="${1:-}"
backend_image="${2:-us-docker.pkg.dev/cloudrun/container/hello}"
google_client_id="smoke-test-placeholder.apps.googleusercontent.com"
codes=(${CODES:-ea ca au me af eu na sa})

if [ -z "$project_id" ]; then
  echo "usage: bash smoke_test_regions.sh <project_id> [backend_image]" >&2
  exit 2
fi

if [ "${SKIP_CONFIRM:-}" != "1" ]; then
  echo "This creates and destroys real resources in GCP project '$project_id',"
  echo "once per region (${#codes[@]} regions: ${codes[*]})."
  read -r -p "Continue? [y/N] " reply
  case "$reply" in
    y|Y|yes|YES) ;;
    *) echo "Aborted."; exit 1 ;;
  esac
fi

workdir="$(mktemp -d)"
echo "Working copy and logs: $workdir"
cp "$SCRIPT_DIR"/*.tf "$workdir"/
# shellcheck source=region.sh
source "$SCRIPT_DIR/region.sh"

cd "$workdir"
if ! terraform init -input=false > init.log 2>&1; then
  echo "terraform init failed:"
  cat init.log
  exit 1
fi

declare -A apply_result http_result destroy_result

for code in "${codes[@]}"; do
  region="$(region_for "$code")" || { echo "skipping unknown code $code"; continue; }
  echo
  echo "==== $code ($region) ===="

  # A fresh bucket name per region: reusing one right after deleting it can
  # make the IAM binding that follows the bucket's creation fail with a 404
  # ("bucket does not exist") from GCS's eventual consistency, which would be
  # reported here as a region problem when it is not.
  bucket="${project_id}-smoke-${code}-$(date +%s | tail -c 6)"

  tf_args=(
    -auto-approve -input=false
    -var="project_id=$project_id"
    -var="region=$region"
    -var="bucket_name=$bucket"
    # Saved into the state at apply time: a destroy with protection left on
    # fails, leaves the service running, and breaks the next region's apply.
    -var="deletion_protection=false"
    -var="backend_image=$backend_image"
    -var="google_client_id=$google_client_id"
  )

  apply_ok=1
  if terraform apply "${tf_args[@]}" > "apply_$code.log" 2>&1; then
    apply_result[$code]="ok"
  else
    apply_result[$code]="FAIL (see $workdir/apply_$code.log)"
    apply_ok=0
    echo "apply failed:"
    tail -n 20 "apply_$code.log"
  fi

  if [ "$apply_ok" = 1 ]; then
    url="$(terraform output -raw backend_url 2>/dev/null)"
    if [ -n "$url" ]; then
      status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 "$url/ping" || echo "000")"
      if [ "$status" = "200" ]; then
        http_result[$code]="ok (200)"
      else
        http_result[$code]="FAIL (http $status)"
        echo "HTTPS check got status $status from $url/ping"
      fi
    else
      http_result[$code]="FAIL (no backend_url output)"
    fi
  else
    http_result[$code]="skipped"
  fi

  # Always destroy, whatever happened above: the whole point is leaving
  # nothing billing for after a failed region, same as a region that worked.
  if terraform destroy "${tf_args[@]}" > "destroy_$code.log" 2>&1; then
    destroy_result[$code]="ok"
  else
    destroy_result[$code]="FAIL — CHECK THE CONSOLE FOR LEFTOVER RESOURCES (see $workdir/destroy_$code.log)"
    echo "destroy failed:"
    tail -n 20 "destroy_$code.log"
  fi
done

echo
echo "==== summary ===="
printf '%-4s %-22s %-8s %-16s %s\n' code region apply http destroy
for code in "${codes[@]}"; do
  printf '%-4s %-22s %-8s %-16s %s\n' \
    "$code" "$(region_for "$code" 2>/dev/null || echo "?")" \
    "${apply_result[$code]:-skipped}" "${http_result[$code]:-skipped}" "${destroy_result[$code]:-skipped}"
done
echo
echo "Logs kept at: $workdir (delete it yourself once you are done reading them)"
echo
echo "Note: google_logging_project_bucket_config adopts the project's single"
echo "built-in _Default log bucket rather than creating one; terraform destroy"
echo "cannot delete that bucket itself, only stop managing its retention_days,"
echo "so it is not a resource leak if that part shows up in a destroy log."
