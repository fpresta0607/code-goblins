#!/usr/bin/env bash
# fm-herdr-regroup.sh - explicit Herdr workspace contiguity regroup.
#
# Re-runs the workspace contiguity reconciler
# (fm_backend_herdr_contiguity_reconcile, bin/backends/herdr.sh;
# docs/herdr-backend.md "Workspace contiguity") on demand, outside the
# automatic spawn/teardown/recovery lifecycle points - the manual repair
# entry point after e.g. a hand-shuffled spaces sidebar.
#
# Scope: this home's own state dir, plus (with --all) the state dir of every
# secondmate home recorded in this home's kind=secondmate task metas. Each
# scope reconciles only ITS OWN herdr_ws_owned=1 crew workspaces under their
# recorded herdr_parent_ws anchors; supervisor anchors and workspaces
# firstmate did not create are never moved. Per-scope reconciles compose into
# the canonical depth-first order (supervisor, then its direct crews, then
# each child secondmate's subtree).
#
# Usage:
#   fm-herdr-regroup.sh [--all] [--session <name>]
#
#   --all              also regroup every live secondmate home recorded in
#                      this home's kind=secondmate task metas (home= field)
#   --session <name>   only regroup workspaces in this herdr session
#                      (default: every session named by an owned task meta)
#
# Read-mostly: the only mutation is non-destructive workspace.move reordering
# of exactly firstmate-owned workspace ids. Fail-closed per scope: a scope
# that cannot be reconciled is reported and left untouched; the script exits
# non-zero if any scope failed and 0 otherwise (including nothing-to-do).
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FM_ROOT="${FM_ROOT_OVERRIDE:-$(cd "$SCRIPT_DIR/.." && pwd)}"
FM_HOME="${FM_HOME:-${FM_ROOT_OVERRIDE:-$FM_ROOT}}"
STATE="${FM_STATE_OVERRIDE:-$FM_HOME/state}"

ALL=0
SESSION_FILTER=
while [ $# -gt 0 ]; do
  case "$1" in
    --all) ALL=1; shift ;;
    --session)
      SESSION_FILTER=${2:-}
      [ -n "$SESSION_FILTER" ] || { echo "error: --session requires a name" >&2; exit 2; }
      shift 2
      ;;
    -h|--help)
      sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "error: unknown argument '$1' (see --help)" >&2; exit 2 ;;
  esac
done

# shellcheck source=bin/fm-backend.sh
. "$FM_ROOT/bin/fm-backend.sh"
fm_backend_source herdr || { echo "error: could not load the herdr backend adapter" >&2; exit 1; }

# owned_sessions <state_dir>: the distinct herdr_session values among that
# state dir's herdr_ws_owned=1 task metas (the only scopes with anything to
# regroup), optionally narrowed by --session.
owned_sessions() {  # <state_dir>
  local state=$1 meta owned msession
  for meta in "$state"/*.meta; do
    [ -f "$meta" ] || continue
    owned=$(grep '^herdr_ws_owned=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-)
    [ "$owned" = 1 ] || continue
    msession=$(grep '^herdr_session=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-)
    [ -n "$msession" ] || continue
    [ -z "$SESSION_FILTER" ] || [ "$msession" = "$SESSION_FILTER" ] || continue
    printf '%s\n' "$msession"
  done | sort -u
}

FAILED=0
SCOPES=0
regroup_scope() {  # <label> <state_dir>
  local scope_label=$1 state=$2 session out
  while IFS= read -r session; do
    [ -n "$session" ] || continue
    SCOPES=$((SCOPES + 1))
    if out=$(fm_backend_herdr_contiguity_reconcile "$session" "$state" 2>&1); then
      echo "regrouped: $scope_label (session '$session')${out:+ - ${out##*$'\n'}}"
    else
      FAILED=1
      echo "FAILED: $scope_label (session '$session') - workspace order left untouched" >&2
      [ -n "$out" ] && printf '%s\n' "$out" >&2
    fi
  done < <(owned_sessions "$state")
}

regroup_scope "this home ($FM_HOME)" "$STATE"

if [ "$ALL" = 1 ]; then
  while IFS= read -r sm_home; do
    [ -n "$sm_home" ] || continue
    [ -d "$sm_home/state" ] || { echo "note: skipping secondmate home '$sm_home' (no state dir)" >&2; continue; }
    regroup_scope "secondmate home $sm_home" "$sm_home/state"
  done < <(
    for meta in "$STATE"/*.meta; do
      [ -f "$meta" ] || continue
      kind=$(grep '^kind=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-)
      [ "$kind" = secondmate ] || continue
      grep '^home=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-
    done | sort -u
  )
fi

if [ "$SCOPES" = 0 ]; then
  msg="nothing to regroup: no owned herdr child workspaces recorded"
  [ -n "$SESSION_FILTER" ] && msg="$msg for session '$SESSION_FILTER'"
  echo "$msg"
fi
exit "$FAILED"
