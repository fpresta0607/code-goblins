#!/usr/bin/env bash
set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=bin/fm-treehouse-lib.sh
. "$SCRIPT_DIR/fm-treehouse-lib.sh"

[ "$#" -eq 4 ] || exit 2
template=$1
meta=$2
worktree=$3
holder=$4
[ -f "$template" ] && [ ! -L "$template" ] || exit 1
case "$meta" in *.meta) ;; *) exit 1 ;; esac
[ "$(cd "$(dirname "$template")" && pwd -P)" = "$(cd "$(dirname "$meta")" && pwd -P)" ] || exit 1
owner="$(cd "$(dirname "$meta")" && pwd -P)/$(basename "${meta%.meta}")"
[ "$(grep -c '^spawn_recovery_state=lease-acquiring$' "$template" 2>/dev/null || true)" -eq 1 ] || exit 1
[ "$(grep -c '^spawn_recovery_owner=' "$template" 2>/dev/null || true)" -eq 1 ] || exit 1
[ "$(grep '^spawn_recovery_owner=' "$template" | cut -d= -f2-)" = "$owner" ] || exit 1
[ "$(grep -c '^treehouse_lease_holder=' "$template" 2>/dev/null || true)" -eq 1 ] || exit 1
[ "$(grep '^treehouse_lease_holder=' "$template" | cut -d= -f2-)" = "$holder" ] || exit 1
[ "$(grep -c '^worktree=$' "$template" 2>/dev/null || true)" -eq 1 ] || exit 1
[ "$(grep -c '^treehouse_lease_identity=$' "$template" 2>/dev/null || true)" -eq 1 ] || exit 1
[ "$(grep -c '^herdr_ws_owned=1$' "$template" 2>/dev/null || true)" -eq 1 ] || exit 1
[ -n "$(grep '^herdr_pane_id=' "$template" | cut -d= -f2-)" ] || exit 1

identity=$(fm_treehouse_worktree_identity "$worktree" "$holder") || exit 1
case "$identity" in lease:*) ;; *) exit 1 ;; esac
tmp="$(dirname "$meta")/.$(basename "$meta").acquire.$$"
umask 077
if ! awk -v worktree="$worktree" -v identity="$identity" '
  /^worktree=/ { print "worktree=" worktree; next }
  /^treehouse_lease_identity=/ { print "treehouse_lease_identity=" identity; next }
  /^spawn_recovery_state=/ { print "spawn_recovery_state=lease-acquired"; next }
  { print }
' "$template" > "$tmp" || ! mv "$tmp" "$meta"; then
  rm -f "$tmp"
  exit 1
fi
fm_treehouse_write_owned_binding "$meta" "$worktree" "$identity" || exit 1
rm -f "$template"
