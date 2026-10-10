# What setup.sh (AWS) needs that Google Cloud's does not: checking that this
# Cloud Shell can hold the materials, keeping Terraform's downloads out of the
# small home directory, the place the setup state lives, and knowing an AWS
# failure when one is printed.
#
# Source this file after ../backend/setup_ui.sh (the screen is the same one).
#
# AWS CloudShell's home directory is only 1 GB and survives between sessions,
# while /tmp is roomier and is wiped. Terraform's AWS provider is several hundred
# MB, and a newer one next to an older one does not fit: the first real run of
# this stack stopped with "no space left on device". So Terraform's working data
# (the downloaded providers) goes to /tmp, and what must survive (the state)
# does not stay in the home directory at all: it is in a bucket of the owner's.

# Free space of the filesystem holding $1, in MB (0 when it cannot be told).
aws_free_mb() {
  local kb
  kb="$(df -Pk "$1" 2> /dev/null | awk 'NR==2 {print $4}')"
  case "$kb" in '' | *[!0-9]*) echo 0 ;; *) echo $((kb / 1024)) ;; esac
}

# Room Terraform needs, in MB: the AWS provider (~700), the archive provider and
# Terraform itself (~150), the backend program and a margin.
AWS_NEED_MB="${AWS_NEED_MB:-1000}"

# Where Terraform's downloads go. TF_DATA_DIR is the folder Terraform keeps
# ".terraform" (the providers) in. It is /tmp's when that has room, else the home
# directory's, and the choice is printed to the log.
#   aws_choose_data_dir  -> sets TF_DATA_DIR, prints nothing
aws_choose_data_dir() {
  if [ -n "${TF_DATA_DIR:-}" ]; then
    mkdir -p "$TF_DATA_DIR"
    return 0
  fi
  local tmp_free home_free
  tmp_free="$(aws_free_mb /tmp)"
  home_free="$(aws_free_mb "$HOME")"
  if [ "$tmp_free" -ge "$AWS_NEED_MB" ]; then
    TF_DATA_DIR="/tmp/sharepi-tfdata"
  elif [ "$home_free" -ge "$AWS_NEED_MB" ]; then
    TF_DATA_DIR="$HOME/.sharepi-tfdata"
  else
    return 1
  fi
  mkdir -p "$TF_DATA_DIR"
  export TF_DATA_DIR
  echo "terraform data dir: $TF_DATA_DIR (free: /tmp ${tmp_free} MB, home ${home_free} MB, needed ${AWS_NEED_MB} MB)"
}

# Removes what an earlier run of this setup left that is no longer needed, and
# only that: the ".terraform" next to setup.sh (providers an older run put in the
# small home directory, where a newer one no longer fits), and its stale plan
# files. Nothing else in the home directory is touched; if there still is not
# enough room, aws_big_files says what is using it.
#   aws_clean_old $setup_dir
aws_clean_old() {
  local dir="$1"
  [ -n "$dir" ] && [ -d "$dir" ] || return 0
  if [ -d "$dir/.terraform" ]; then
    echo "removing old $dir/.terraform ($(du -sm "$dir/.terraform" 2> /dev/null | cut -f1) MB)"
    rm -rf "${dir:?}/.terraform"
  fi
  rm -f "${dir:?}"/.backend.zip "${dir:?}"/tfplan
}

# The biggest things in the home directory, for the failure message.
aws_big_files() {
  du -sm "$HOME"/* "$HOME"/.[!.]* 2> /dev/null | sort -rn | head -n 6 | awk '{printf "  %6d MB  %s\n", $1, $2}'
}

# Checks the tools and the room before anything is created. Prints what is wrong
# in a form ui_classify recognises, and returns 1 for a failure.
#   aws_preflight
aws_preflight() {
  local tool
  for tool in aws curl unzip sha256sum; do
    if ! command -v "$tool" > /dev/null 2>&1; then
      echo "MISSING_TOOL: \`$tool\` is not installed in this shell" >&2
      return 1
    fi
  done
  if ! aws sts get-caller-identity --output text --query Account > /dev/null 2>&1; then
    echo "AWS_NO_CREDENTIALS: the AWS CLI has no usable sign-in" >&2
    return 1
  fi
  if ! aws_choose_data_dir; then
    echo "NO_SPACE: not enough free space for Terraform's downloads (needs ${AWS_NEED_MB} MB)" >&2
    aws_big_files >&2
    return 1
  fi
}

# The account's id, for names that must be unique across AWS and for the log.
aws_account_id() { aws sts get-caller-identity --query Account --output text; }

# Makes the bucket that keeps Terraform's state when it is not there yet: private,
# versioned (so a bad apply can be recovered from) and encrypted. The state holds
# no secret, but it names every resource. Prints "created" or "already_there".
#   aws_ensure_state_bucket <bucket> <region>
aws_ensure_state_bucket() {
  local bucket="$1" region="$2"
  if aws s3api head-bucket --bucket "$bucket" > /dev/null 2>&1; then
    echo already_there
    return 0
  fi
  if [ "$region" = "us-east-1" ]; then
    aws s3api create-bucket --bucket "$bucket" --region "$region" > /dev/null || return 1
  else
    aws s3api create-bucket --bucket "$bucket" --region "$region" \
      --create-bucket-configuration "LocationConstraint=$region" > /dev/null || return 1
  fi
  aws s3api put-public-access-block --bucket "$bucket" --public-access-block-configuration \
    BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true || return 1
  aws s3api put-bucket-versioning --bucket "$bucket" --versioning-configuration Status=Enabled || return 1
  aws s3api put-bucket-encryption --bucket "$bucket" --server-side-encryption-configuration \
    '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}' || return 1
  echo created
}

# The text of a failed AWS step -> one word naming the cause, the way
# ui_classify does for Google Cloud (first match wins). Anything it does not know
# falls back to ui_classify, so the shared kinds (terraform, network) still work.
aws_classify() {
  local text="$1"
  if grep -qE 'NO_SPACE|no space left on device' <<< "$text"; then
    echo no_space
  elif grep -qE 'AWS_NO_CREDENTIALS|Unable to locate credentials|could not be found|expired' <<< "$text"; then
    echo aws_login
  elif grep -qE 'MISSING_TOOL' <<< "$text"; then
    echo missing_tool
  elif grep -qE 'explicit deny in a service control policy' <<< "$text"; then
    echo scp
  elif grep -qE 'BACKEND_PROGRAM' <<< "$text"; then
    echo no_program
  elif grep -qE 'BucketAlreadyExists|BucketAlreadyOwnedByYou' <<< "$text"; then
    echo bucket_taken
  elif grep -qE 'ReservedConcurrentExecutions|UnreservedConcurrentExecution' <<< "$text"; then
    echo concurrency
  elif grep -qE 'FunctionURLAllowPublicAccess|ResourceConflictException' <<< "$text"; then
    echo statement_exists
  elif grep -qE 'AccessDenied|UnauthorizedOperation|not authorized to perform' <<< "$text"; then
    echo permission
  else
    ui_classify "$text"
  fi
}
