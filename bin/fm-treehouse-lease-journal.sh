#!/usr/bin/env bash
set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=bin/fm-treehouse-lib.sh
. "$SCRIPT_DIR/fm-treehouse-lib.sh"

[ "$#" -eq 3 ] || exit 2
template=$1
meta=$2
holder=$3
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

evidence="$template.evidence"
[ ! -e "$evidence" ] && [ ! -L "$evidence" ] || exit 1
journal_seed=$(cat "$template") || exit 1
tmp="$(dirname "$evidence")/.$(basename "$evidence").prepare.$$"
umask 077
if ! printf '%s\n' "$journal_seed" > "$tmp" || ! mv "$tmp" "$evidence"; then
  rm -f "$tmp"
  exit 1
fi

rewrite_evidence() {
  local state=$1 lease_identity=$2 write_tmp
  write_tmp="$(dirname "$evidence")/.$(basename "$evidence").write.$$"
  if ! printf '%s\n' "$journal_seed" | awk -v worktree="$worktree" -v identity="$lease_identity" -v state="$state" '
    /^worktree=/ { print "worktree=" worktree; next }
    /^treehouse_lease_identity=/ { print "treehouse_lease_identity=" identity; next }
    /^spawn_recovery_state=/ { print "spawn_recovery_state=" state; next }
    { print }
  ' > "$write_tmp" || ! mv "$write_tmp" "$evidence"; then
    rm -f "$write_tmp"
    return 1
  fi
}

rollback_exact_lease() {
  local expected_identity=$1 current_identity post_identity
  [ -n "$expected_identity" ] || return 1
  treehouse return --help 2>&1 | grep -q -- '--lease-holder' || return 1
  current_identity=$(fm_treehouse_worktree_identity "$worktree" "$holder") || return 1
  [ "$current_identity" = "$expected_identity" ] || return 1
  treehouse return --force --lease-holder "$holder" "$worktree" || return 1
  post_identity=$(fm_treehouse_worktree_identity "$worktree") || return 1
  [ "$post_identity" != "$expected_identity" ] || return 1
}

transaction_failed() {
  local reason=$1 expected_identity=${2:-}
  if [ -z "$expected_identity" ]; then
    expected_identity=$(fm_treehouse_worktree_identity "$worktree" "$holder" 2>/dev/null || true)
  fi
  if rollback_exact_lease "$expected_identity"; then
    rm -f "$template" "$evidence" "$(fm_treehouse_owned_binding_path "$meta")"
    echo "error: $reason; returned the exact Treehouse lease for $holder" >&2
  else
    echo "error: $reason; retained Treehouse lease recovery evidence at $evidence" >&2
  fi
  exit 1
}

worktree=$(treehouse get --lease --lease-holder "$holder") || exit 1
[ -n "$worktree" ] || transaction_failed "Treehouse returned an empty leased worktree path"
case "$worktree" in *$'\n'*) transaction_failed "Treehouse returned multiple leased worktree paths" ;; esac
rewrite_evidence lease-acquired-unverified "" || transaction_failed "could not record the acquired Treehouse worktree"
identity=$(fm_treehouse_worktree_identity "$worktree" "$holder") || transaction_failed "could not resolve the authoritative Treehouse lease identity"
case "$identity" in lease:*) ;; *) transaction_failed "Treehouse did not report an exact durable lease" ;; esac
rewrite_evidence lease-acquired "$identity" || transaction_failed "could not complete the Treehouse lease recovery journal" "$identity"
fm_treehouse_write_owned_binding "$meta" "$worktree" "$identity" || transaction_failed "could not publish the exact Treehouse lease binding" "$identity"
if ! mv "$evidence" "$meta"; then
  transaction_failed "could not publish the complete Treehouse lease recovery journal" "$identity"
fi
rm -f "$template" || true
printf '%s\n' "$worktree"
