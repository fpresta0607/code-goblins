#!/usr/bin/env bash
# tests/fm-self-supervision-autowire-e2e.test.sh - end-to-end proof that a
# persistent secondmate's self-supervision is CODE-LEVEL automatic: no model
# action is needed to start, keep, restart, or stop the daemon that supervises
# its children. Exercises the real `fm-afk-launch.sh ensure-self-supervise`
# auto-wire path (the same call bin/fm-spawn.sh makes on dispatch and
# bin/fm-bootstrap.sh's liveness sweep makes on reconcile), including the real
# non-visible daemon-terminal creation.
#
#   Scenario A (pane-context + full lifecycle, no model action):
#     ensure-self-supervise with the secondmate's OWN pane -> daemon starts in a
#     detached terminal -> child writes `done` while the pane is idle -> daemon
#     injects a resume ONLY into that pane -> a second event injects again
#     (stays live) -> the daemon is KILLED (crash) -> a reconcile ensure restarts
#     it and injection resumes -> child work removed -> the daemon self-exits.
#     Throughout: state/.afk is never created, and the daemon never mutates a
#     child meta (no approval-authority expansion).
#
#   Scenario B (multi-home isolation): two secondmate homes each get their own
#     single daemon targeting their OWN pane; a child event in one home injects
#     only into that home's pane, and stopping one home's daemon leaves the
#     other's live.
#
# Isolation: a dedicated private tmux socket (tmux -L). A tmux shim first on PATH
# redirects every bare `tmux` - the launcher's terminal creation AND the daemon's
# injection - to that socket. Never touches the live fleet or any herdr session.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DAEMON="$ROOT/bin/fm-supervise-daemon.sh"
LAUNCH="$ROOT/bin/fm-afk-launch.sh"

command -v tmux >/dev/null 2>&1 || { echo "skip: tmux not found"; exit 0; }

REAL_TMUX=$(command -v tmux)
SOCKET="fm-ssaw-e2e-$$"
SHIM_DIR=
WORK=
HOMES=()

fail() { printf 'not ok - %s\n' "$1" >&2; cleanup_all; exit 1; }
pass() { printf 'ok - %s\n' "$1"; }

cleanup_all() {
  local h pid
  for h in "${HOMES[@]:-}"; do
    [ -n "$h" ] || continue
    pid=$(cat "$h/state/.supervise-daemon.pid" 2>/dev/null || true)
    [ -n "$pid" ] && kill -9 "$pid" 2>/dev/null || true
  done
  [ -n "$REAL_TMUX" ] && "$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null || true
  rm -rf "${SHIM_DIR:-}" "${WORK:-}" 2>/dev/null || true
}
trap cleanup_all EXIT
trap 'exit 143' TERM INT

WORK=$(mktemp -d "${TMPDIR:-/tmp}/fm-ssaw.XXXXXX")

# tmux shim: redirect every bare `tmux` to the private socket.
SHIM_DIR=$(mktemp -d "${TMPDIR:-/tmp}/fm-ssaw-shim.XXXXXX")
cat > "$SHIM_DIR/tmux" <<SHIM
#!/usr/bin/env bash
exec "$REAL_TMUX" -L "$SOCKET" "\$@"
SHIM
chmod +x "$SHIM_DIR/tmux"

