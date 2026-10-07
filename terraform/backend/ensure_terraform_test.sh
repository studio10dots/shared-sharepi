#!/usr/bin/env bash
# Checks ensure_terraform.sh without Google, HashiCorp or a network:
#   bash ensure_terraform_test.sh
# `uname` and `curl` are replaced by functions, so the install path runs against
# a local zip that holds a stand-in terraform.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=ensure_terraform.sh
source ./ensure_terraform.sh

fail=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1" >&2; fail=1; }
check() { # name expected actual
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1: expected [$2], got [$3]"; fi
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
ORIG_PATH="$PATH"

# A terraform that prints install instructions and exits 0: what Cloud Shell has now.
mkdir "$work/shim"
printf '#!/bin/sh\necho "  Follow the instructions at https://developer.hashicorp.com/terraform/install to install terraform"\nexit 0\n' > "$work/shim/terraform"
chmod +x "$work/shim/terraform"
PATH="$work/shim:$ORIG_PATH"
if terraform_works; then bad "the Cloud Shell stand-in counts as a working terraform"; else ok "the stand-in that exits 0 is not a working terraform"; fi

# A working one.
mkdir "$work/real"
printf '#!/bin/sh\necho "Terraform v1.0.0"\n' > "$work/real/terraform"
chmod +x "$work/real/terraform"
PATH="$work/real:$ORIG_PATH"
if terraform_works; then ok "a terraform that answers Terraform v... works"; else bad "a working terraform is not recognised"; fi
ensure_terraform && ok "ensure_terraform does nothing when it already works" || bad "ensure_terraform failed with a working terraform"

# Checksums.
printf 'abc' > "$work/f"
sum="$(sha256sum "$work/f" | cut -d' ' -f1)"
if verify_sha256 "$work/f" "$sum"; then ok "the right checksum is accepted"; else bad "the right checksum was refused"; fi
if verify_sha256 "$work/f" "0000"; then bad "a wrong checksum was accepted"; else ok "a wrong checksum is refused"; fi

# The install path, on a pretend Linux with a pretend download.
python_bin=""
for candidate in python3 python; do
  # A candidate has to actually run (Windows has a `python3` stub that does not).
  if "$candidate" -c 'import zipfile' > /dev/null 2>&1; then python_bin="$candidate"; break; fi
done
if [ -z "$python_bin" ]; then
  echo "skip install-path tests: no python to build a zip"
else
  mkdir "$work/zipsrc"
  printf '#!/bin/sh\necho "Terraform v9.9.9"\n' > "$work/zipsrc/terraform"
  chmod +x "$work/zipsrc/terraform"
  # A relative path: a Windows python cannot write to an MSYS /tmp path.
  (cd "$work/zipsrc" && "$python_bin" -m zipfile -c ../fake.zip terraform)
  fake_sum="$(sha256sum "$work/fake.zip" | cut -d' ' -f1)"

  uname() { case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac; }
  curl() { # last "-o file" wins, as in the real call
    local out=""
    while [ "$#" -gt 0 ]; do [ "$1" = "-o" ] && out="$2"; shift; done
    cp "$work/fake.zip" "$out"
  }

  PATH="$work/shim:$ORIG_PATH"
  TERRAFORM_SHA256_LINUX_AMD64="$fake_sum"
  TERRAFORM_INSTALL_DIR="$work/installed"
  if ensure_terraform 2> /dev/null && [ -x "$work/installed/terraform" ] && terraform_works; then
    ok "installs, verifies and uses a downloaded terraform when only the stand-in exists"
  else
    bad "the install path did not produce a working terraform"
  fi

  # The same download with a different pinned sum must be refused and installed nowhere.
  PATH="$work/shim:$ORIG_PATH"
  TERRAFORM_SHA256_LINUX_AMD64="1111111111111111111111111111111111111111111111111111111111111111"
  TERRAFORM_INSTALL_DIR="$work/refused"
  if ensure_terraform 2> /dev/null; then bad "a download with the wrong checksum was used"; else ok "a download with the wrong checksum is refused"; fi
  [ ! -e "$work/refused/terraform" ] && ok "nothing is installed when the checksum is wrong" || bad "a refused download was installed"

  uname() { case "$1" in -s) echo Linux ;; -m) echo riscv64 ;; esac; }
  PATH="$work/shim:$ORIG_PATH"
  if ensure_terraform 2> /dev/null; then bad "an architecture with no release was accepted"; else ok "an architecture with no release is refused"; fi
fi

exit "$fail"
