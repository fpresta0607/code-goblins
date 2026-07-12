#!/usr/bin/env bash
# tests/fm-self-supervision-e2e.test.sh - private-socket end-to-end test for the
# PERSISTENT-SECONDMATE self-supervision path (docs/self-supervision.md). It is
# the regression for the silent-overnight-death incident: a persistent
# secondmate must keep supervising its own children WITHOUT the captain and
# WITHOUT away mode (state/.afk).
#
#   Scenario A (autonomous wake, no captain, no .afk): with state/.self-supervise
#     present and state/.afk ABSENT, an in-flight child writes `done` AFTER the
#     secondmate's pane has gone idle. The self-supervise daemon must inject a
#     marked (sentinel-prefixed) resume into the secondmate's OWN pane, and
#     supervision must stay live: a SECOND child event produces a SECOND
#     injection (not a one-shot). Throughout, state/.afk stays absent, and the
#     daemon never mutates the child's meta/status itself (no approval-authority
#     expansion - it only pokes the pane; the secondmate advances on its turn).
#
#   Scenario B (idle self-exit): with self-supervise active, no away mode, and
#     ZERO in-flight work, the daemon self-exits cleanly after the idle grace, so
#     an empty-queue secondmate costs nothing.
#
# Isolation: a dedicated private tmux socket (tmux -L). A tmux shim first on PATH
# redirects the daemon's bare `tmux` calls to that socket; the daemon points at a
# throwaway state dir (FM_STATE_OVERRIDE) and the test pane (FM_SUPERVISOR_TARGET).
# This test never touches the live fleet or any herdr session. FM_SUPERVISOR_BACKEND=tmux
# is passed explicitly so an ambient HERDR_ENV=1 (this test may itself run inside
# herdr) cannot leak into the daemon subprocess and misdetect backend=herdr.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DAEMON="$ROOT/bin/fm-supervise-daemon.sh"

command -v tmux >/dev/null 2>&1 || { echo "skip: tmux not found"; exit 0; }

REAL_TMUX=$(command -v tmux)
SOCKET="fm-selfsup-e2e-$$"
STATE_DIR=
TMUX_SHIM_DIR=
LOG_FILE=
DAEMON_PID=
SUPERVISOR_PANE=
LOOP_SCRIPT=

fail() { printf 'not ok - %s\n' "$1" >&2; cleanup_all; exit 1; }
pass() { printf 'ok - %s\n' "$1"; }

cleanup_all() {
  if [ -n "${DAEMON_PID:-}" ]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
  if [ -n "${SOCKET:-}" ] && [ -n "${REAL_TMUX:-}" ]; then
    "$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null || true
  fi
  rm -rf "${TMUX_SHIM_DIR:-}" 2>/dev/null || true
  rm -rf "${STATE_DIR:-}" 2>/dev/null || true
}
trap cleanup_all EXIT

# --- setup ------------------------------------------------------------------

STATE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/fm-selfsup-e2e.XXXXXX")
mkdir -p "$STATE_DIR"
LOG_FILE="$STATE_DIR/submitted.log"
: > "$LOG_FILE"

# Source the daemon only for FM_INJECT_MARK; the main loop is guarded off.
# shellcheck source=bin/fm-supervise-daemon.sh
. "$DAEMON"

# Private tmux server with a supervisor session (the secondmate's own pane).
"$REAL_TMUX" -L "$SOCKET" new-session -d -s supervisor -x 200 -y 50
SUPERVISOR_PANE=$("$REAL_TMUX" -L "$SOCKET" display-message -p -t supervisor '#{pane_id}')

