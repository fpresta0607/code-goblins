#!/usr/bin/env bash
# tests/fm-backend-herdr-child-workspace-e2e.test.sh - mandatory ISOLATED
# end-to-end real-herdr test for the child-workspace hierarchy PROTOTYPE
# (default OFF; config/herdr-child-workspaces=on; docs/herdr-backend.md
# "Child-workspace hierarchy (prototype)").
#
# Drives the REAL bin/fm-spawn.sh and bin/fm-teardown.sh (raw `sh -c` launch
# commands, no real agent), because the behavior under test lives in
# fm-spawn.sh's herdr case arm (the child-workspace branch), the herdr
# backend's create/close/list functions, the meta contract
# (herdr_parent_ws/herdr_ws_owned), and fm-teardown.sh's owned-workspace close.
#
# Mirrors tests/fm-backend-herdr-workspace-per-home-e2e.test.sh's isolated
# convention: a private throwaway fm-lab session (never the captain's default),
# scratch FM_HOME(s), scratch local-only projects, cleanup ONLY through
# herdr_safe_stop_and_delete (bin/fm-herdr-lab.sh's refuse-default teardown).
#
# Covers, per the prototype brief:
#   - flag OFF: byte-identical tab-per-task (no child workspace, no owned meta)
#   - flag ON: a delegated job gets its OWN child workspace "<home>/<id>" with
#     the parent recorded in meta and runtime+log tabs grouped inside it
#   - multiple concurrent jobs -> distinct child workspaces, same parent
#   - nested supervisor homes -> the supervisor stays a tab; its delegated job
#     gets a child workspace under the supervisor's own workspace
#   - restart/recovery: list_live rediscovers child-workspace jobs by label
#   - stale metadata + refuse-to-close-parent safety (function level)
#   - EXACT cleanup: teardown closes only the owned child workspace; parent,
#     siblings, and the default session are untouched
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() { printf 'not ok - %s\n' "$1" >&2; cleanup_all; exit 1; }
pass() { printf 'ok - %s\n' "$1"; }
assert_contains_local() {  # <haystack> <needle> <msg>
  case "$1" in
    *"$2"*) : ;;
    *) fail "$3"$'\n'"--- got ---"$'\n'"$1" ;;
  esac
}
assert_not_contains_local() {  # <haystack> <needle> <msg>
  case "$1" in
    *"$2"*) fail "$3"$'\n'"--- got ---"$'\n'"$1" ;;
    *) : ;;
  esac
}

command -v herdr >/dev/null 2>&1 || { echo "skip: herdr not found"; exit 0; }
command -v jq >/dev/null 2>&1 || { echo "skip: jq not found (required by the herdr adapter)"; exit 0; }
command -v treehouse >/dev/null 2>&1 || { echo "skip: treehouse not found (required by fm-spawn.sh)"; exit 0; }

# shellcheck source=tests/herdr-test-safety.sh
. "$ROOT/tests/herdr-test-safety.sh"

TMP_ROOT=$(mktemp -d "$(cd "${TMPDIR:-/tmp}" && pwd -P)/fm-herdr-childws-e2e.XXXXXX")
SESSION="fm-lab-herdr-childws-e2e-$$"
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

# --- default-session tripwire (non-sensitive: running-state + workspace COUNT
# only, never the captain's live fleet contents) --------------------------------
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

pane_ws() {  # <pane_id> -> its workspace_id
  herdr pane get "$1" --session "$SESSION" 2>/dev/null | jq -r '.result.pane.workspace_id // empty'
}
ws_label() {  # <workspace_id> -> its label
  herdr workspace list --session "$SESSION" 2>&1 | jq -r --arg id "$1" '.result.workspaces[]? | select(.workspace_id == $id) | .label'
}
ws_exists() {  # <workspace_id> -> 0 if present
  herdr workspace get "$1" --session "$SESSION" >/dev/null 2>&1
}
ws_tab_labels() {  # <workspace_id> -> newline-separated tab labels
  herdr tab list --workspace "$1" --session "$SESSION" 2>/dev/null | jq -r '.result.tabs[]?.label'
}
meta_get() { grep "^$2=" "$1" 2>/dev/null | cut -d= -f2-; }

