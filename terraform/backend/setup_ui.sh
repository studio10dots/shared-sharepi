# What setup.sh shows the owner, and where everything else goes.
#
# The screen carries only the banner and one line per step. Everything the
# commands print (gcloud, terraform) is appended to a log file instead, and when a
# step fails the owner gets what happened, what to do next (with commands they
# can copy), the last lines of that step's output and the log's path. The last
# line of a successful run is the backend's URL, so that it is the one thing left
# to copy.
#
# Source this file. setup.sh sets SP_PROJECT and SP_REGION and calls, in order:
# ui_lang, ui_init, ui_total, then per step ui_step / ui_run (or ui_run_progress)
# / ui_done, ui_fail when a step fails, and ui_final at the end.
#
# The words are English or Japanese (SP_LANG, see ui_lang): this runs in Cloud
# Shell for owners everywhere, and a language is added by adding a branch to ui_t
# and to ui_explain_text.

SP_PROJECT=""
SP_REGION=""
SP_LANG="en"
SP_LOG=""
SP_TOTAL=0
SP_N=0
SP_STEP_OFFSET=0
SP_STEP_LABEL=""
SP_KNOWN=1
# SP_K: how many rows the cursor is below the top of the banner (every line this
# file prints is counted), so the banner can be redrawn from wherever a long step
# has left the cursor. SP_PHASE: where in its colour cycle the banner is.
# SP_LIVE: whether it may be redrawn while a step runs (see ui_init).
SP_K=0
SP_PHASE=0
SP_LIVE=0

# Whether the screen is a terminal, decided once, here, where this file is
# sourced. It must not be asked again inside $( ... ): there the output is a pipe
# and the answer is always "no" (that is why the colours and the spinner once
# never showed).
if [ -z "${SP_TTY:-}" ]; then
  SP_TTY=0
  if [ -t 1 ]; then SP_TTY=1; fi
fi
SP_MODE="none"

# ---------------------------------------------------------------- language

# The words are in setup_msg_<code>.sh, one file per language (en is the
# reference; setup_test.sh checks that the others have everything it has).
SP_LANGUAGES="en ja es fr de pt ko"
for _sp_code in $SP_LANGUAGES; do
  # shellcheck source=setup_msg_en.sh
  source "$(dirname "${BASH_SOURCE[0]}")/setup_msg_${_sp_code}.sh"
done
unset _sp_code

# SP_LANG: the language given on the command line (SHAREPI_LANG), else the one the
# shell is set to, else English. Only the first two letters count (ja_JP.UTF-8,
# pt-BR, ko_KR all work); a language with no translation is English.
ui_lang() {
  local want="${SHAREPI_LANG:-${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}}"
  want="$(printf '%s' "$want" | cut -c1-2 | tr 'A-Z' 'a-z')"
  case " $SP_LANGUAGES " in
    *" $want "*) SP_LANG="$want" ;;
    *) SP_LANG="en" ;;
  esac
}

# A short message by key. Arguments (%s) follow the key. A key a language lacks is
# shown in English.
ui_t() {
  local key="$1"
  shift
  local fmt
  fmt="$("ui_fmt_$SP_LANG" "$key")"
  if [ -z "$fmt" ]; then fmt="$(ui_fmt_en "$key")"; fi
  if [ -z "$fmt" ]; then fmt="$key"; fi
  # shellcheck disable=SC2059
  printf "$fmt" "$@"
}


# ---------------------------------------------------------------- banner