# Supervisor pane loop: a deterministic composer that logs each submitted line
# verbatim, classified injection (starts with sentinel) vs user. Same proven
# loop as tests/fm-afk-inject-e2e.test.sh.
LOOP_SCRIPT="$STATE_DIR/supervisor-loop.sh"
cat > "$LOOP_SCRIPT" <<'LOOP'
#!/usr/bin/env bash
MARK=$'\x1f'
LOG="$1"
OLD_STTY=$(stty -g 2>/dev/null || true)
[ -z "$OLD_STTY" ] || stty -echo -icanon min 1 time 0 2>/dev/null || true
cleanup() { [ -z "$OLD_STTY" ] || stty "$OLD_STTY" 2>/dev/null || true; }
trap cleanup EXIT INT TERM
_buf=
redraw() { printf '\r\033[K%s' "$_buf"; }
submit_line() {
  local _line=$_buf _c _hex
  if [ "${_line:0:1}" = "$MARK" ]; then _c="injection"; else _c="user"; fi
  _hex=$(printf '%s' "$_line" | od -An -tx1 | tr -d ' \n')
  printf '%s\t%s\t%s\n' "$_hex" "$_line" "$_c" >> "$LOG"
  _buf=
  printf '\r\033[K\n'
  redraw
}
redraw
while IFS= read -r -n 1 _ch; do
  if [ -z "$_ch" ]; then submit_line; continue; fi
  case "$_ch" in
    $'\r'|$'\n') submit_line ;;
    $'\177'|$'\b') _buf=${_buf%?}; redraw ;;
    *) _buf="${_buf}${_ch}"; redraw ;;
  esac
done
LOOP
chmod +x "$LOOP_SCRIPT"

"$REAL_TMUX" -L "$SOCKET" send-keys -t "$SUPERVISOR_PANE" \
  "bash '$LOOP_SCRIPT' '$LOG_FILE'" Enter
sleep 1

# tmux shim: redirect the daemon's bare `tmux` to the private socket.
TMUX_SHIM_DIR=$(mktemp -d "${TMPDIR:-/tmp}/fm-selfsup-shim.XXXXXX")
cat > "$TMUX_SHIM_DIR/tmux" <<SHIM
#!/usr/bin/env bash
exec "$REAL_TMUX" -L "$SOCKET" "\$@"
SHIM
chmod +x "$TMUX_SHIM_DIR/tmux"

# A fake in-flight child window (the watcher lists fm-* windows for stale checks).
"$REAL_TMUX" -L "$SOCKET" new-window -d -n fm-child-c1 -t supervisor
CHILD_PANE=$("$REAL_TMUX" -L "$SOCKET" display-message -p -t fm-child-c1 '#{pane_id}')

# An in-flight child meta (keeps in-flight count > 0 so idle-exit never fires
# during Scenario A) and its initial working status.
write_child_in_flight() {
  cat > "$STATE_DIR/child-c1.meta" <<META
window=$CHILD_PANE
worktree=$STATE_DIR/wt-child-c1
project=labproj
harness=claude
kind=ship
mode=local-only
yolo=off
META
  printf 'working: child building\n' > "$STATE_DIR/child-c1.status"
}

start_daemon() {  # extra env passed as KEY=VAL args (parsed by env at runtime)
  env \
    PATH="$TMUX_SHIM_DIR:$PATH" \
    FM_STATE_OVERRIDE="$STATE_DIR" \
    FM_SUPERVISOR_TARGET="$SUPERVISOR_PANE" \
    FM_SUPERVISOR_BACKEND=tmux \
    FM_ESCALATE_BATCH_SECS=0 \
    FM_HOUSEKEEPING_TICK=1 \
    FM_POLL=1 \
    FM_SIGNAL_GRACE=1 \
    FM_HEARTBEAT=999999 \
    FM_CHECK_INTERVAL=999999 \
    FM_INJECT_CONFIRM_SLEEP=0.3 \
    FM_INJECT_CONFIRM_RETRIES=5 \
    FM_STALE_ESCALATE_SECS=999999 \
    "$@" \
    nohup "$DAEMON" >"$STATE_DIR/daemon.out" 2>"$STATE_DIR/daemon.err" &
  DAEMON_PID=$!
  local i=0
  while [ "$i" -lt 30 ]; do
    [ -f "$STATE_DIR/.supervise-daemon.pid" ] && break
    sleep 0.2
    i=$((i + 1))
  done
  [ -f "$STATE_DIR/.supervise-daemon.pid" ] || {
    echo "daemon stderr:" >&2; cat "$STATE_DIR/daemon.err" >&2
    fail "daemon did not start (no pid file after 6s)"
  }
}

stop_daemon() {
  [ -n "${DAEMON_PID:-}" ] || return 0
  kill "$DAEMON_PID" 2>/dev/null || true
  wait "$DAEMON_PID" 2>/dev/null || true
  DAEMON_PID=""
  sleep 1
}

count_injections() {
  local n
  n=$(grep -c $'\tinjection$' "$LOG_FILE" 2>/dev/null) || true
  printf '%s' "${n:-0}"
}

