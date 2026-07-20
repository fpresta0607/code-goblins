#!/usr/bin/env bash

fm_treehouse_worktree_identity() {
  local worktree=$1 expected_holder=${2:-} pool state
  [ -n "$worktree" ] || return 1
  pool=$(dirname "$(dirname "$worktree")")
  state="$pool/treehouse-state.json"
  [ -f "$state" ] && [ ! -L "$state" ] || return 1
  jq -er --arg path "$worktree" --arg holder "$expected_holder" '
    [.worktrees[]? | select(.path == $path)] as $matches
    | if ($matches | length) != 1 then error("worktree identity is not unique")
      else $matches[0]
      end
    | if (.destroying // false) then error("worktree is being destroyed")
      elif (.leased // false) then
        if ($holder != "" and (.lease_holder // "") != $holder) then error("lease holder mismatch")
        elif (.leased_at // "") == "" then error("lease timestamp missing")
        else "lease:" + ([.leased_at, (.lease_holder // "")] | @json | @base64)
        end
      elif ((.owner_pid // 0) > 0 and (.owner_started_at // 0) > 0) then
        "owner:" + ((.owner_pid | tostring) + ":" + (.owner_started_at | tostring))
      else "available"
      end
  ' "$state" 2>/dev/null
}