# The banner's art, one array entry per line.
SP_ART=()
while IFS= read -r _sp_line; do SP_ART+=("$_sp_line"); done << 'BANNER'
   _____ __                    ____  _
  / ___// /_  ____ _________  / __ \(_)
  \__ \/ __ \/ __ `/ ___/ _ \/ /_/ / /
 ___/ / / / / /_/ / /  /  __/ ____/ /
/____/_/ /_/\__,_/_/   \___/_/   /_/
BANNER
unset _sp_line

# Sets SP_MODE to truecolor, 256 or none: what the terminal can show. Colour is
# off when the output is not a terminal (a pipe, a file) or NO_COLOR is set.
# SP_COLOR forces a mode (the tests do). It sets a variable rather than printing
# the answer, so that nobody calls it inside $( ... ).
ui_color_mode() {
  if [ -n "${SP_COLOR:-}" ]; then
    SP_MODE="$SP_COLOR"
  elif [ "$SP_TTY" -ne 1 ] || [ -n "${NO_COLOR:-}" ] || [ "${TERM:-dumb}" = "dumb" ]; then
    SP_MODE="none"
  elif [ -n "${CLOUD_SHELL:-}" ] || [ "${COLORTERM:-}" = "truecolor" ] || [ "${COLORTERM:-}" = "24bit" ]; then
    SP_MODE="truecolor"
  else
    SP_MODE="256"
  fi
}

# The text cursor is hidden while something is redrawn in place, or it is seen
# jumping about; it is always shown again, whatever ends the run (ui_init sets
# the traps).
ui_cursor_hide() { if [ "$SP_TTY" -eq 1 ]; then printf '\033[?25l'; fi; }
ui_cursor_show() { if [ "$SP_TTY" -eq 1 ]; then printf '\033[?25h'; fi; }

# A point on the landing page's blue -> indigo -> violet gradient, t in 0..100:
# sets SP_R, SP_G, SP_B (truecolor) and SP_P (the nearest 256-colour code).
ui_gradient() {
  local t="$1"
  local -a a b
  if [ "$t" -le 50 ]; then
    a=(59 130 246) b=(99 102 241) t=$((t * 2)) # #3b82f6 -> #6366f1
  else
    a=(99 102 241) b=(167 139 250) t=$(((t - 50) * 2)) # #6366f1 -> #a78bfa
  fi
  SP_R=$(((a[0] * (100 - t) + b[0] * t) / 100))
  SP_G=$(((a[1] * (100 - t) + b[1] * t) / 100))
  SP_B=$(((a[2] * (100 - t) + b[2] * t) / 100))
  local -a palette=(33 33 69 63 99 105 141 141)
  SP_P="${palette[$(($1 * 8 / 101))]}"
}

# One frame of the banner. The colour of each letter depends on where it is and on
# $1, the phase, so successive frames make the gradient slide along the letters.
ui_banner_frame() {
  local phase="${1:-0}" mode r c ch line out="" pos
  ui_color_mode
  mode="$SP_MODE"
  for r in "${!SP_ART[@]}"; do
    line="${SP_ART[$r]}"
    if [ "$mode" = none ]; then
      out+="$line"$'\n'
      continue
    fi
    for ((c = 0; c < ${#line}; c++)); do
      ch="${line:c:1}"
      if [ "$ch" = " " ]; then
        out+=" "
        continue
      fi
      pos=$(((c * 100 / 40 + r * 6 + phase * 8) % 200))
      [ "$pos" -gt 100 ] && pos=$((200 - pos))
      ui_gradient "$pos"
      if [ "$mode" = truecolor ]; then
        out+=$'\033[38;2;'"${SP_R};${SP_G};${SP_B}m${ch}"
      else
        out+=$'\033[38;5;'"${SP_P}m${ch}"
      fi
    done
    out+=$'\033[0m\n'
  done
  printf '%s' "$out"
}

# The banner. On a terminal the colours slide along the letters for about a
# second and a half, redrawn in place with the cursor hidden; anywhere else it is
# printed once, plain.
ui_banner() {
  echo
  ui_color_mode
  if [ "$SP_MODE" != none ] && [ "$SP_TTY" -eq 1 ]; then
    local lines=${#SP_ART[@]} f
    ui_cursor_hide
    ui_banner_frame 0
    for ((f = 1; f <= 24; f++)); do
      sleep 0.06
      printf '\033[%dA' "$lines"
      ui_banner_frame "$f"
    done
    ui_cursor_show
    SP_PHASE=24
  else
    ui_banner_frame 0
  fi
  echo
  SP_K=$((${#SP_ART[@]} + 1)) # the art, and the blank line after it
}

# The number of rows of the screen, 0 when it cannot be told.
ui_rows() {
  local r=""
  r="$(stty size 2> /dev/null | cut -d' ' -f1)" || true
  case "$r" in '' | *[!0-9]*) r="$(tput lines 2> /dev/null || true)" ;; esac
  case "$r" in '' | *[!0-9]*) r=0 ;; esac
  echo "$r"
}

# One more frame of the banner, drawn where it is: up SP_K rows, draw, back to
# where the cursor was (ESC 7 and ESC 8 save and restore it). This is what keeps
# the colours moving for as long as a step runs.
ui_banner_live() {
  printf '\e7\033[%dA\r' "$SP_K"
  ui_banner_frame "$SP_PHASE"
  printf '\e8'
  SP_PHASE=$((SP_PHASE + 1))
}

# Prints a message of one or more lines, and counts them.
ui_print_lines() {
  local text
  text="$(ui_t "$1")"
  printf '%s\n' "$text"
  SP_K=$((SP_K + $(printf '%s\n' "$text" | wc -l)))
}

# ---------------------------------------------------------------- the run

# The banner, and the log file the rest of the output goes to.
ui_init() {
  SP_LOG="${SHAREPI_SETUP_LOG:-$HOME/sharepi-setup-$(date +%Y%m%d-%H%M%S).log}"
  mkdir -p "$(dirname "$SP_LOG")"
  : > "$SP_LOG"
  chmod 600 "$SP_LOG" 2> /dev/null || true # it holds the project and the account's id
  ui_color_mode
  # What the screen was taken to be, so a colourless run can be understood.
  printf 'terminal: tty=%s colour=%s lang=%s TERM=%s COLORTERM=%s CLOUD_SHELL=%s\n' \
    "$SP_TTY" "$SP_MODE" "$SP_LANG" "${TERM:-}" "${COLORTERM:-}" "${CLOUD_SHELL:-}" >> "$SP_LOG"
  # Whatever ends the run, the cursor comes back.
  trap ui_cursor_show EXIT
  trap 'ui_cursor_show; exit 130' INT TERM
  ui_banner
  ui_t intro
  echo
  echo "  $SP_LOG"
  echo
  SP_K=$((SP_K + 3))
  # The banner is redrawn while steps run only where every row it needs is still on
  # the screen: the steps below it and the longest message must fit, or "up SP_K
  # rows" would land somewhere else. 26 rows is room for all of them.
  if [ "$SP_MODE" != none ] && [ "$SP_TTY" -eq 1 ] && [ "$(ui_rows)" -ge 26 ]; then
    SP_LIVE=1
  fi
  printf 'banner: live=%s rows=%s\n' "$SP_LIVE" "$(ui_rows)" >> "$SP_LOG"
}

ui_total() { SP_TOTAL="$1"; }

# Starts a step: "[2/7] Doing a thing ... " with no newline; ui_done or ui_fail
# ends the line.
ui_step() {
  SP_N=$((SP_N + 1))
  printf '=== [%d/%d] %s\n' "$SP_N" "$SP_TOTAL" "$1" >> "$SP_LOG"
  SP_STEP_OFFSET="$(wc -c < "$SP_LOG" | tr -d ' ')"
  SP_STEP_LABEL="$(printf '[%d/%d] %s ... ' "$SP_N" "$SP_TOTAL" "$1")"
  printf '%s' "$SP_STEP_LABEL"
}

ui_done() {
  ui_t ok
  if [ -n "${1:-}" ]; then printf ' (%s)' "$1"; fi
  echo
  SP_K=$((SP_K + 1))
}

# Runs a command with everything it prints going to the log. Functions run in
# this shell, so a PATH change they make stays.
ui_run() { "$@" >> "$SP_LOG" 2>&1; }

# The same, for a long command: a spinning bar (| / - \) in place, so it is clear
# that it is working. Without a terminal there is nothing to spin, and nothing is
# printed while it waits.
ui_run_progress() {
  "$@" >> "$SP_LOG" 2>&1 &
  local pid=$! i=0 frames='|/-\'
  ui_cursor_hide
  while kill -0 "$pid" 2> /dev/null; do
    if [ "$SP_TTY" -eq 1 ]; then
      if [ "$SP_LIVE" -eq 1 ]; then ui_banner_live; fi
      # The whole label is written again each time (\r goes back to the start of
      # the line) rather than a backspace being relied on.
      printf '\r%s%s' "$SP_STEP_LABEL" "${frames:i%4:1}"
      i=$((i + 1))
    fi
    sleep "${SP_PROGRESS_INTERVAL:-0.1}"
  done
  if [ "$SP_TTY" -eq 1 ]; then printf '\r%s' "$SP_STEP_LABEL"; fi
  ui_cursor_show
  wait "$pid"
}

# What the current step has written to the log so far.
ui_step_log() { tail -c +"$((SP_STEP_OFFSET + 1))" "$SP_LOG"; }

# ---------------------------------------------------------------- failures

# The kind of failure in the text of a failed step ($1), by the first pattern that
# matches (so the specific ones come first). Prints one word.
ui_classify() {
  local text="$1"
  if grep -qiE 'billing account.*(disabled|absent)|BILLING_DISABLED|billing must be enabled|billing has not been enabled|requires billing' <<< "$text"; then
    echo billing
  elif grep -qE 'cannot destroy service without setting deletion_protection' <<< "$text"; then
    echo protected
  elif grep -qiE 'Terraform is not installed|installed Terraform does not run|does not match the expected checksum|Could not download' <<< "$text"; then
    echo terraform
  elif grep -qiE 'SERVICE_DISABLED|has not been used in project|accessNotConfigured' <<< "$text"; then
    echo not_ready
  elif grep -qiE 'quota.*exceeded|RESOURCE_EXHAUSTED|failed to initialize in this region' <<< "$text"; then
    echo quota
  elif grep -qiE 'bucket.*(already exists|already own|not available)|409.*bucket|The requested bucket name is not available' <<< "$text"; then
    echo bucket_taken
  elif grep -qiE 'project.*(not found|does not exist)|may not exist|Could not find project|invalid project' <<< "$text"; then
    echo no_project
  elif grep -qiE 'permission|PERMISSION_DENIED|forbidden|403' <<< "$text"; then
    echo permission
  elif grep -qiE 'Could not resolve|Connection refused|i/o timeout|TLS handshake|Temporary failure|no such host|network is unreachable' <<< "$text"; then
    echo network
  else
    echo unknown
  fi
}

# "What happened" and "What to do" for a kind of failure, in SP_LANG (the text is
# in setup_msg_<code>.sh).
ui_explain_text() {
  printf '%s\n' "$(ui_t what_happened)"
  "ui_body_$SP_LANG" "$1"
}


# Prints "what happened" and "what to do" for the text of a failed step ($1) and
# sets SP_KNOWN to 0 when the cause is not one this script knows.
ui_explain() {
  local kind
  kind="$(ui_classify "$1")"
  SP_KNOWN=1
  if [ "$kind" = unknown ]; then SP_KNOWN=0; fi
  ui_explain_text "$kind"
}

# Ends the current step as failed: the explanation, the last lines of what the
# step printed, the log's path. Stops the script.
ui_fail() {
  ui_t failed
  echo
  echo
  ui_explain "$(ui_step_log)"
  echo
  # For a cause the script knows, the explanation is enough; for the rest the
  # raw lines are what someone needs to see.
  if [ "$SP_KNOWN" -eq 0 ]; then
    ui_t last_lines
    echo
    ui_step_log | grep -v '^[[:space:]]*$' | tail -n 8 | cut -c1-200 | sed 's/^/  | /'
    echo
  fi
  ui_t full_log "$SP_LOG"
  echo
  exit 1
}

# ---------------------------------------------------------------- the end

# The last thing printed: a note about the administrator, where the log is, what
# happened to the backend (created, already running, updated), and then the URL as
# the very last line, alone, flush left, so that selecting that line copies the URL
# and nothing else.
#   ui_final <created|unchanged|updated> <backend url> <how the administrator is recognised>
ui_final() {
  echo
  ui_t final_admin "$3"
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