# The supervisor-pane composer loop: logs each submitted line, classified
# injection (sentinel-prefixed) vs user. Same proven loop as the other e2es.
LOOP_SCRIPT="$WORK/loop.sh"
cat > "$LOOP_SCRIPT" <<'LOOP'
#!/usr/bin/env bash
MARK=$'\x1f'
LOG="$1"
OLD_STTY=$(stty -g 2>/dev/null || true)
[ -z "$OLD_STTY" ] || stty -echo -icanon min 1 time 0 2>/dev/null || true
trap '[ -z "$OLD_STTY" ] || stty "$OLD_STTY" 2>/dev/null || true' EXIT INT TERM
_buf=
redraw() { printf '\r\033[K%s' "$_buf"; }
submit_line() {
  local _c _hex
  if [ "${_buf:0:1}" = "$MARK" ]; then _c=injection; else _c=user; fi
  _hex=$(printf '%s' "$_buf" | od -An -tx1 | tr -d ' \n')
  printf '%s\t%s\t%s\n' "$_hex" "$_buf" "$_c" >> "$LOG"
  _buf=; printf '\r\033[K\n'; redraw
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

# Entry wrapper for the daemon terminal: put the shim on PATH (so the daemon's
# bare tmux injection hits the private socket) and set fast timers. IDLE_EXIT is
# left generous; a scenario overrides it via a per-home wrapper when it wants to
# assert self-exit.
make_wrapper() {  # <path> <idle-exit-secs>
  cat > "$1" <<W
#!/usr/bin/env bash
export PATH="$SHIM_DIR:\$PATH"
exec env FM_POLL=1 FM_HOUSEKEEPING_TICK=1 FM_SIGNAL_GRACE=1 FM_ESCALATE_BATCH_SECS=0 \\
  FM_HEARTBEAT=999999 FM_CHECK_INTERVAL=999999 FM_STALE_ESCALATE_SECS=999999 \\
  FM_INJECT_CONFIRM_SLEEP=0.3 FM_INJECT_CONFIRM_RETRIES=5 \\
  FM_SELF_SUPERVISE_IDLE_EXIT_SECS=$2 "$DAEMON"
W
  chmod +x "$1"
}

# make_home <name>: an isolated secondmate-shaped home with its OWN supervisor
# pane on the private socket. Sets HOME_DIR / PANE / LOG.
make_home() {  # <name>
  local name=$1
  HOME_DIR="$WORK/$name"
  mkdir -p "$HOME_DIR/state"
  : > "$HOME_DIR/.fm-secondmate-home"
  LOG="$HOME_DIR/submitted.log"; : > "$LOG"
  "$REAL_TMUX" -L "$SOCKET" new-session -d -s "$name" -x 200 -y 50
  PANE=$("$REAL_TMUX" -L "$SOCKET" display-message -p -t "$name" '#{pane_id}')
  "$REAL_TMUX" -L "$SOCKET" send-keys -t "$PANE" "bash '$LOOP_SCRIPT' '$LOG'" Enter
  sleep 1
  HOMES+=("$HOME_DIR")
}

add_child() {  # <home> <child-id>
  cat > "$1/state/$2.meta" <<META
window=fm-$2
kind=ship
mode=local-only
META
  printf 'working: %s building\n' "$2" > "$1/state/$2.status"
}

ensure() {  # <home> <pane> <idle-secs>
  local wrapper="$1/daemon-entry.sh"
  make_wrapper "$wrapper" "$3"
  env PATH="$SHIM_DIR:$PATH" \
      FM_HOME="$1" \
      FM_SUPERVISOR_TARGET="$2" \
      FM_SUPERVISOR_BACKEND=tmux \
      FM_AFK_LAUNCH_ENTRY="$wrapper" \
      "$LAUNCH" ensure-self-supervise >/dev/null 2>&1
}

wait_daemon_up() {  # <home>
  local i=0
  while [ "$i" -lt 60 ]; do
    [ -f "$1/state/.supervise-daemon.pid" ] && \
      kill -0 "$(cat "$1/state/.supervise-daemon.pid" 2>/dev/null)" 2>/dev/null && return 0
    sleep 0.2; i=$((i + 1))
  done
  return 1
}

count_inj() { local n; n=$(grep -c $'\tinjection$' "$1" 2>/dev/null) || true; printf '%s' "${n:-0}"; }

wait_inj() {  # <log> <min> <deciseconds>
  local i=0
  while [ "$i" -lt "${3:-150}" ]; do
    [ "$(count_inj "$1")" -ge "$2" ] && return 0
    sleep 0.1; i=$((i + 1))
  done
  return 1
}

# --- Scenario A: pane-context + full lifecycle, no model action -------------

test_full_lifecycle() {
  make_home A
  local home=$HOME_DIR pane=$PANE log=$LOG
  add_child "$home" c1

  # (1) DISPATCH auto-start: exactly the call fm-spawn.sh makes, no model action.
  ensure "$home" "$pane" 3
  wait_daemon_up "$home" || fail "A: daemon did not auto-start on dispatch"
  [ ! -e "$home/state/.afk" ] || fail "A: state/.afk must never be created"
  [ -f "$home/state/.self-supervise" ] || fail "A: state/.self-supervise missing after auto-start"
  # Pane-context: the recorded + used target is the secondmate's OWN pane.
  grep -q "	$pane$" "$home/state/.self-supervise-target" \
    || fail "A: recorded supervisor target is not the secondmate's own pane ($pane)"

  local meta_before; meta_before=$(cat "$home/state/c1.meta")

  # (2) Autonomous wake: child done while the pane is idle -> ONE injection here.
  printf 'done: c1 PR ready\n' >> "$home/state/c1.status"
  wait_inj "$log" 1 200 || fail "A: no autonomous injection after child done"
  local first_hex; first_hex=$(grep $'\tinjection$' "$log" | head -1 | cut -f1)
  case "$first_hex" in 1f*) ;; *) fail "A: injection not sentinel-prefixed ($first_hex)" ;; esac

  # (3) Stays live: a second event injects again.
  printf 'blocked: c1 needs a decision\n' >> "$home/state/c1.status"
  wait_inj "$log" 2 200 || fail "A: supervision did not stay live for the next event"

  # (4) CRASH the daemon, then RECONCILE (the sweep's ensure) restarts it.
  local pid; pid=$(cat "$home/state/.supervise-daemon.pid")
  kill -9 "$pid" 2>/dev/null || true
  local i=0; while [ "$i" -lt 40 ] && kill -0 "$pid" 2>/dev/null; do sleep 0.1; i=$((i + 1)); done
  kill -0 "$pid" 2>/dev/null && fail "A: daemon did not die on crash"
  local inj_before_reconcile; inj_before_reconcile=$(count_inj "$log")

  ensure "$home" "$pane" 3            # reconcile + restart, child work still present
  wait_daemon_up "$home" || fail "A: reconcile did not restart the daemon after a crash"
  local newpid; newpid=$(cat "$home/state/.supervise-daemon.pid")
  [ "$newpid" != "$pid" ] || fail "A: reconcile reused the dead pid"
  printf 'done: c1 fix checks green\n' >> "$home/state/c1.status"
  wait_inj "$log" "$((inj_before_reconcile + 1))" 200 \
    || fail "A: injection did not resume after crash-recovery restart"

  # (5) Invariants held through the whole run.
  [ ! -e "$home/state/.afk" ] || fail "A: state/.afk appeared during the run"
  [ "$(cat "$home/state/c1.meta")" = "$meta_before" ] \
    || fail "A: daemon mutated the child meta (approval-authority expansion)"

  # (6) Idle self-exit once child work is gone (no model action).
  rm -f "$home/state/c1.meta" "$home/state/c1.status"
  i=0; while [ "$i" -lt 120 ] && kill -0 "$newpid" 2>/dev/null; do sleep 0.1; i=$((i + 1)); done
  kill -0 "$newpid" 2>/dev/null && fail "A: daemon did not self-exit after child work was removed"
  grep -q 'self-supervise idle exit' "$home/state/.supervise-daemon.log" \
    || fail "A: idle self-exit not logged"

  pass "A: dispatch auto-start -> autonomous wake -> stays live -> crash reconcile restart -> idle self-exit, all with no model action and no .afk"
}