make_home() {  # <dir> [<secondmate-id>] [<flag on|off>]
  local dir=$1 smid=${2:-} flag=${3:-off}
  mkdir -p "$dir/state" "$dir/data" "$dir/config" "$dir/projects" "$dir/bin"
  printf '# scratch home AGENTS.md\n' > "$dir/AGENTS.md"
  if [ -n "$smid" ]; then
    printf '%s\n' "$smid" > "$dir/.fm-secondmate-home"
    printf 'trivial e2e secondmate charter: nothing to do.\n' > "$dir/data/charter.md"
  fi
  [ "$flag" = on ] && printf 'on\n' > "$dir/config/herdr-child-workspaces"
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

PRIMARY_ON="$TMP_ROOT/primary-on";     make_home "$PRIMARY_ON" "" on
OFF_HOME="$TMP_ROOT/primary-off";      make_home "$OFF_HOME" "" off
SM_HOME="$TMP_ROOT/secondmate-home";   make_home "$SM_HOME" "childe2" on
PROJ1="$TMP_ROOT/proj1"; make_project "$PROJ1"
PROJ2="$TMP_ROOT/proj2"; make_project "$PROJ2"

# === A. flag OFF => byte-identical tab-per-task (control) =====================
spawn "$OFF_HOME" cmoff "$PROJ1" "sh -c 'echo off-ok'"
OFF_META="$OFF_HOME/state/cmoff.meta"
OFF_PANE=$(meta_get "$OFF_META" herdr_pane_id)
assert_not_contains_local "$(cat "$OFF_META")" "herdr_ws_owned" "flag-off meta must NOT carry herdr_ws_owned (byte-identical guarantee)"
assert_not_contains_local "$(cat "$OFF_META")" "herdr_parent_ws" "flag-off meta must NOT carry herdr_parent_ws"
[ "$(ws_label "$(pane_ws "$OFF_PANE")")" = "firstmate" ] || fail "flag-off crewmate must land as a TAB in the home 'firstmate' workspace, got '$(ws_label "$(pane_ws "$OFF_PANE")")'"
pass "flag OFF: crewmate lands as a tab in the home workspace, no owned-workspace meta (current behavior preserved)"

# === B. flag ON => the job gets its OWN child workspace ======================
spawn "$PRIMARY_ON" cma "$PROJ1" "sh -c 'echo cma-ok'"
A_META="$PRIMARY_ON/state/cma.meta"
A_PANE=$(meta_get "$A_META" herdr_pane_id)
A_CHILD_WS=$(meta_get "$A_META" herdr_workspace_id)
A_PARENT_WS=$(meta_get "$A_META" herdr_parent_ws)
[ "$(meta_get "$A_META" herdr_ws_owned)" = 1 ] || fail "flag-on job meta must record herdr_ws_owned=1"
[ -n "$A_PARENT_WS" ] || fail "flag-on job meta must record herdr_parent_ws"
[ "$A_CHILD_WS" != "$A_PARENT_WS" ] || fail "child workspace must differ from parent workspace"
[ "$(pane_ws "$A_PANE")" = "$A_CHILD_WS" ] || fail "runtime pane must live in the child workspace ($A_CHILD_WS), got '$(pane_ws "$A_PANE")'"
[ "$(ws_label "$A_CHILD_WS")" = "firstmate/cma" ] || fail "child workspace label should be 'firstmate/cma', got '$(ws_label "$A_CHILD_WS")'"
[ "$(ws_label "$A_PARENT_WS")" = "firstmate" ] || fail "recorded parent should be the home 'firstmate' workspace, got '$(ws_label "$A_PARENT_WS")'"
ws_exists "$A_PARENT_WS" || fail "the parent/home workspace must persist as the supervisor anchor"
A_TABS=$(ws_tab_labels "$A_CHILD_WS")
assert_contains_local "$A_TABS" "fm-cma" "child workspace must contain the runtime tab fm-cma"
assert_contains_local "$A_TABS" "log" "child workspace must contain the log tab"
sleep 1
assert_contains_local "$(fm_backend_herdr_capture "$SESSION:$A_PANE" 30)" "cma-ok" "runtime pane did not run the launch command"
pass "flag ON: delegated job gets its own child workspace 'firstmate/cma' with runtime+log tabs; parent recorded in meta and preserved"

# === C. multiple concurrent jobs => distinct child workspaces, one parent ====
spawn "$PRIMARY_ON" cmb "$PROJ2" "sh -c 'echo cmb-ok'"
B_META="$PRIMARY_ON/state/cmb.meta"
B_CHILD_WS=$(meta_get "$B_META" herdr_workspace_id)
B_PARENT_WS=$(meta_get "$B_META" herdr_parent_ws)
[ "$B_CHILD_WS" != "$A_CHILD_WS" ] || fail "concurrent jobs must get distinct child workspaces"
[ "$B_PARENT_WS" = "$A_PARENT_WS" ] || fail "concurrent jobs under the same home must share the same parent workspace"
[ "$(ws_label "$B_CHILD_WS")" = "firstmate/cmb" ] || fail "second child workspace label should be 'firstmate/cmb'"
pass "flag ON: a second concurrent job gets its own distinct child workspace under the same parent"

# === D. nested supervisor home: supervisor stays a tab; its job gets a child ==
spawn "$PRIMARY_ON" childe2 "$SM_HOME" "sh -c 'echo sm-ok'" --secondmate
SM_META="$PRIMARY_ON/state/childe2.meta"
SM_PANE=$(meta_get "$SM_META" herdr_pane_id)
SM_WS=$(pane_ws "$SM_PANE")
assert_not_contains_local "$(cat "$SM_META")" "herdr_ws_owned" "a --secondmate supervisor must NOT get a child workspace (stays a tab)"
[ "$(ws_label "$SM_WS")" = "2ndmate-childe2" ] || fail "secondmate should land as a tab in '2ndmate-childe2', got '$(ws_label "$SM_WS")'"
pass "nested homes: the supervisor itself stays a tab in its own '2ndmate-childe2' workspace"

spawn "$SM_HOME" cmc "$PROJ1" "sh -c 'echo cmc-ok'"
C_META="$SM_HOME/state/cmc.meta"
C_CHILD_WS=$(meta_get "$C_META" herdr_workspace_id)
C_PARENT_WS=$(meta_get "$C_META" herdr_parent_ws)
[ "$(meta_get "$C_META" herdr_ws_owned)" = 1 ] || fail "a job delegated from the secondmate home must get an owned child workspace"
[ "$(ws_label "$C_CHILD_WS")" = "2ndmate-childe2/cmc" ] || fail "child workspace should be '2ndmate-childe2/cmc', got '$(ws_label "$C_CHILD_WS")'"
[ "$C_PARENT_WS" = "$SM_WS" ] || fail "the delegated job's parent must be the secondmate's OWN workspace ($SM_WS), got '$C_PARENT_WS'"
pass "nested homes: a job delegated by the secondmate gets a child workspace under the secondmate's own workspace"

# === E. restart/recovery: list_live rediscovers child-workspace jobs ==========
PRIMARY_LIVE=$(FM_HOME="$PRIMARY_ON" fm_backend_herdr_list_live "$SESSION")
assert_contains_local "$PRIMARY_LIVE" "fm-cma" "recovery: primary list_live must rediscover child-workspace job cma"
assert_contains_local "$PRIMARY_LIVE" "fm-cmb" "recovery: primary list_live must rediscover child-workspace job cmb"
assert_not_contains_local "$PRIMARY_LIVE" "fm-cmc" "recovery: primary must not see the secondmate's job"
# the log tab (label "log", not fm-prefixed) is never a task endpoint
assert_not_contains_local "$PRIMARY_LIVE" $'\tlog' "recovery: list_live must not surface the log tab as a task endpoint"
SM_LIVE=$(FM_HOME="$SM_HOME" fm_backend_herdr_list_live "$SESSION")
assert_contains_local "$SM_LIVE" "fm-childe2" "recovery: secondmate list_live must see its own supervisor tab"
assert_contains_local "$SM_LIVE" "fm-cmc" "recovery: secondmate list_live must rediscover its child-workspace job cmc"
assert_not_contains_local "$SM_LIVE" "fm-cma" "recovery: secondmate must not see the primary's jobs"
pass "recovery: list_live rediscovers child-workspace jobs by label, scoped per home, without surfacing log tabs"

# === F. stale metadata + refuse-to-close-parent safety (function level) =======
if FM_HOME="$PRIMARY_ON" fm_backend_herdr_close_owned_workspace "$SESSION" "$A_PARENT_WS" "$A_PARENT_WS" 2>/dev/null; then
  fail "close_owned_workspace must REFUSE when child == parent"
fi
ws_exists "$A_PARENT_WS" || fail "refused close must not have touched the parent workspace"
FM_HOME="$PRIMARY_ON" fm_backend_herdr_close_owned_workspace "$SESSION" "w999999" "$A_PARENT_WS" || fail "close of an already-gone workspace id must be a safe no-op (returns 0)"
pass "safety: close_owned_workspace refuses child==parent and no-ops on stale/already-gone ids"

# === G. EXACT cleanup: teardown closes ONLY the owned child workspace =========
teardown "$PRIMARY_ON" cma
[ -f "$A_META" ] && fail "teardown must remove cma's meta"
ws_exists "$A_CHILD_WS" && fail "teardown must close cma's OWN child workspace"
ws_exists "$B_CHILD_WS" || fail "tearing down cma must NOT close sibling cmb's child workspace"
ws_exists "$A_PARENT_WS" || fail "tearing down cma must NOT close the parent/home workspace"
ws_exists "$C_CHILD_WS" || fail "tearing down cma must NOT close the secondmate's job workspace"
ws_exists "$SM_WS" || fail "tearing down cma must NOT close the secondmate's own workspace"
pass "EXACT cleanup: teardown removed only cma's own child workspace; parent, sibling, and the secondmate's spaces all survived"

# tidy remaining jobs
teardown "$PRIMARY_ON" cmb
teardown "$SM_HOME" cmc
teardown "$OFF_HOME" cmoff
ws_exists "$B_CHILD_WS" && fail "teardown must close cmb's child workspace"
ws_exists "$C_CHILD_WS" && fail "teardown must close cmc's child workspace"
pass "EXACT cleanup: every owned child workspace is removed by its own teardown"

# === H. default session untouched throughout =================================
DEFAULT_AFTER=$(default_fingerprint)
[ "$DEFAULT_AFTER" = "$DEFAULT_BEFORE" ] || fail "the default session changed! before='$DEFAULT_BEFORE' after='$DEFAULT_AFTER'"
pass "the captain's default herdr session is byte-identical (running-state + workspace count) before and after"

echo "# all child-workspace prototype E2E assertions passed"
