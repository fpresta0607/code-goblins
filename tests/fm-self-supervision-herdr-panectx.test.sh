#!/usr/bin/env bash
# tests/fm-self-supervision-herdr-panectx.test.sh - herdr pane-context proof for
# the self-supervision auto-wire: `fm-afk-launch.sh ensure-self-supervise`, given
# a herdr secondmate's OWN pane, creates the self-supervise daemon terminal in
# THAT secondmate's own herdr session (never the live default session) and records
# the correct herdr target. The herdr INJECTION transport itself is covered by
# tests/fm-afk-inject-herdr-e2e.test.sh, and the pane-capture composition
# (<session>:<pane>) by tests/fm-daemon.test.sh; this test owns the remaining
# link: the auto-wire targets the secondmate's own herdr pane/session per backend.
#
# HARD SAFETY: all herdr work goes through bin/fm-herdr-lab.sh - a never-`default`
# fm-lab-* session, a trailing --session on every call, guarded teardown, and a
# fleet-state tripwire asserting the live default session is byte-identical
# before/after. Uses FM_AFK_LAUNCH_ENTRY=/bin/true so no real daemon runs - this
# is a topology/record assertion, not an injection test.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LAUNCH="$ROOT/bin/fm-afk-launch.sh"
HELPER="$ROOT/bin/fm-herdr-lab.sh"

command -v herdr >/dev/null 2>&1 || { echo "skip: herdr not found"; exit 0; }
command -v jq >/dev/null 2>&1 || { echo "skip: jq not found"; exit 0; }

WORK=$(mktemp -d "${TMPDIR:-/tmp}/fm-ss-herdr.XXXXXX")
LAB=$("$HELPER" name selfsup-panectx)
HOME_DIR="$WORK/home"
mkdir -p "$HOME_DIR/state"

fail() { printf 'not ok - %s\n' "$1" >&2; exit 1; }
pass() { printf 'ok - %s\n' "$1"; }

cleanup() {
  # Best-effort stop of any daemon terminal the launcher recorded, then guarded
  # lab teardown (re-checks refuse-default and the fleet tripwire).
  env FM_HOME="$HOME_DIR" "$LAUNCH" stop-self-supervise >/dev/null 2>&1 || true
  "$HELPER" teardown "$LAB" >/dev/null 2>&1 || true
  rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 143' TERM INT

# Provision an isolated lab session and a real "secondmate pane" in it.
"$HELPER" provision "$LAB" >/dev/null 2>&1 || { echo "skip: could not provision herdr lab"; exit 0; }
WS_JSON=$("$HELPER" run "$LAB" workspace create --cwd "$HOME_DIR" --label secondmate-pane --no-focus 2>/dev/null)
PANE=$(printf '%s' "$WS_JSON" | jq -r '.result.root_pane.pane_id // empty' 2>/dev/null)
[ -n "$PANE" ] || { echo "skip: could not create lab pane"; exit 0; }
OWN_TARGET="$LAB:$PANE"

# One in-flight child so ensure-self-supervise does not treat the home as idle.
cat > "$HOME_DIR/state/child.meta" <<META
window=$LAB:childpane
kind=ship
mode=local-only
META
printf 'working: child\n' > "$HOME_DIR/state/child.status"

# Workspaces in the lab BEFORE (to prove the daemon terminal lands in the lab).
ws_before=$("$HELPER" run "$LAB" workspace list 2>/dev/null | jq '[.result.workspaces[]?] | length' 2>/dev/null)

# The exact auto-wire call: ensure with the secondmate's OWN herdr pane. Scope
# every daemon herdr call to the lab via HERDR_SESSION + the parsed target
# session, never default. /bin/true stands in for the daemon (topology only).
env FM_HOME="$HOME_DIR" \
    HERDR_SESSION="$LAB" \
    FM_SUPERVISOR_TARGET="$OWN_TARGET" \
    FM_SUPERVISOR_BACKEND=herdr \
    FM_AFK_LAUNCH_ENTRY=/bin/true \
    "$LAUNCH" ensure-self-supervise >/dev/null 2>&1 || true

# (1) The recorded self-supervise target is the secondmate's OWN herdr pane.
rec=$(cat "$HOME_DIR/state/.self-supervise-target" 2>/dev/null || true)
[ "$rec" = "$(printf 'herdr\t%s' "$OWN_TARGET")" ] \
  || fail "recorded target is not the secondmate's own herdr pane (got: $rec)"

# (2) state/.self-supervise set, state/.afk NEVER created.
[ -f "$HOME_DIR/state/.self-supervise" ] || fail "state/.self-supervise not set"
[ ! -e "$HOME_DIR/state/.afk" ] || fail "state/.afk must never be created by self-supervise"

# (3) The daemon terminal was created IN THE LAB SESSION (a new workspace), not
# in default. A firstmate-afk-daemon-* workspace now exists in the lab.
ws_after=$("$HELPER" run "$LAB" workspace list 2>/dev/null | jq '[.result.workspaces[]?] | length' 2>/dev/null)
[ "${ws_after:-0}" -gt "${ws_before:-0}" ] \
  || fail "no new daemon workspace appeared in the lab session (before=$ws_before after=$ws_after)"
daemon_ws=$("$HELPER" run "$LAB" workspace list 2>/dev/null \
  | jq -r '[.result.workspaces[]? | select(.label | startswith("firstmate-afk-daemon"))] | length' 2>/dev/null)
[ "${daemon_ws:-0}" -ge 1 ] \
  || fail "the self-supervise daemon terminal was not created in the secondmate's own lab session"

# (4) The recorded daemon terminal is scoped to the LAB session (never default).
term_rec=$(cat "$HOME_DIR/state/.afk-daemon-terminal" 2>/dev/null || true)
case "$term_rec" in
  "herdr	$LAB:"*) ;;
  *) fail "daemon terminal record is not scoped to the lab session (got: $term_rec)" ;;
esac

pass "herdr pane-context: ensure-self-supervise targets the secondmate's own lab pane and creates the daemon terminal in the lab session, never default; .afk never set"
echo "all self-supervision herdr pane-context tests passed"
