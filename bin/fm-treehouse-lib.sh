#!/usr/bin/env bash

fm_treehouse_config_root() {
  local project=$1 config='' count value matched=0
  if [ -f "$project/treehouse.toml" ] && [ ! -L "$project/treehouse.toml" ]; then
    config="$project/treehouse.toml"
  elif [ -n "${HOME:-}" ] && [ -f "$HOME/.config/treehouse/config.toml" ] \
    && [ ! -L "$HOME/.config/treehouse/config.toml" ]; then
    config="$HOME/.config/treehouse/config.toml"
  fi
  [ -n "$config" ] || { printf '\n'; return 0; }
  count=$(grep -c '^[[:space:]]*root[[:space:]]*=' "$config" 2>/dev/null || true)
  [ "$count" -le 1 ] || return 1
  [ "$count" -eq 1 ] || { printf '\n'; return 0; }
  if grep -Eq '^[[:space:]]*root[[:space:]]*=[[:space:]]*"[^"\\]*"[[:space:]]*(#.*)?$' "$config"; then
    value=$(sed -n 's/^[[:space:]]*root[[:space:]]*=[[:space:]]*"\([^"\\]*\)"[[:space:]]*\(#.*\)\{0,1\}$/\1/p' "$config")
    matched=1
  elif grep -Eq "^[[:space:]]*root[[:space:]]*=[[:space:]]*'[^']*'[[:space:]]*(#.*)?$" "$config"; then
    value=$(sed -n "s/^[[:space:]]*root[[:space:]]*=[[:space:]]*'\([^']*\)'[[:space:]]*\(#.*\)\{0,1\}$/\1/p" "$config")
    matched=1
  fi
  [ "$matched" = 1 ] || return 1
  value=$(awk -v input="$value" '
    BEGIN {
      output = ""
      while (length(input) > 0) {
        marker = index(input, "$")
        if (marker == 0) {
          output = output input
          break
        }
        output = output substr(input, 1, marker - 1)
        input = substr(input, marker + 1)
        if (substr(input, 1, 1) == "{") {
          closing = index(input, "}")
          if (closing == 0) exit 1
          name = substr(input, 2, closing - 2)
          input = substr(input, closing + 1)
        } else {
          match(input, /^[A-Za-z_][A-Za-z0-9_]*/)
          if (RLENGTH == 0) exit 1
          name = substr(input, 1, RLENGTH)
          input = substr(input, RLENGTH + 1)
        }
        if (name !~ /^[A-Za-z_][A-Za-z0-9_]*$/) exit 1
        output = output ENVIRON[name]
      }
      print output
    }
  ') || return 1
  printf '%s\n' "$value"
}

fm_treehouse_state_path_for_project() {
  local project=$1 root hash_input hash repo_name
  project=$(cd "$project" 2>/dev/null && pwd -P) || return 1
  root=$(fm_treehouse_config_root "$project") || return 1
  if [ -z "$root" ]; then
    [ -n "${HOME:-}" ] || return 1
    root="$HOME/.treehouse"
  elif [ "${root#/}" = "$root" ]; then
    root="$project/$root/.treehouse"
  else
    root="$root/.treehouse"
  fi
  hash_input=$(git -C "$project" remote get-url origin 2>/dev/null || printf '%s\n' "$project")
  if command -v shasum >/dev/null 2>&1; then
    hash=$(printf '%s' "$hash_input" | LC_ALL=C shasum -a 256 | awk '{print substr($1,1,6)}')
  elif command -v sha256sum >/dev/null 2>&1; then
    hash=$(printf '%s' "$hash_input" | LC_ALL=C sha256sum | awk '{print substr($1,1,6)}')
  else
    return 1
  fi
  [ -n "$hash" ] || return 1
  repo_name=$(basename "$project")
  printf '%s/%s-%s/treehouse-state.json\n' "$root" "$repo_name" "$hash"
}

fm_treehouse_lease_by_holder() {
  local state=$1 holder=$2
  [ -n "$holder" ] || return 1
  [ -f "$state" ] && [ ! -L "$state" ] || return 1
  jq -er --arg holder "$holder" '
    [.worktrees[]? | select((.leased // false) and (.lease_holder // "") == $holder)] as $matches
    | if ($matches | length) != 1 then error("lease holder is not unique")
      else $matches[0]
      end
    | if (.destroying // false) or (.path // "") == "" or (.leased_at // "") == ""
      then error("lease identity is incomplete")
      else [.path, ("lease:" + ([.leased_at, (.lease_holder // "")] | @json | @base64))] | @tsv
      end
  ' "$state" 2>/dev/null
}

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

fm_treehouse_owned_binding_path() {
  local meta=$1
  case "$meta" in *.meta) printf '%s.treehouse-lease\n' "${meta%.meta}" ;; *) return 1 ;; esac
}

fm_treehouse_write_owned_binding() {
  local meta=$1 worktree=$2 identity=$3 binding tmp
  [ -n "$worktree" ] || return 1
  case "$identity" in lease:*) ;; *) return 1 ;; esac
  case "$worktree$identity" in *$'\n'*) return 1 ;; esac
  binding=$(fm_treehouse_owned_binding_path "$meta") || return 1
  mkdir -p "$(dirname "$binding")" || return 1
  tmp="$(dirname "$binding")/.$(basename "$binding").write.$$"
  umask 077
  if ! printf 'worktree=%s\ntreehouse_lease_identity=%s\n' "$worktree" "$identity" > "$tmp" || ! mv "$tmp" "$binding"; then
    rm -f "$tmp"
    return 1
  fi
}

fm_treehouse_read_owned_binding() {
  local meta=$1 expected_worktree=$2 binding worktree identity
  binding=$(fm_treehouse_owned_binding_path "$meta") || return 1
  [ -f "$binding" ] && [ ! -L "$binding" ] || return 1
  [ "$(wc -l < "$binding" | tr -d ' ')" -eq 2 ] || return 1
  [ "$(grep -c '^worktree=' "$binding" 2>/dev/null || true)" -eq 1 ] || return 1
  [ "$(grep -c '^treehouse_lease_identity=' "$binding" 2>/dev/null || true)" -eq 1 ] || return 1
  worktree=$(grep '^worktree=' "$binding" | cut -d= -f2-)
  identity=$(grep '^treehouse_lease_identity=' "$binding" | cut -d= -f2-)
  [ "$worktree" = "$expected_worktree" ] || return 1
  case "$identity" in lease:*) printf '%s\n' "$identity" ;; *) return 1 ;; esac
}

fm_treehouse_migrate_owned_meta() {
  local meta=$1 worktree identity_count identity current tmp
  [ -f "$meta" ] && [ ! -L "$meta" ] || return 1
  [ "$(grep -c '^worktree=' "$meta" 2>/dev/null || true)" -eq 1 ] || return 1
  identity_count=$(grep -c '^treehouse_lease_identity=' "$meta" 2>/dev/null || true)
  [ "$identity_count" -le 1 ] || return 1
  if [ "$identity_count" -eq 1 ]; then
    identity=$(grep '^treehouse_lease_identity=' "$meta" | cut -d= -f2-)
    case "$identity" in lease:*) printf '%s\n' "$identity"; return 0 ;; *) return 1 ;; esac
  fi
  worktree=$(grep '^worktree=' "$meta" | cut -d= -f2-)
  identity=$(fm_treehouse_read_owned_binding "$meta" "$worktree") || return 1
  current=$(fm_treehouse_worktree_identity "$worktree") || return 1
  [ "$current" = "$identity" ] || return 1
  tmp="$(dirname "$meta")/.$(basename "$meta").treehouse-migrate.$$"
  umask 077
  if ! awk -v identity="$identity" '{ print } END { print "treehouse_lease_identity=" identity }' "$meta" > "$tmp" || ! mv "$tmp" "$meta"; then
    rm -f "$tmp"
    return 1
  fi
  printf '%s\n' "$identity"
}
