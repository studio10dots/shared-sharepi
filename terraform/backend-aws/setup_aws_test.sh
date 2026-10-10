#!/usr/bin/env bash
# Runs the AWS setup.sh end to end against stand-ins for aws, terraform and curl,
# and checks what the owner sees, what is left in the log, and what it deletes:
#   bash setup_aws_test.sh
# Nothing here touches AWS or the network.
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

SUB="100000000000000000001"

# --- stand-ins, steered by FAKE_* variables
mkdir "$work/bin"
cat > "$work/bin/aws" << 'EOF'
#!/usr/bin/env bash
echo "aws $*" >> "$FAKE_CALLS"
case "$1 $2" in
  "sts get-caller-identity")
    if [ -n "${FAKE_NO_LOGIN:-}" ]; then echo "Unable to locate credentials" >&2; exit 255; fi
    echo "123456789012"; exit 0 ;;
  "s3api head-bucket") exit "${FAKE_BUCKET_EXISTS_EXIT:-254}" ;;
  "s3api create-bucket")
    if [ -n "${FAKE_BUCKET_TAKEN:-}" ]; then echo "An error occurred (BucketAlreadyExists) when calling the CreateBucket operation" >&2; exit 1; fi
    exit 0 ;;
esac
exit 0
EOF
cat > "$work/bin/terraform" << 'EOF'
#!/usr/bin/env bash
echo "terraform $* [TF_DATA_DIR=${TF_DATA_DIR:-}]" >> "$FAKE_CALLS"
case "$1" in
  version) echo "Terraform v1.15.9"; exit 0 ;;
  init) echo "Terraform has been successfully initialized!"; exit 0 ;;
  apply)
    case "${FAKE_APPLY:-ok}" in
      ok)
        echo "aws_lambda_function.backend: Creation complete after 20s"
        echo "Apply complete! Resources: 14 added, 0 changed, 0 destroyed."; exit 0 ;;
      nochange)
        echo "No changes. Your infrastructure matches the configuration."; exit 0 ;;
      scp)
        echo "Error: s3:CreateBucket ... with an explicit deny in a service control policy: arn:aws:organizations::1:policy/o-x/service_control_policy/p-y" >&2; exit 1 ;;
      nospace)
        echo "Error while installing hashicorp/aws v6.68.0: write .terraform/providers/x: no space left on device" >&2; exit 1 ;;
      conflict)
        echo "Error: ResourceConflictException: The statement id (FunctionURLAllowPublicAccess) provided already exists." >&2; exit 1 ;;
      denied)
        echo "Error: AccessDenied: User is not authorized to perform: lambda:CreateFunction" >&2; exit 1 ;;
    esac ;;
  output)
    case "$3" in
      backend_url) echo "https://abc123.lambda-url.ap-northeast-1.on.aws" ;;
    esac
    exit 0 ;;
esac
exit 0
EOF
chmod +x "$work/bin/aws" "$work/bin/terraform"
for t in curl unzip sha256sum; do printf '#!/usr/bin/env bash\nexit 0\n' > "$work/bin/$t"; chmod +x "$work/bin/$t"; done

# --- a copy of the AWS directory and the shared files it sources, so that what
# setup.sh writes (backend.tf) and deletes (.terraform) stays in the sandbox.
setup_sandbox() {
  rm -rf "${work:?}/repo"
  mkdir -p "$work/repo/terraform"
  cp -r "$here/../backend" "$work/repo/terraform/backend"
  cp -r "$here" "$work/repo/terraform/backend-aws"
  rm -f "$work/repo/terraform/backend-aws/backend.tf"
  : > "$work/repo/terraform/backend-aws/bootstrap"
  rm -rf "$work/repo/terraform/backend-aws/.terraform"
}

# NEED_MB and DATA_DIR steer the room check: a Terraform data dir that is already
# set is taken as it is, so the "no room" test leaves it empty and asks for more
# room than any disk has.
run_setup() { # args to setup.sh ; env passes through; prints the screen
  FAKE_CALLS="$work/calls" FAKE_DIR="$work" PATH="$work/bin:$PATH" \
    SHAREPI_SETUP_LOG="$work/setup.log" SHAREPI_LANG="${LANG_UNDER_TEST:-en}" \
    SP_TTY=0 NO_COLOR=1 AWS_NEED_MB="${NEED_MB:-1}" TF_DATA_DIR="${DATA_DIR-$work/tfdata}" HOME="$work/home" \
    bash "$work/repo/terraform/backend-aws/setup.sh" "$@" 2>&1
}
mkdir -p "$work/home"
: > "$work/calls"

