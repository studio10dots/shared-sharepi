#!/usr/bin/env bash
# Runs gosec on ../backend (or the directory given as $1).
#
# gosec v2.29.0 as released cannot read the export data Go 1.27.2 writes ("export
# data version 5 is greater than maximum supported version 4"): it then skips the
# type-aware analysis and still prints "Issues: 0". So it is built here against a
# newer golang.org/x/tools, and any package error makes the run fail.
set -euo pipefail

GOSEC_VERSION=v2.29.0
XTOOLS_VERSION=v0.51.0

target="$(cd "${1:-$(dirname "$0")/../backend}" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

(
  cd "$work"
  go mod init gosectool >/dev/null 2>&1
  printf 'package main\nimport _ "github.com/securego/gosec/v2/cmd/gosec"\nfunc main(){}\n' > tools.go
  go get "github.com/securego/gosec/v2@${GOSEC_VERSION}" "golang.org/x/tools@${XTOOLS_VERSION}" >/dev/null 2>&1
  go mod tidy >/dev/null 2>&1
  go build -o "$work/gosec" github.com/securego/gosec/v2/cmd/gosec
)

cd "$target"
"$work/gosec" ./...
