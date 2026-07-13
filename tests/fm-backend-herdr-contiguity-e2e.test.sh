#!/usr/bin/env bash
# tests/fm-backend-herdr-contiguity-e2e.test.sh - mandatory ISOLATED real-herdr
# end-to-end test for native workspace contiguity maintenance
# (docs/herdr-backend.md "Workspace contiguity"): the typed raw-socket
# workspace.move writer (bin/backends/herdr-move.py), the depth-first
# contiguity reconciler (fm_backend_herdr_contiguity_reconcile), its
# fm-spawn.sh / fm-teardown.sh lifecycle hooks, and the explicit
# bin/fm-herdr-regroup.sh entry point.
#
# Empirically pins, against the real binary:
#   - workspace.move semantics: insert_index is the moved workspace's FINAL
#     0-based position (remove-then-insert), both directions
#   - non-destructive: the moved workspace keeps its id, tabs, panes, and a
#     registered agent's state; no focus is stolen by moves or reconciles
#   - the canonical depth-first render across a real spawn lifecycle
#     (captain-mandated: supervisor, then its DIRECT crews, then each child
#     secondmate followed by that secondmate's own crews)
#   - teardown keeps the surviving blocks contiguous
#   - manual drift is repaired by fm-herdr-regroup.sh, which is then a strict
#     no-op on the second run (live idempotence, counted via a writer shim)
#   - an unrelated manual workspace is never moved
#
# Mirrors tests/fm-backend-herdr-child-workspace-e2e.test.sh's isolated
# convention: a private throwaway fm-lab session (never the captain's
# default), scratch FM_HOMEs, scratch local-only projects, cleanup ONLY
# through herdr_safe_stop_and_delete (bin/fm-herdr-lab.sh's refuse-default
# teardown), and a default-session fingerprint tripwire.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() { printf 'not ok - %s\n' "$1" >&2; cleanup_all; exit 1; }
pass() { printf 'ok - %s\n' "$1"; }

command -v herdr >/dev/null 2>&1 || { echo "skip: herdr not found"; exit 0; }
command -v jq >/dev/null 2>&1 || { echo "skip: jq not found (required by the herdr adapter)"; exit 0; }
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not found (required by the move writer)"; exit 0; }
command -v treehouse >/dev/null 2>&1 || { echo "skip: treehouse not found (required by fm-spawn.sh)"; exit 0; }

# shellcheck source=tests/herdr-test-safety.sh
. "$ROOT/tests/herdr-test-safety.sh"

TMP_ROOT=$(mktemp -d "$(cd "${TMPDIR:-/tmp}" && pwd -P)/fm-herdr-contig-e2e.XXXXXX")
SESSION="fm-lab-herdr-contig-e2e-$$"
export HERDR_SESSION="$SESSION"
WTS=()
_CLEANED=
cleanup_all() {
  [ -n "$_CLEANED" ] && return 0
  _CLEANED=1
  local wt
  for wt in "${WTS[@]:-}"; do
    [ -n "$wt" ] && command -v treehouse >/dev/null 2>&1 && treehouse return --force "$wt" >/dev/null 2>&1
  done
  herdr_safe_stop_and_delete "$SESSION"
  rm -rf "$TMP_ROOT"
}
trap cleanup_all EXIT

default_fingerprint() {
  local running count
  running=$(herdr session list 2>/dev/null | awk '$1=="default"{print $2}')
  count=$(herdr workspace list --session default 2>/dev/null | jq -r '.result.workspaces | length' 2>/dev/null)
  echo "running=$running workspace_count=$count"
}
DEFAULT_BEFORE=$(default_fingerprint)

fm_herdr_lab_prepare "$SESSION" || fail "could not prepare isolated Herdr lab session"

# shellcheck source=bin/fm-backend.sh
. "$ROOT/bin/fm-backend.sh"
fm_backend_source herdr || fail "fm_backend_source herdr failed"
fm_backend_herdr_server_ensure "$SESSION" || fail "could not start the isolated lab herdr server"

order_labels() {  # the live flat order as one label per line
  fm_backend_herdr_cli "$SESSION" workspace list 2>/dev/null | jq -r '.result.workspaces[]?.label'
}
focused_ws() {
  fm_backend_herdr_cli "$SESSION" workspace list 2>/dev/null | jq -r '.result.workspaces[]? | select(.focused == true) | .workspace_id'
}
ws_tab_ids() {  # <workspace_id> -> sorted tab ids
  fm_backend_herdr_cli "$SESSION" tab list --workspace "$1" 2>/dev/null | jq -r '.result.tabs[]?.tab_id' | sort
}
mk_ws() {  # <label> -> workspace_id
  fm_backend_herdr_cli "$SESSION" workspace create --cwd /tmp --label "$1" --no-focus 2>/dev/null \
    | jq -r '.result.workspace.workspace_id // empty'
}
meta_get() { grep "^$2=" "$1" 2>/dev/null | cut -d= -f2-; }

