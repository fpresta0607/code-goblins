#!/usr/bin/env bash
# bin/backends/orca.sh - the Orca terminal session-provider adapter.
#
# This slice intentionally exposes only terminal primitives for already-created
# Orca terminals: capture, send text, Enter/Ctrl-C keys, and close. Worktree
# creation, terminal creation, spawn metadata, Escape key support, and teardown
# lifecycle wiring are outside this primitive-only slice.
#
# Target string shape: the Orca terminal id accepted by `orca terminal ...`.

fm_backend_orca_tool_check() {
  command -v orca >/dev/null 2>&1 || { echo "error: backend=orca selected but the 'orca' CLI is not installed" >&2; return 1; }
}

fm_backend_orca_capture() {  # <terminal-id> <lines>
  local terminal=$1 lines=${2:-40}
  fm_backend_orca_tool_check || return 1
  orca terminal read --terminal "$terminal" --limit "$lines" --json \
    | node -e '
const fs = require("fs");
const data = JSON.parse(fs.readFileSync(0, "utf8"));
const r = data.result || {};
if (r.terminal && Array.isArray(r.terminal.tail)) {
  process.stdout.write(r.terminal.tail.join("\n"));
} else if (Array.isArray(r.tail)) {
  process.stdout.write(r.tail.join("\n"));
} else {
  process.stdout.write(r.text || r.output || r.content || r.preview || "");
}
'
}

fm_backend_orca_send_key() {  # <terminal-id> <key>
  local terminal=$1 key=$2
  fm_backend_orca_tool_check || return 1
  case "$key" in
    C-c|ctrl+c|Ctrl-c|Ctrl-C)
      orca terminal send --terminal "$terminal" --interrupt --json >/dev/null
      ;;
    Enter|enter)
      orca terminal send --terminal "$terminal" --text "" --enter --json >/dev/null
      ;;
    *)
      echo "error: unsupported Orca key '$key'" >&2
      return 1
      ;;
  esac
}

fm_backend_orca_send_text_submit() {  # <terminal-id> <text> <retries> <enter-sleep> <settle>
  local terminal=$1 text=$2
  fm_backend_orca_tool_check || { printf 'send-failed'; return 0; }
  if orca terminal send --terminal "$terminal" --text "$text" --enter --json >/dev/null; then
    printf 'empty'
  else
    printf 'send-failed'
  fi
}

fm_backend_orca_kill() {  # <terminal-id>
  fm_backend_orca_tool_check || return 0
  orca terminal close --terminal "$1" --json >/dev/null 2>&1 || true
}
