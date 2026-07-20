#!/usr/bin/env bash

fm_treehouse_worktree_identity() {
  local worktree=$1 expected_holder=${2:-} expected_prefix=${3:-} pool state
  [ -n "$worktree" ] || return 1
  pool=$(dirname "$(dirname "$worktree")")
  state="$pool/treehouse-state.json"
  [ -f "$state" ] && [ ! -L "$state" ] || return 1
  jq -er --arg path "$worktree" --arg holder "$expected_holder" --arg prefix "$expected_prefix" '
    [.worktrees[]? | select(.path == $path)] as $matches
    | if ($matches | length) != 1 then error("worktree identity is not unique")
      else $matches[0]
      end
    | if (.destroying // false) then error("worktree is being destroyed")
      elif (.leased // false) then
        if ($holder != "" and (.lease_holder // "") != $holder) then error("lease holder mismatch")
        elif ($prefix != "" and (((.lease_holder // "") | startswith($prefix) | not) or (((.lease_holder // "") | ltrimstr($prefix)) | test("^[0-9a-f]{32}$") | not))) then error("lease holder prefix mismatch")
        elif (.leased_at // "") == "" then error("lease timestamp missing")
        else "lease:" + ([.leased_at, (.lease_holder // "")] | @json | @base64)
        end
      elif ($prefix != "") then error("matching lease is not live")
      elif ((.owner_pid // 0) > 0 and (.owner_started_at // 0) > 0) then
        "owner:" + ((.owner_pid | tostring) + ":" + (.owner_started_at | tostring))
      else "available"
      end
  ' "$state" 2>/dev/null
}

fm_treehouse_migrate_owned_meta() {
  local meta=$1 id=$2 worktree identity_count identity tmp
  [ -f "$meta" ] && [ ! -L "$meta" ] || return 1
  [ "$(grep -c '^worktree=' "$meta" 2>/dev/null || true)" -eq 1 ] || return 1
  identity_count=$(grep -c '^treehouse_lease_identity=' "$meta" 2>/dev/null || true)
  [ "$identity_count" -le 1 ] || return 1
  if [ "$identity_count" -eq 1 ]; then
    identity=$(grep '^treehouse_lease_identity=' "$meta" | cut -d= -f2-)
    case "$identity" in lease:*) printf '%s\n' "$identity"; return 0 ;; *) return 1 ;; esac
  fi
  worktree=$(grep '^worktree=' "$meta" | cut -d= -f2-)
  identity=$(fm_treehouse_worktree_identity "$worktree" "" "firstmate-$id-") || return 1
  tmp="$(dirname "$meta")/.$(basename "$meta").treehouse-migrate.$$"
  umask 077
  if ! awk -v identity="$identity" '{ print } END { print "treehouse_lease_identity=" identity }' "$meta" > "$tmp" || ! mv "$tmp" "$meta"; then
    rm -f "$tmp"
    return 1
  fi
  printf '%s\n' "$identity"
}
