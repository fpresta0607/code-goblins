#!/usr/bin/env bash
set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=bin/fm-treehouse-lib.sh
. "$SCRIPT_DIR/fm-treehouse-lib.sh"

seed_count() {
  local key=$1
  printf '%s\n' "$journal_seed" | grep -c "^${key}=" 2>/dev/null || true
}

seed_value() {
  local key=$1
  printf '%s\n' "$journal_seed" | grep "^${key}=" | cut -d= -f2-
}

validate_seed() {
  local expected_holder=$1 expected_project=$2 expected_state=$3 recovery=${4:-0} owner state
  [ "$(seed_count spawn_recovery_owner)" -eq 1 ] || return 1
  [ "$(seed_count treehouse_lease_holder)" -eq 1 ] || return 1
  [ "$(seed_count project)" -eq 1 ] || return 1
  [ "$(seed_count worktree)" -eq 1 ] || return 1
  [ "$(seed_count treehouse_lease_identity)" -eq 1 ] || return 1
  [ "$(seed_count treehouse_state)" -eq 1 ] || return 1
  [ "$(seed_count spawn_recovery_state)" -eq 1 ] || return 1
  [ "$(seed_count herdr_ws_owned)" -eq 1 ] || return 1
  [ "$(seed_count herdr_pane_id)" -eq 1 ] || return 1
  owner="$(cd "$(dirname "$meta")" && pwd -P)/$(basename "${meta%.meta}")"
  [ "$(seed_value spawn_recovery_owner)" = "$owner" ] || return 1
  [ "$(seed_value treehouse_lease_holder)" = "$expected_holder" ] || return 1
  [ "$(seed_value project)" = "$expected_project" ] || return 1
  [ "$(seed_value treehouse_state)" = "$expected_state" ] || return 1
  [ "$(seed_value herdr_ws_owned)" = 1 ] || return 1
  [ -n "$(seed_value herdr_pane_id)" ] || return 1
  state=$(seed_value spawn_recovery_state)
  if [ "$recovery" = 1 ]; then
    case "$state" in lease-acquiring|lease-acquired-unverified|lease-acquired) ;; *) return 1 ;; esac
  else
    [ "$state" = lease-acquiring ] || return 1
    [ -z "$(seed_value worktree)" ] || return 1
    [ -z "$(seed_value treehouse_lease_identity)" ] || return 1
  fi
}

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

publish_recovery() {
  rewrite_evidence lease-acquired "$identity" || return 1
  fm_treehouse_write_owned_binding "$meta" "$worktree" "$identity" || return 1
  mv "$evidence" "$meta" || return 1
  if [ "${FM_TEST_INTERRUPT_AFTER_TREEHOUSE_META_PUBLISH:-0}" = 1 ]; then
    kill -KILL "$$"
  fi
  rm -f "$template" || true
  printf '%s\n' "$worktree" || true
  return 0
}

if [ "${1:-}" = --recover ]; then
  [ "$#" -eq 3 ] || exit 2
  evidence=$2
  meta=$3
  template=${evidence%.evidence}
  [ "$template" != "$evidence" ] || exit 1
  [ -f "$evidence" ] && [ ! -L "$evidence" ] || exit 1
  case "$meta" in *.meta) ;; *) exit 1 ;; esac
  [ "$(cd "$(dirname "$evidence")" && pwd -P)" = "$(cd "$(dirname "$meta")" && pwd -P)" ] || exit 1
  journal_seed=$(cat "$evidence") || exit 1
  holder=$(seed_value treehouse_lease_holder)
  project=$(seed_value project)
  state_path=$(seed_value treehouse_state)
  expected_state=$(fm_treehouse_state_path_for_project "$project") || exit 1
  validate_seed "$holder" "$project" "$expected_state" 1 || exit 1
  lease_record=$(fm_treehouse_lease_by_holder "$state_path" "$holder") || exit 1
  worktree=${lease_record%%$'\t'*}
  identity=${lease_record#*$'\t'}
  [ -n "$worktree" ] && [ "$worktree" != "$identity" ] || exit 1
  recorded_worktree=$(seed_value worktree)
  recorded_identity=$(seed_value treehouse_lease_identity)
  [ -z "$recorded_worktree" ] || [ "$recorded_worktree" = "$worktree" ] || exit 1
  [ -z "$recorded_identity" ] || [ "$recorded_identity" = "$identity" ] || exit 1
  publish_recovery || exit 1
  exit 0
fi

[ "$#" -eq 4 ] || exit 2
template=$1
meta=$2
holder=$3
project=$4
[ -f "$template" ] && [ ! -L "$template" ] || exit 1
case "$meta" in *.meta) ;; *) exit 1 ;; esac
[ "$(cd "$(dirname "$template")" && pwd -P)" = "$(cd "$(dirname "$meta")" && pwd -P)" ] || exit 1
project=$(cd "$project" 2>/dev/null && pwd -P) || exit 1
state_path=$(fm_treehouse_state_path_for_project "$project") || exit 1
evidence="$template.evidence"
[ ! -e "$evidence" ] && [ ! -L "$evidence" ] || exit 1
tmp="$(dirname "$evidence")/.$(basename "$evidence").prepare.$$"
umask 077
if ! awk -v state_path="$state_path" '
  /^treehouse_state=/ { next }
  { print }
  END { print "treehouse_state=" state_path }
' "$template" > "$tmp" || ! mv "$tmp" "$evidence"; then
  rm -f "$tmp"
  exit 1
fi
if ! journal_seed=$(cat "$evidence"); then
  rm -f "$evidence"
  exit 1
fi
if ! validate_seed "$holder" "$project" "$state_path"; then
  rm -f "$evidence"
  exit 1
fi

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
if [ "${FM_TEST_INTERRUPT_AFTER_TREEHOUSE_GET:-0}" = 1 ]; then
  kill -KILL "$$"
fi
rewrite_evidence lease-acquired-unverified "" || transaction_failed "could not record the acquired Treehouse worktree"
identity=$(fm_treehouse_worktree_identity "$worktree" "$holder") || transaction_failed "could not resolve the authoritative Treehouse lease identity"
case "$identity" in lease:*) ;; *) transaction_failed "Treehouse did not report an exact durable lease" ;; esac
publish_recovery || transaction_failed "could not publish the complete Treehouse lease recovery journal" "$identity"
