# Makes sure a working `terraform` is on the PATH, installing a pinned release
# into $HOME/.local/bin when there is none. Cloud Shell no longer ships
# Terraform: it has a stand-in `terraform` that prints install instructions and
# exits successfully, so "is there a terraform command" is not the right test --
# `terraform version` has to answer "Terraform v...".
#
# Source this file, then call `ensure_terraform` (setup.sh does).
#
# The release comes straight from releases.hashicorp.com and is checked against
# the SHA-256 pinned below, which is HashiCorp's own published value for that
# file (terraform_<version>_SHA256SUMS). A different file is refused. To move to
# a newer Terraform, change the version and both sums together, from that
# release's SHA256SUMS file.

TERRAFORM_VERSION="1.15.9"
TERRAFORM_SHA256_LINUX_AMD64="76edd0b22d2f27d3d2e097cd793209646f719cf60f02ff3af626b07361137da1"
TERRAFORM_SHA256_LINUX_ARM64="0afa6c29f61ca5ea270e950e43e50ecf2418b598507bf580e8ae76e1e6699b19"

# True when `terraform version` runs and says so.
terraform_works() {
  terraform version 2>/dev/null | head -n 1 | grep -q '^Terraform v'
}

# True when the file $1 has the SHA-256 $2.
verify_sha256() {
  local actual
  actual="$(sha256sum "$1" | cut -d' ' -f1)"
  [ "$actual" = "$2" ]
}

ensure_terraform() {
  if terraform_works; then
    return 0
  fi

  local arch expected
  if [ "$(uname -s)" != "Linux" ]; then
    echo "Terraform is not installed, and it can only be installed automatically on Linux (Cloud Shell)." >&2
    return 1
  fi
  case "$(uname -m)" in
    x86_64 | amd64) arch="amd64"; expected="$TERRAFORM_SHA256_LINUX_AMD64" ;;
    aarch64 | arm64) arch="arm64"; expected="$TERRAFORM_SHA256_LINUX_ARM64" ;;
    *) echo "Terraform is not installed, and there is no release for $(uname -m)." >&2; return 1 ;;
  esac

  local tool
  for tool in curl unzip sha256sum; do
    if ! command -v "$tool" > /dev/null 2>&1; then
      echo "Terraform is not installed, and installing it needs \`$tool\`." >&2
      return 1
    fi
  done

  local dir tmp url
  dir="${TERRAFORM_INSTALL_DIR:-$HOME/.local/bin}"
  tmp="$(mktemp -d)"
  url="https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${arch}.zip"

  echo "Terraform is not installed here: installing Terraform ${TERRAFORM_VERSION} into ${dir} ..." >&2
  if ! curl -fsSL "$url" -o "$tmp/terraform.zip"; then
    echo "Could not download $url" >&2
    rm -rf "$tmp"
    return 1
  fi
  if ! verify_sha256 "$tmp/terraform.zip" "$expected"; then
    echo "The downloaded Terraform does not match the expected checksum; refusing to use it." >&2
    rm -rf "$tmp"
    return 1
  fi
  mkdir -p "$dir"
  unzip -q -o "$tmp/terraform.zip" terraform -d "$dir"
  rm -rf "$tmp"

  export PATH="$dir:$PATH"
  if ! terraform_works; then
    echo "The installed Terraform does not run." >&2
    return 1
  fi
}
