# Finds the Google user id -- the `sub` of the account's ID token -- of whoever
# is running setup.sh, so the backend can pin its administrator to that id
# (ADMIN_SUBS) instead of to an email address. An email can change hands (a
# company or school address, a recreated account); the id never does.
#
# Source this file, then call `google_user_id`: it prints the id, or nothing if
# it could not be read (setup.sh then falls back to the email and says so).
# Tokens are only ever held in shell variables and passed on stdin, never as
# command-line arguments, so they do not show in a process list.

# The `sub` claim of a JWT given as $1: its payload is the base64url JSON
# between the first and second dot. Prints nothing if there is no numeric sub.
sub_from_jwt() {
  local payload
  payload="$(printf '%s' "$1" | cut -d. -f2 | tr '_-' '/+')"
  # base64url drops the "=" padding that base64 -d expects.
  while [ $(( ${#payload} % 4 )) -ne 0 ]; do payload="${payload}="; done
  printf '%s' "$payload" | base64 -d 2>/dev/null \
    | tr -d ' \n' | sed -n 's/.*"sub":"\([0-9]\{10,30\}\)".*/\1/p'
}

# The `sub` in Google's tokeninfo answer (JSON on stdin).
sub_from_tokeninfo() {
  tr -d ' \n' | sed -n 's/.*"sub":"\([0-9]\{10,30\}\)".*/\1/p'
}

google_user_id() {
  local token sub=""
  # An ID token carries the id itself.
  token="$(gcloud auth print-identity-token 2>/dev/null || true)"
  if [ -n "$token" ]; then
    sub="$(sub_from_jwt "$token" || true)"
  fi
  # Otherwise ask Google what the access token belongs to.
  if [ -z "$sub" ]; then
    token="$(gcloud auth print-access-token 2>/dev/null || true)"
    if [ -n "$token" ]; then
      sub="$(printf 'access_token=%s' "$token" \
        | curl -fsS --data @- https://oauth2.googleapis.com/tokeninfo 2>/dev/null \
        | sub_from_tokeninfo || true)"
    fi
  fi
  printf '%s' "$sub"
}