# ---------------------------------------------------------------- arguments
setup_sandbox
out="$(run_setup)"; contains "no arguments: usage" "$out" "usage: bash setup.sh <area code> <google_user_id> [language]"
out="$(run_setup ea)"; contains "the owner is required" "$out" "usage: bash setup.sh"
out="$(run_setup ea "a@b.c")"; contains "an email is not an owner id" "$out" "google_user_id must be 10-30 digits"
out="$(run_setup ea "123; rm -rf ~")"; contains "a shell injection is refused" "$out" "google_user_id must be 10-30 digits"
out="$(run_setup zz "$SUB")"; contains "an unknown area code" "$out" 'unknown area code "zz"'

# ---------------------------------------------------------------- a clean run
setup_sandbox; : > "$work/calls"
out="$(run_setup ea "$SUB")"; status=$?
[ "$status" -eq 0 ] && ok "a clean run succeeds" || bad "a clean run exited $status: $out"
contains "step 1 is the check" "$out" "[1/8] Checking this shell has what it needs ... ok"
contains "step 2 reports where downloads go" "$out" "Terraform's downloads go to $work/tfdata"
contains "the program next to setup.sh is found" "$out" "found"
contains "the state bucket is made" "$out" "created"
contains "the backend is reported created" "$out" "Your backend is ready."
last="$(tail -n 1 <<< "$out")"
[ "$last" = "https://abc123.lambda-url.ap-northeast-1.on.aws" ] && ok "the URL is the last line, alone" || bad "last line: [$last]"
contains "the administrator note names the Google account" "$out" "Sign in to the app with the Google account the setup command came from"

calls="$(cat "$work/calls")"
contains "the state bucket is named after the account and region" "$calls" "sharepi-tfstate-123456789012-ap-northeast-1"
contains "a Tokyo bucket needs its LocationConstraint" "$calls" "LocationConstraint=ap-northeast-1"
contains "the bucket is made private" "$calls" "put-public-access-block"
contains "the bucket is versioned" "$calls" "put-bucket-versioning"
contains "the bucket is encrypted" "$calls" "put-bucket-encryption"
contains "Terraform gets the Region of the area code" "$calls" "-var=region=ap-northeast-1"
contains "Terraform gets the owner as admin_subs" "$calls" "admin_subs=[\"$SUB\"]"
contains "Terraform gets the program path" "$calls" "backend_binary=$work/repo/terraform/backend-aws/bootstrap"
contains "Terraform's data is in the chosen place" "$calls" "TF_DATA_DIR=$work/tfdata"
lacks "no client ID is passed: the config file is the one place" "$calls" "google_client_id"
backend_tf="$(cat "$work/repo/terraform/backend-aws/backend.tf")"
contains "backend.tf points at the bucket" "$backend_tf" 'bucket       = "sharepi-tfstate-123456789012-ap-northeast-1"'
contains "backend.tf locks without a table" "$backend_tf" "use_lockfile = true"
log="$(cat "$work/setup.log")"
contains "the log says the account and Region" "$log" "account: 123456789012 region: ap-northeast-1"

# us-east-1 has no LocationConstraint
setup_sandbox; : > "$work/calls"
run_setup na "$SUB" > /dev/null
lacks "us-east-1 is made without a LocationConstraint" "$(cat "$work/calls")" "LocationConstraint"

# ---------------------------------------------------------------- unchanged
setup_sandbox
out="$(FAKE_APPLY=nochange run_setup ea "$SUB")"
contains "nothing to do is reported as already running" "$out" "Your backend is already running."

# ---------------------------------------------------------------- the bucket is already there
setup_sandbox; : > "$work/calls"
out="$(FAKE_BUCKET_EXISTS_EXIT=0 run_setup ea "$SUB")"
contains "an existing state bucket is reused" "$out" "already there"
lacks "an existing state bucket is not made again" "$(cat "$work/calls")" "create-bucket"

# ---------------------------------------------------------------- tidying
setup_sandbox
# What an earlier run left in the small home directory: removed. Anything else
# there is the owner's and must stay.
mkdir -p "$work/repo/terraform/backend-aws/.terraform/providers"
: > "$work/repo/terraform/backend-aws/.terraform/providers/old"
: > "$work/home/my-notes.txt"
mkdir -p "$work/home/photos"; : > "$work/home/photos/a.jpg"
run_setup ea "$SUB" > /dev/null
[ ! -d "$work/repo/terraform/backend-aws/.terraform" ] && ok "an earlier run's .terraform is removed" || bad ".terraform was kept"
[ -f "$work/home/my-notes.txt" ] && [ -f "$work/home/photos/a.jpg" ] && ok "the owner's own files are never touched" || bad "an owner file was deleted"
[ -f "$work/repo/terraform/backend-aws/setup.sh" ] && [ -f "$work/repo/terraform/backend-aws/main.tf" ] && ok "the setup's own files are kept" || bad "a setup file was deleted"