wait_for_injections() {  # <min-count> <timeout-deciseconds>
  local want=$1 budget=${2:-120} i=0
  while [ "$i" -lt "$budget" ]; do
    [ "$(count_injections)" -ge "$want" ] && return 0
    sleep 0.1
    i=$((i + 1))
  done
  return 1
}

# --- Scenario A: autonomous wake without captain or .afk ---------------------

test_self_supervise_autonomous_wake() {
  write_child_in_flight
  # Self-supervise ON, away mode OFF - the whole point of the fix.
  date '+%s' > "$STATE_DIR/.self-supervise"
  rm -f "$STATE_DIR/.afk"
  # Idle-exit disabled here so it cannot race the injection assertions.
  start_daemon FM_SELF_SUPERVISE_IDLE_EXIT_SECS=999999

  [ ! -e "$STATE_DIR/.afk" ] || fail "Scenario A: state/.afk must NOT be set by self-supervise mode"

  # Secondmate pane is idle (no typing). The child completes AFTER idle.
  local meta_before status_before
  meta_before=$(cat "$STATE_DIR/child-c1.meta")
  status_before=$(cat "$STATE_DIR/child-c1.status")

  printf 'done: child PR ready, checks green\n' >> "$STATE_DIR/child-c1.status"
  wait_for_injections 1 120 \
    || { cat "$STATE_DIR/daemon.err" >&2; fail "Scenario A: no autonomous injection after child done (supervision stalled)"; }

  # The injection is sentinel-prefixed (a real resume poke into the own pane).
  local first_hex
  first_hex=$(grep $'\tinjection$' "$LOG_FILE" | head -1 | cut -f1)
  case "$first_hex" in
    1f*) ;;
    *) fail "Scenario A: injected line is not sentinel-prefixed (hex: $first_hex)" ;;
  esac

  # Supervision stays LIVE: a SECOND child event yields a SECOND injection.
  printf 'working: child fix round\n' >> "$STATE_DIR/child-c1.status"
  sleep 2
  printf 'blocked: child needs a decision\n' >> "$STATE_DIR/child-c1.status"
  wait_for_injections 2 120 \
    || fail "Scenario A: supervision did not stay live for the next child event (no second injection - daemon did not re-arm)"

  # Away mode never turned on, and the daemon never mutated the child's own
  # records (no approval-authority expansion: it only pokes the pane).
  [ ! -e "$STATE_DIR/.afk" ] || fail "Scenario A: state/.afk appeared during self-supervision"
  [ "$(cat "$STATE_DIR/child-c1.meta")" = "$meta_before" ] \
    || fail "Scenario A: daemon mutated the child meta (it must never advance/teardown a child itself)"
  case "$(cat "$STATE_DIR/child-c1.status")" in
    "$status_before"*) ;;
    *) fail "Scenario A: daemon rewrote the child status (it must only append-nothing; the secondmate advances)" ;;
  esac

  stop_daemon
  pass "Scenario A: self-supervise daemon autonomously wakes the secondmate's own pane (no captain, no .afk) and stays live for the next event"
}

# --- Scenario B: idle self-exit ---------------------------------------------

test_self_supervise_idle_exit() {
  rm -f "$STATE_DIR"/child-c1.meta "$STATE_DIR"/child-c1.status \
        "$STATE_DIR"/.self-supervise-idle-since "$STATE_DIR"/.wake-queue* 2>/dev/null || true
  date '+%s' > "$STATE_DIR/.self-supervise"
  rm -f "$STATE_DIR/.afk"
  # Zero in-flight metas; short idle grace so the daemon self-exits promptly.
  start_daemon FM_SELF_SUPERVISE_IDLE_EXIT_SECS=2

  local i=0
  while [ "$i" -lt 100 ]; do
    kill -0 "$DAEMON_PID" 2>/dev/null || break
    sleep 0.1
    i=$((i + 1))
  done
  if kill -0 "$DAEMON_PID" 2>/dev/null; then
    fail "Scenario B: daemon did not self-exit after idle grace with zero in-flight work"
  fi
  grep -q 'self-supervise idle exit' "$STATE_DIR/.supervise-daemon.log" 2>/dev/null \
    || fail "Scenario B: daemon exited but did not log the self-supervise idle exit"
  DAEMON_PID=""
  pass "Scenario B: an empty-queue self-supervise daemon self-exits cleanly"
}

test_self_supervise_autonomous_wake
test_self_supervise_idle_exit

echo "all self-supervision e2e tests passed"