# --- Scenario B: multi-home isolation ---------------------------------------

test_multi_home_isolation() {
  make_home B1; local h1=$HOME_DIR p1=$PANE l1=$LOG
  make_home B2; local h2=$HOME_DIR p2=$PANE l2=$LOG
  add_child "$h1" x1
  add_child "$h2" y1

  ensure "$h1" "$p1" 999999
  ensure "$h2" "$p2" 999999
  wait_daemon_up "$h1" || fail "B: home1 daemon did not start"
  wait_daemon_up "$h2" || fail "B: home2 daemon did not start"

  local pid1 pid2; pid1=$(cat "$h1/state/.supervise-daemon.pid"); pid2=$(cat "$h2/state/.supervise-daemon.pid")
  [ "$pid1" != "$pid2" ] || fail "B: the two homes share one daemon pid (not per-home singletons)"

  # A child event in home1 injects ONLY into home1's pane.
  printf 'done: x1 ready\n' >> "$h1/state/x1.status"
  wait_inj "$l1" 1 200 || fail "B: home1 got no injection"
  [ "$(count_inj "$l2")" -eq 0 ] || fail "B: home1's event leaked an injection into home2's pane"

  # A child event in home2 injects ONLY into home2's pane.
  printf 'done: y1 ready\n' >> "$h2/state/y1.status"
  wait_inj "$l2" 1 200 || fail "B: home2 got no injection"
  [ "$(count_inj "$l1")" -eq 1 ] || fail "B: home2's event leaked an injection into home1's pane"

  # Stopping home1's daemon leaves home2's live and still injecting.
  env FM_HOME="$h1" "$LAUNCH" stop-self-supervise >/dev/null 2>&1 || true
  kill -0 "$pid2" 2>/dev/null || fail "B: stopping home1 killed home2's daemon"
  printf 'blocked: y1 wait\n' >> "$h2/state/y1.status"
  wait_inj "$l2" 2 200 || fail "B: home2 supervision died after home1 was stopped"

  # Kill home2's daemon to end the scenario cleanly.
  kill -9 "$pid2" 2>/dev/null || true
  pass "B: two secondmate homes run isolated per-home daemons; neither touches the other's pane or state"
}

test_full_lifecycle
test_multi_home_isolation

echo "all self-supervision auto-wire e2e tests passed"
