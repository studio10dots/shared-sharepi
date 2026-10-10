#!/usr/bin/env bash
# Sets up, or updates, an owner's SharePi backend on AWS in one go, from AWS
# CloudShell. It is the AWS counterpart of ../backend/setup.sh and shows the same
# screen (the gradient banner, one line per step, a failure that says what to do).
# The app's setup guide gives the one command that runs it from a fresh clone:
#
#   bash setup.sh <area code> <google_user_id> [language]
#
# <area code>       one of region.sh's 2-letter codes (ea, ca, au, me, af, eu, na, sa).
# <google_user_id>  the owner: the Google account that is the backend's
#                   administrator, as the id (10-30 digits) the app's setup guide
#                   puts in the command. It is required, unlike on Google Cloud,
#                   because an AWS sign-in is not a Google account and there is
#                   nothing to read it from: the owner is chosen explicitly.
# [language]        "ja" for Japanese; anything else (or nothing) for the language
#                   the shell is set to, else English.
#
# There is no project to name: the AWS account CloudShell is signed in to is the
# one that is used.
#
# What it does, in order (each is one line on the screen; everything the commands
# print goes to a log file, and a failure says what to do next):
#   1. Checks this shell has the tools, a working AWS sign-in and enough room.
#   2. Clears what an earlier run left that is no longer needed, and puts
#      Terraform's downloads in /tmp rather than the 1 GB home directory.
#   3. Makes sure Terraform is there.
#   4. Gets the backend program ("bootstrap", next to this file, or downloaded and
#      checked against a SHA-256 from program.env).
#   5. Makes a small bucket for Terraform's state if it is not there, and points
#      Terraform at it (backend.tf, written here and never committed): the state
#      survives CloudShell being reset, so running this again only changes what
#      needs changing.
#   6. Terraform init, 7. apply, 8. reads the backend's URL and prints it as the
#      very last line.
#
# The command is the same for the first setup and every update. Use the same area
# code every time; another one would move the backend to another Region.

set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "usage: bash setup.sh <area code> <google_user_id> [language]" >&2
  echo "  <google_user_id> is the owner's Google user id (10-30 digits): copy the" >&2
  echo "  whole command from the SharePi app's setup guide, which includes it." >&2
  exit 2
fi

area="$1"
admin_sub="$2"
if [ "$#" -eq 3 ]; then
  export SHAREPI_LANG="$3"
fi

# Typed into a Terraform variable, so only a plain Google user id gets through.
if ! [[ "$admin_sub" =~ ^[0-9]{10,30}$ ]]; then
  echo "google_user_id must be 10-30 digits (the owner's Google user id), not an email address." >&2
  exit 2
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"
# shellcheck source=region.sh
source ./region.sh
region="$(region_for "$area")"

# The screen and Terraform's installer are the Google Cloud ones, unchanged.
# shellcheck source=../backend/setup_ui.sh
source ../backend/setup_ui.sh
# shellcheck source=../backend/ensure_terraform.sh
source ../backend/ensure_terraform.sh
# shellcheck source=setup_aws.sh
source ./setup_aws.sh
# shellcheck source=setup_aws_msg.sh
source ./setup_aws_msg.sh

# The AWS words for the failures the shared screen explains.
ui_explain_text() {
  local fn="ui_body_aws_en"
  if declare -F "ui_body_aws_$SP_LANG" > /dev/null; then fn="ui_body_aws_$SP_LANG"; fi
  printf '%s\n' "$(ui_t what_happened)"
  "$fn" "$1"
}
ui_explain() {
  local kind
  kind="$(aws_classify "$1")"
  SP_KNOWN=1
  if [ "$kind" = unknown ]; then SP_KNOWN=0; fi
  ui_explain_text "$kind"
}