make_home() {  # <dir> [<secondmate-id>]
  local dir=$1 smid=${2:-}
  mkdir -p "$dir/state" "$dir/data" "$dir/config" "$dir/projects" "$dir/bin"
  printf '# scratch home AGENTS.md\n' > "$dir/AGENTS.md"
  if [ -n "$smid" ]; then
    printf '%s\n' "$smid" > "$dir/.fm-secondmate-home"
    printf 'trivial e2e secondmate charter: nothing to do.\n' > "$dir/data/charter.md"
  fi
  printf 'on\n' > "$dir/config/herdr-child-workspaces"
}
make_project() {  # <dir>
  local dir=$1
  mkdir -p "$dir"
  git -C "$dir" init -q
  printf '# scratch\n' > "$dir/README.md"
  git -C "$dir" add README.md
  git -C "$dir" -c user.name='Firstmate Tests' -c user.email='tests@example.invalid' commit -qm initial
}
spawn() {  # <home> <id> <project> <launch> [extra fm-spawn args...]
  local home=$1 id=$2 proj=$3 launch=$4; shift 4
  local out="$TMP_ROOT/$id.out" err="$TMP_ROOT/$id.err"
  mkdir -p "$home/data/$id"
  printf 'trivial e2e brief for %s: nothing to do.\n' "$id" > "$home/data/$id/brief.md"
  FM_SPAWN_NO_GUARD=1 FM_HOME="$home" FM_ROOT_OVERRIDE="$ROOT" \
    "$ROOT/bin/fm-spawn.sh" "$id" "$proj" "$launch" --backend herdr "$@" >"$out" 2>"$err"
  local rc=$?
  [ "$rc" -eq 0 ] || fail "spawn $id failed"$'\n'"--- stdout ---"$'\n'"$(cat "$out")"$'\n'"--- stderr ---"$'\n'"$(cat "$err")"
  local wt; wt=$(meta_get "$home/state/$id.meta" worktree)
  [ -n "$wt" ] && WTS+=("$wt")
}
teardown() {  # <home> <id>
  local home=$1 id=$2
  local out="$TMP_ROOT/td-$id.out"
  FM_ROOT_OVERRIDE="$ROOT" FM_STATE_OVERRIDE="$home/state" FM_DATA_OVERRIDE="$home/data" \
    FM_CONFIG_OVERRIDE="$home/config" \
    "$ROOT/bin/fm-teardown.sh" "$id" >"$out" 2>&1
  local rc=$?
  [ "$rc" -eq 0 ] || fail "teardown $id failed"$'\n'"$(cat "$out")"
}

# === A. workspace.move semantics: insert_index is the FINAL 0-based index ====
WS_A=$(mk_ws manual-base); [ -n "$WS_A" ] || fail "could not create workspace manual-base"
WS_B=$(mk_ws probe-b);     [ -n "$WS_B" ] || fail "could not create workspace probe-b"
WS_C=$(mk_ws probe-c);     [ -n "$WS_C" ] || fail "could not create workspace probe-c"
[ "$(order_labels)" = $'manual-base\nprobe-b\nprobe-c' ] || fail "unexpected creation order: $(order_labels | tr '\n' ' ')"

# The pinned wire semantics (also verified by hand against 0.7.3):
# insert_index addresses the PRE-removal array - a leftward move lands AT
# insert_index, a rightward move lands at insert_index-1, and insert_index
# == list length is valid (lands last).
fm_backend_herdr_workspace_move "$SESSION" "$WS_C" 0 || fail "leftward workspace.move failed"
[ "$(order_labels)" = $'probe-c\nmanual-base\nprobe-b' ] || fail "leftward move: expected probe-c to land AT insert_index 0, got: $(order_labels | tr '\n' ' ')"
fm_backend_herdr_workspace_move "$SESSION" "$WS_C" 2 || fail "rightward mid workspace.move failed"
[ "$(order_labels)" = $'manual-base\nprobe-c\nprobe-b' ] || fail "rightward move: insert_index 2 must land probe-c at final index 1 (pre-removal addressing), got: $(order_labels | tr '\n' ' ')"
fm_backend_herdr_workspace_move "$SESSION" "$WS_C" 3 || fail "rightward end workspace.move (insert_index == length) failed"
[ "$(order_labels)" = $'manual-base\nprobe-b\nprobe-c' ] || fail "rightward move: insert_index == length must land probe-c last, got: $(order_labels | tr '\n' ' ')"
pass "real herdr ($(herdr --version 2>/dev/null | head -1)): workspace.move insert_index addresses the PRE-removal array (leftward lands AT it, rightward lands one before it, length is valid)"

