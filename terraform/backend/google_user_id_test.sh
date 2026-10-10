#!/usr/bin/env bash
# Checks the parts of google_user_id.sh that need no Google account:
#   bash google_user_id_test.sh
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=google_user_id.sh
source ./google_user_id.sh

fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then
    echo "ok   $1"
  else
    echo "FAIL $1: expected [$2], got [$3]" >&2
    fail=1
  fi
}

# A JWT whose payload is the given JSON (the header and signature are filler).
jwt() {
  local body
  body="$(printf '%s' "$1" | base64 | tr -d '\n=' | tr '+/' '-_')"
  printf 'eyJhbGciOiJSUzI1NiJ9.%s.c2ln' "$body"
}

check "plain payload" "100000000000000000001" \
  "$(sub_from_jwt "$(jwt '{"sub":"100000000000000000001","email":"a@b.c"}')")"

# Payload lengths that need 0, 1, 2 and 3 characters of padding.
for pad in "" "x" "xx" "xxx"; do
  check "padding variant [$pad]" "100000000000000000002" \
    "$(sub_from_jwt "$(jwt "{\"sub\":\"100000000000000000002\",\"n\":\"$pad\"}")")"
done

check "pretty-printed json" "100000000000000000001" \
  "$(sub_from_jwt "$(jwt '{ "sub" : "100000000000000000001" }')")"

check "no sub claim" "" "$(sub_from_jwt "$(jwt '{"email":"a@b.c"}')")"
check "a sub that is not numeric is refused" "" \
  "$(sub_from_jwt "$(jwt '{"sub":"abc"}')")"
check "garbage" "" "$(sub_from_jwt "not-a-token" || true)"

check "tokeninfo answer" "100000000000000000001" \
  "$(printf '{\n  "sub": "100000000000000000001",\n  "email": "a@b.c"\n}\n' | sub_from_tokeninfo)"
check "tokeninfo without sub" "" \
  "$(printf '{"email": "a@b.c"}' | sub_from_tokeninfo)"

exit "$fail"