# A local state from before the bucket existed is moved into it.
setup_sandbox; : > "$work/calls"; : > "$work/repo/terraform/backend-aws/terraform.tfstate"
run_setup ea "$SUB" > /dev/null
contains "a local state is migrated, not left behind" "$(cat "$work/calls")" "init -input=false -migrate-state -force-copy"
setup_sandbox; : > "$work/calls"
run_setup ea "$SUB" > /dev/null
lacks "no migration flags without a local state" "$(cat "$work/calls")" "migrate-state"

# ---------------------------------------------------------------- room
# Not enough room anywhere: stops before anything is created, says what to do.
setup_sandbox; : > "$work/calls"
out="$(NEED_MB=99999999 DATA_DIR= run_setup ea "$SUB")"; status=$?
contains "no room: the kind is named" "$out" "does not have enough free space"
contains "no room: says what to delete" "$out" "du -sm ~/*"
lacks "no room: nothing was created" "$(cat "$work/calls")" "create-bucket"

# ---------------------------------------------------------------- failures
setup_sandbox
out="$(FAKE_NO_LOGIN=1 run_setup ea "$SUB")"
contains "no AWS sign-in is explained" "$out" "has no sign-in that works in this shell"

setup_sandbox
out="$(FAKE_APPLY=scp run_setup ea "$SUB")"
contains "an SCP denial is explained" "$out" "service control policy refuses this action"
contains "an SCP denial points at AWS Settings" "$out" "https://settings.aws.com"

setup_sandbox
out="$(FAKE_APPLY=conflict run_setup ea "$SUB")"
contains "a leftover permission says how to remove it" "$out" "aws lambda remove-permission --region ap-northeast-1 --function-name chamagon-backend --statement-id FunctionURLAllowPublicAccess"

setup_sandbox
out="$(FAKE_APPLY=nospace run_setup ea "$SUB")"
contains "a full disk during init is explained" "$out" "does not have enough free space"

setup_sandbox
out="$(FAKE_APPLY=denied run_setup ea "$SUB")"
contains "a permission error is explained" "$out" "is not allowed to do something the setup needs"

setup_sandbox
out="$(FAKE_BUCKET_TAKEN=1 run_setup ea "$SUB")"
contains "a taken state bucket name is explained" "$out" "SHAREPI_STATE_BUCKET"

setup_sandbox; rm -f "$work/repo/terraform/backend-aws/bootstrap"
out="$(run_setup ea "$SUB")"
contains "no program and no address says what to set" "$out" "SHAREPI_BACKEND_URL"

# ---------------------------------------------------------------- Japanese
setup_sandbox
out="$(LANG_UNDER_TEST=ja FAKE_APPLY=scp run_setup ea "$SUB")"
contains "Japanese: a step" "$out" "この画面に必要なものがあるか確かめています"
contains "Japanese: an SCP failure" "$out" "サービスコントロールポリシー"
setup_sandbox
out="$(LANG_UNDER_TEST=ja run_setup ea "$SUB")"
contains "Japanese: the administrator note" "$out" "このセットアップのコマンドを出した Google アカウント"
# A language with no AWS words is English.
setup_sandbox
out="$(LANG_UNDER_TEST=fr run_setup ea "$SUB")"
contains "another language: English AWS steps" "$out" "Checking this shell has what it needs"

# ---------------------------------------------------------------- the catalogue
# Every failure kind aws_classify can name has a body in English and Japanese.
# shellcheck source=setup_aws.sh
# shellcheck source=../backend/setup_ui.sh
( cd "$here/../backend" && source ./setup_ui.sh && cd "$here" && source ./setup_aws.sh && source ./setup_aws_msg.sh
  missing=0
  for kind in no_space aws_login missing_tool scp permission bucket_taken concurrency statement_exists no_program; do
    for lang in en ja; do
      SP_LANG="$lang"
      body="$("ui_body_aws_$lang" "$kind")"
      [ -n "$body" ] || { echo "FAIL aws body $lang/$kind is empty" >&2; missing=1; }
    done
  done
  exit "$missing" ) && ok "catalogue: every AWS failure kind has English and Japanese words" || bad "catalogue has a gap"

# aws_classify, on its own
( source "$here/../backend/setup_ui.sh" && source "$here/setup_aws.sh"
  r=0
  chk() { [ "$(aws_classify "$1")" = "$2" ] || { echo "FAIL classify [$1] -> $(aws_classify "$1"), want $2" >&2; r=1; }; }
  chk "x with an explicit deny in a service control policy: y" scp
  chk "write: no space left on device" no_space
  chk "BucketAlreadyExists" bucket_taken
  chk "InvalidParameterValueException: ReservedConcurrentExecutions below UnreservedConcurrentExecution" concurrency
  chk "AccessDenied: not authorized to perform: s3:PutObject" permission
  chk "Could not resolve host" network
  exit "$r" ) && ok "aws_classify names the known failures" || bad "aws_classify is wrong"

[ "$fail" -eq 0 ] && echo "all passed" || { echo "FAILED" >&2; exit 1; }