fm_backend_herdr_cli "$SESSION" workspace close "$WS_B" >/dev/null 2>&1 || fail "could not close probe-b"
fm_backend_herdr_cli "$SESSION" workspace close "$WS_C" >/dev/null 2>&1 || fail "could not close probe-c"
FOCUS_BASE=$(focused_ws)

# === B. canonical depth-first render across a REAL spawn lifecycle ===========
# Spawn order deliberately displaces the last direct crew: first-crew, then
# the secondmate and BOTH its crews, then release-notes - which herdr appends
# at the very end of the flat list, after the whole secondmate subtree. The
# spawn-time reconcile must pull it up into the firstmate block with a real
# socket move, producing the captain-mandated render.
PRIMARY="$TMP_ROOT/primary";      make_home "$PRIMARY"
SM_HOME="$TMP_ROOT/hibit-home";   make_home "$SM_HOME" "hibit-h9"
PROJ1="$TMP_ROOT/proj1"; make_project "$PROJ1"
PROJ2="$TMP_ROOT/proj2"; make_project "$PROJ2"

spawn "$PRIMARY" first-crew "$PROJ1" "sh -c 'echo fc-ok'"
spawn "$PRIMARY" hibit-h9 "$SM_HOME" "sh -c 'echo sm-ok'" --secondmate
spawn "$SM_HOME" local-sandbox "$PROJ1" "sh -c 'echo ls-ok'"
spawn "$SM_HOME" game-parity "$PROJ2" "sh -c 'echo gp-ok'"
spawn "$PRIMARY" release-notes "$PROJ2" "sh -c 'echo rn-ok'"

CANONICAL=$'manual-base\nfirstmate\nfirstmate/first-crew\nfirstmate/release-notes\n2ndmate-hibit-h9\n2ndmate-hibit-h9/local-sandbox\n2ndmate-hibit-h9/game-parity'
[ "$(order_labels)" = "$CANONICAL" ] || fail "canonical depth-first render violated after the lifecycle spawns; expected:"$'\n'"$CANONICAL"$'\n'"--- got ---"$'\n'"$(order_labels)"
pass "canonical render: supervisor, then its DIRECT crews (release-notes pulled up by a real socket move), then the secondmate and its crews - direct crews before the secondmate subtree"
[ "$(focused_ws)" = "$FOCUS_BASE" ] || fail "the spawn-time reconcile stole focus: was $FOCUS_BASE, now $(focused_ws)"
pass "focus preserved: spawns and reconcile moves never changed the focused workspace"

# === C. teardown keeps the surviving blocks contiguous ========================
teardown "$PRIMARY" first-crew
[ "$(order_labels)" = $'manual-base\nfirstmate\nfirstmate/release-notes\n2ndmate-hibit-h9\n2ndmate-hibit-h9/local-sandbox\n2ndmate-hibit-h9/game-parity' ] \
  || fail "teardown must leave the surviving blocks contiguous, got:"$'\n'"$(order_labels)"
pass "teardown: removing a crew leaves every surviving block contiguous"
# Re-baseline focus here: herdr's own `workspace close` re-focuses a neighbor
# even when the closed workspace was NOT focused (observed against 0.7.3; a
# close/teardown behavior, not a move behavior - the lab probe shows
# workspace.move never changes focus in either direction). The assertions
# below pin that the MOVES from here on never change focus again.
FOCUS_AFTER_TEARDOWN=$(focused_ws)

# === D. a move never disturbs a live registered agent, its tabs, or panes ====
RN_META="$PRIMARY/state/release-notes.meta"
RN_WS=$(meta_get "$RN_META" herdr_workspace_id)
RN_PANE=$(meta_get "$RN_META" herdr_pane_id)
[ -n "$RN_WS" ] && [ -n "$RN_PANE" ] || fail "release-notes meta is missing workspace/pane ids"
fm_backend_herdr_cli "$SESSION" pane report-agent "$RN_PANE" --source fm-contig-e2e --agent claude --state working >/dev/null 2>&1 \
  || fail "could not register a fake agent on release-notes' runtime pane"
RN_TABS_BEFORE=$(ws_tab_ids "$RN_WS")