# The shared ui_final, with the AWS note about who the administrator is.
aws_final() {
  echo
  ui_t_aws final_admin_aws "$3"
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

# ---- the program: found next to this file, or downloaded and checked
# program.env (the publisher's, committed) names where a built program is and its
# SHA-256; both can also be set in the environment.
[ -f ./program.env ] && source ./program.env
aws_get_program() {
  if [ -n "${SHAREPI_BACKEND_PROGRAM:-}" ] && [ -f "$SHAREPI_BACKEND_PROGRAM" ]; then
    program="$SHAREPI_BACKEND_PROGRAM"
    program_how=have
    return 0
  fi
  if [ -f ./bootstrap ]; then
    program="$here/bootstrap"
    program_how=have
    return 0
  fi
  if [ -z "${SHAREPI_BACKEND_URL:-}" ] || [ -z "${SHAREPI_BACKEND_SHA256:-}" ]; then
    echo "BACKEND_PROGRAM: not found next to setup.sh and no SHAREPI_BACKEND_URL / SHAREPI_BACKEND_SHA256 set" >&2
    return 1
  fi
  local tmp
  tmp="$(mktemp -d)"
  if ! curl -fsSL "$SHAREPI_BACKEND_URL" -o "$tmp/program.zip"; then
    echo "Could not download $SHAREPI_BACKEND_URL" >&2
    rm -rf "${tmp:?}"
    return 1
  fi
  if ! verify_sha256 "$tmp/program.zip" "$SHAREPI_BACKEND_SHA256"; then
    echo "The downloaded program does not match the expected checksum; refusing to use it." >&2
    rm -rf "${tmp:?}"
    return 1
  fi
  unzip -q -o "$tmp/program.zip" bootstrap -d "$here" || { rm -rf "${tmp:?}"; return 1; }
  rm -rf "${tmp:?}"
  chmod +x "$here/bootstrap"
  program="$here/bootstrap"
  program_how=downloaded
}

ui_lang
SP_REGION="$region"
ui_init
ui_total 8

# ---- 1. what this shell has
ui_step "$(ui_t_aws step_env)"
ui_run aws_preflight || ui_fail
SP_ACCOUNT="$(aws_account_id)"
echo "account: $SP_ACCOUNT region: $region" >> "$SP_LOG"
ui_done

# ---- 2. room
ui_step "$(ui_t_aws step_tidy)"
ui_run aws_clean_old "$here" || ui_fail
ui_done "$(ui_t_aws tidy_done "$TF_DATA_DIR")"

# ---- 3. Terraform
ui_step "$(ui_t step_terraform)"
ui_run ensure_terraform || ui_fail
ui_done

# ---- 4. the program
ui_step "$(ui_t_aws step_program)"
program=""
# aws_get_program sets $program, which a $(...) would lose: it runs here, and says
# what it did through a variable rather than its output.
program_how=""
ui_run aws_get_program || ui_fail
case "$program_how" in
  downloaded) ui_done "$(ui_t_aws program_downloaded)" ;;
  *) ui_done "$(ui_t_aws program_have)" ;;
esac

# ---- 5. where Terraform keeps its state
ui_step "$(ui_t step_state)"
state_bucket="${SHAREPI_STATE_BUCKET:-sharepi-tfstate-${SP_ACCOUNT}-${region}}"
created="$(aws_ensure_state_bucket "$state_bucket" "$region" 2>> "$SP_LOG")" || ui_fail
cat > backend.tf << EOF
terraform {
  backend "s3" {
    bucket       = "${state_bucket}"
    key          = "sharepi-backend/terraform.tfstate"
    region       = "${region}"
    encrypt      = true
    use_lockfile = true
  }
}
EOF
ui_done "$(ui_t "$created")"

# ---- 6. terraform init
# A state kept in this directory by an earlier run (before the bucket existed) is
# moved into the bucket, not left behind to be applied a second time.
ui_step "$(ui_t step_init)"
init_args=(-input=false)
if [ -f terraform.tfstate ]; then init_args+=(-migrate-state -force-copy); fi
ui_run_progress terraform init "${init_args[@]}" || ui_fail
ui_done

# ---- 7. terraform apply
ui_step "$(ui_t step_apply)"
mark="$(wc -c < "$SP_LOG" | tr -d ' ')"
ui_run_progress terraform apply -auto-approve -input=false \
  -var="region=${region}" \
  -var="backend_binary=${program}" \
  -var="admin_subs=[\"${admin_sub}\"]" || ui_fail
ui_done

# What this run did to the backend: made it, left it as it was, or changed it.
apply_output="$(tail -c +"$((mark + 1))" "$SP_LOG")"
if grep -q 'aws_lambda_function.backend: Creation complete' <<< "$apply_output"; then
  outcome="created"
elif grep -qE 'Resources: 0 added, 0 changed, 0 destroyed|No changes\.' <<< "$apply_output"; then
  outcome="unchanged"
else
  outcome="updated"
fi

# ---- 8. the result
ui_step "$(ui_t step_result)"
backend_url="$(terraform output -raw backend_url 2>> "$SP_LOG")" || ui_fail
admin_check="$(ui_t admin_account)"
ui_done

aws_final "$outcome" "$backend_url" "$admin_check"