# Manual drift: push release-notes' workspace to the end of the flat list
# (wire insert_index == list length lands last on a rightward move), exactly
# what a hand-shuffle in the sidebar produces.
WS_COUNT=$(order_labels | grep -c .)
fm_backend_herdr_workspace_move "$SESSION" "$RN_WS" "$WS_COUNT" || fail "manual-drift workspace.move failed"
[ "$(order_labels | tail -1)" = "firstmate/release-notes" ] || fail "manual drift did not land release-notes last"
[ "$(ws_tab_ids "$RN_WS")" = "$RN_TABS_BEFORE" ] || fail "a workspace.move changed the moved workspace's tabs"
[ "$(fm_backend_herdr_agent_status_raw "$SESSION" "$RN_PANE")" = "working" ] || fail "a workspace.move disturbed the registered agent's state"
pass "non-destructive: a real workspace.move keeps the workspace's tabs, panes, and registered agent state untouched"

# === E. explicit regroup repairs the drift; second run is a strict no-op =====
SHIM="$TMP_ROOT/move-shim.sh"
MOVELOG="$TMP_ROOT/move-shim.log"
cat > "$SHIM" <<SH
#!/usr/bin/env bash
printf '%s %s\n' "\$2" "\$3" >> "$MOVELOG"
exec python3 "$ROOT/bin/backends/herdr-move.py" "\$@"
SH
chmod +x "$SHIM"
: > "$MOVELOG"
FM_HOME="$PRIMARY" FM_STATE_OVERRIDE="$PRIMARY/state" FM_ROOT_OVERRIDE="$ROOT" \
  FM_BACKEND_HERDR_MOVE_WRITER="$SHIM" \
  "$ROOT/bin/fm-herdr-regroup.sh" --all >"$TMP_ROOT/regroup1.out" 2>&1 \
  || fail "fm-herdr-regroup.sh failed:"$'\n'"$(cat "$TMP_ROOT/regroup1.out")"
[ "$(order_labels)" = $'manual-base\nfirstmate\nfirstmate/release-notes\n2ndmate-hibit-h9\n2ndmate-hibit-h9/local-sandbox\n2ndmate-hibit-h9/game-parity' ] \
  || fail "regroup did not restore the canonical order, got:"$'\n'"$(order_labels)"
[ "$(grep -c . "$MOVELOG")" = 1 ] || fail "the drift repair must take exactly one move, got: $(cat "$MOVELOG")"
[ "$(fm_backend_herdr_agent_status_raw "$SESSION" "$RN_PANE")" = "working" ] || fail "the regroup disturbed the registered agent"
pass "regroup: bin/fm-herdr-regroup.sh repairs manual drift with exactly one move, agent untouched"

: > "$MOVELOG"
FM_HOME="$PRIMARY" FM_STATE_OVERRIDE="$PRIMARY/state" FM_ROOT_OVERRIDE="$ROOT" \
  FM_BACKEND_HERDR_MOVE_WRITER="$SHIM" \
  "$ROOT/bin/fm-herdr-regroup.sh" --all >"$TMP_ROOT/regroup2.out" 2>&1 \
  || fail "idempotent regroup failed:"$'\n'"$(cat "$TMP_ROOT/regroup2.out")"
[ ! -s "$MOVELOG" ] || fail "a second regroup on a correct order must issue ZERO moves, got: $(cat "$MOVELOG")"
pass "idempotence: regrouping an already-correct live order issues zero moves (no churn)"

[ "$(order_labels | head -1)" = "manual-base" ] || fail "the unrelated manual workspace must never be moved"
[ "$(focused_ws)" = "$FOCUS_AFTER_TEARDOWN" ] || fail "the drift move / regroup moves stole focus: was $FOCUS_AFTER_TEARDOWN, now $(focused_ws)"
pass "unrelated preservation: the manual workspace kept its position, and no move or regroup changed focus"

# tidy remaining jobs (the secondmate task stays, exactly like the
# child-workspace e2e; the session teardown owns the rest)
teardown "$PRIMARY" release-notes
teardown "$SM_HOME" local-sandbox
teardown "$SM_HOME" game-parity

# === F. default session untouched throughout =================================
DEFAULT_AFTER=$(default_fingerprint)
[ "$DEFAULT_AFTER" = "$DEFAULT_BEFORE" ] || fail "the default session changed! before='$DEFAULT_BEFORE' after='$DEFAULT_AFTER'"
pass "the captain's default herdr session is byte-identical (running-state + workspace count) before and after"

echo "# all workspace-contiguity E2E assertions passed"
