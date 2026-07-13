#!/usr/bin/env bash
# bin/backends/herdr.sh - the herdr session-provider adapter (EXPERIMENTAL).
#
# Design: data/fm-backend-design-d7/herdr-addendum.md ("Interface mapping",
# decisions D1-D6) and the empirical verification recorded in
# data/fm-backend-design-d7/herdr-verification-p2.md (real herdr v0.7.1,
# protocol 14, macOS aarch64), refined by docs/herdr-backend.md's
# "workspace-per-home" pass (AGENTS.md task herdr-sm-spaces-k4). Herdr is a
# session provider ONLY (D3): the worktree provider stays treehouse, exactly
# like tmux. Sourced only through bin/fm-backend.sh's fm_backend_source in
# normal operation; the unit tests source it directly, so the FM_HOME fallback
# below keeps that path sane without fm-backend.sh's preamble.
#
# Container shape (D4, decided empirically - see herdr-verification-p2.md
# "Task container shape", refined by docs/herdr-backend.md "Task container
# shape"): ONE herdr workspace PER FIRSTMATE HOME (the primary, and each
# secondmate, gets its own), ONE herdr TAB per task inside its home's
# workspace. Workspace-per-task was tried and rejected (bad human-watching
# ergonomics); workspace-per-HOME keeps that same rejection while giving every
# home its own space, labeled distinctly, in the shared spaces sidebar. Target
# resolution and the human-watch story stay parallel to the tmux adapter.
#
# Target string shape: "<herdr-session>:<pane-id>", e.g. "default:w1:p2" (the
# pane id itself contains a colon; the session is always the FIRST field, the
# remainder is the whole pane id - fm_backend_herdr_parse_target splits on the
# first colon only). This is the value stored in a herdr task's meta window=
# field and is what fm_backend_resolve_selector already returns unchanged for
# exact task-id, legacy fm-<id>, and explicit backend-target forms (that
# function has no herdr-specific logic; it just returns meta's window=
# verbatim).
#
# Recovery/orphan discovery (ids may not deterministically match live state
# after a server restart in a differently-configured session; see the
# verification doc) uses LABEL matching (fm-<id> tab labels), never trusts a
# stored pane id blindly: fm_backend_herdr_list_live.
#
# Requires: herdr (CLI + socket), jq (JSON parsing). Both are gated behind
# selecting this backend; bin/fm-bootstrap.sh's core tool list is unaffected.

# FM_HOME fallback: every real caller (fm-spawn.sh, fm-peek.sh, fm-send.sh,
# fm-teardown.sh, fm-watch.sh, fm-crew-state.sh) already sets FM_HOME as a
# global before sourcing fm-backend.sh (which sources this file), so this
# never overrides a real invocation. It exists only so this file's own unit
# tests, which source it directly without that preamble, resolve to a sane
# default (the firstmate repo root - never a secondmate home, so
# fm_backend_herdr_workspace_label falls through to "firstmate" exactly like
# pre-P3 behavior when a test does not care about home-specific labeling).
FM_BACKEND_HERDR_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FM_ROOT="${FM_ROOT_OVERRIDE:-${FM_ROOT:-$FM_BACKEND_HERDR_ROOT}}"
FM_HOME="${FM_HOME:-${FM_ROOT_OVERRIDE:-$FM_ROOT}}"

# Shared composer-content classifier (empty|pending|unknown, and the fleet-wide
# dead-shell-vs-agent-composer rule). Owned by bin/fm-composer-lib.sh, reused by
# every backend so the decision cannot drift.
# shellcheck source=bin/fm-composer-lib.sh
. "$FM_BACKEND_HERDR_ROOT/bin/fm-composer-lib.sh"

# Shared, backend-neutral normalized-transition shape and the single-owner
# status->action policy table (bin/fm-transition-lib.sh). This adapter's event
# subscriber (fm_backend_herdr_wait_transition) normalizes every
# pane.agent_status_changed edge through fm_transition_record and routes it
# through fm_transition_policy - it never re-encodes the mapping.
# shellcheck source=bin/fm-transition-lib.sh
. "$FM_BACKEND_HERDR_ROOT/bin/fm-transition-lib.sh"

FM_BACKEND_HERDR_MIN_PROTOCOL=14
# events.subscribe (the native pane.agent_status_changed push stream) and its
# subscription_event schema first shipped at protocol 16 (verified: herdr
# 0.7.3). Below this, or with the events surface absent from `herdr api schema`,
# the event fast-path fails closed to the watcher's poll loop
# (fm_backend_herdr_events_capable). Distinct from FM_BACKEND_HERDR_MIN_PROTOCOL
# (14): the adapter's spawn/capture/send primitives work on 14, only the push
# subscriber needs 16.
FM_BACKEND_HERDR_MIN_EVENTS_PROTOCOL=16
# Per-pane escalation dedupe marker prefix, under the state dir. One marker per
# window (keyed like the watcher's own .stale-<key>): set when a ->blocked edge
# is enqueued, cleared on any working edge, so exactly one wake fires per
# ->blocked edge and a reconnect level-reconcile never re-delivers a still-
# blocked pane. Mirrors bin/fm-watch.sh's .stale-<key> naming.
FM_BACKEND_HERDR_ESCALATED_PREFIX=".herdr-escalated-"
# .fm-secondmate-home is written by bin/fm-home-seed.sh (AGENTS.md section 6)
# at a seeded secondmate home's root, containing exactly that secondmate's id.
# The primary firstmate home never carries this marker.
FM_BACKEND_HERDR_SECONDMATE_MARKER=".fm-secondmate-home"

# fm_backend_herdr_workspace_label: the per-firstmate-HOME herdr workspace
# label (docs/herdr-backend.md "Task container shape"). The PRIMARY home (no
# secondmate marker) resolves to the constant "firstmate", byte-identical to
# every pre-existing task's recorded label - no forced migration. A SECONDMATE
# home resolves to "2ndmate-<secondmate-id>", so its tasks land in their own
# workspace, obviously distinguishable from the primary's (and from every
# other secondmate's) in herdr's spaces sidebar. Read fresh from FM_HOME on
# every call rather than cached at source time: FM_HOME is the home's own
# durable identity, not env plumbing threaded through a call chain, so the
# label is automatically stable across every respawn/recovery for the life of
# that home. fm-spawn.sh briefly shadows FM_HOME to a secondmate's own home
# when the PRIMARY spawns that secondmate (its own process's FM_HOME still
# names the primary at that point) - see fm-spawn.sh's herdr case arm.
fm_backend_herdr_workspace_label() {
  local marker="$FM_HOME/$FM_BACKEND_HERDR_SECONDMATE_MARKER" id
  if [ -f "$marker" ]; then
    id=$(tr -d '[:space:]' < "$marker" 2>/dev/null)
    if [ -n "$id" ]; then
      printf '2ndmate-%s' "$id"
      return 0
    fi
  fi
  printf 'firstmate'
}

# fm_backend_herdr_cli: run `herdr <args...>` scoped to <session>, setting
# BOTH the HERDR_SESSION env var AND appending a trailing `--session <name>`
# CLI flag. Verified empirically (docs/herdr-backend.md "Session targeting: the
# --session flag, not HERDR_SESSION alone"): on the installed herdr 0.7.1
# client, the HERDR_SESSION env var is NOT reliably honored by CLI subcommands
# once ANY other herdr server is already bound on the machine - queries
# silently fall back to whatever server IS running (the wrong one) instead of
# routing to the requested session or refusing. The `--session <name>` global
# flag (verified in both leading and trailing position; trailing used here to
# keep every call site a minimal, append-only diff) always routes correctly,
# including starting a genuinely separate, isolated server process. The env
# var is kept alongside it - harmless, self-documenting, and forward-
# compatible if a future herdr build honors it. Never used by
# fm_backend_herdr_version_check, which is intentionally session-independent
# (reads only .client.* fields).
fm_backend_herdr_cli() {  # <session> <herdr-subcommand-and-args...>
  local session=$1
  shift
  HERDR_SESSION="$session" herdr "$@" --session "$session"
}

# fm_backend_herdr_tool_check: refuse loudly if herdr or jq is missing.
fm_backend_herdr_tool_check() {
  command -v herdr >/dev/null 2>&1 || { echo "error: backend=herdr selected but the 'herdr' CLI is not installed (https://herdr.dev) (dual-licensed AGPL-3.0-or-later/commercial)" >&2; return 1; }
  command -v jq >/dev/null 2>&1 || { echo "error: backend=herdr selected but 'jq' is not installed (required to parse herdr's JSON output)" >&2; return 1; }
  return 0
}

# fm_backend_herdr_version_check: refuse loudly on a missing/incompatible
# herdr client. Verified locally: v0.7.1, protocol 14 (herdr status --json's
# .client.protocol; client info is session-independent, unlike .server).
fm_backend_herdr_version_check() {
  fm_backend_herdr_tool_check || return 1
  local status protocol version
  status=$(herdr status --json 2>/dev/null) || { echo "error: 'herdr status --json' failed; is herdr installed correctly?" >&2; return 1; }
  protocol=$(printf '%s' "$status" | jq -r '.client.protocol // empty' 2>/dev/null)
  version=$(printf '%s' "$status" | jq -r '.client.version // empty' 2>/dev/null)
  case "$protocol" in
    ''|*[!0-9]*)
      echo "error: could not read herdr client protocol from 'herdr status --json'; refusing to use an unverified herdr build" >&2
      return 1
      ;;
  esac
  if [ "$protocol" -lt "$FM_BACKEND_HERDR_MIN_PROTOCOL" ]; then
    echo "error: herdr protocol $protocol (version ${version:-unknown}) is older than the verified minimum $FM_BACKEND_HERDR_MIN_PROTOCOL; update herdr (herdr update) before using backend=herdr" >&2
    return 1
  fi
  return 0
}

# fm_backend_herdr_session: resolve which named herdr session this normal
# spawn/op uses. HERDR_SESSION mirrors tmux's $TMUX ambient-selection for
# adapter workspace/tab/pane operations: an operator (or firstmate's own
# isolated test harness) sets it explicitly; absent means herdr's own
# "default" session. Do not use HERDR_SESSION alone for destructive test
# cleanup; tests/herdr-test-safety.sh documents and guards that path.
fm_backend_herdr_session() {
  printf '%s' "${HERDR_SESSION:-default}"
}

# fm_backend_herdr_server_ensure: start the herdr server for <session>
# headless (no TUI client) if not already running, mirroring tmux's `tmux
# has-session || tmux new-session -d`. Verified: a bare socket CLI call does
# NOT auto-start the server, so this must run before any workspace/tab/pane
# call. Bounded poll for the server to report running.
fm_backend_herdr_server_ensure() {  # <session>
  local session=$1 running out i
  running=$(fm_backend_herdr_cli "$session" status --json 2>/dev/null | jq -r '.server.running // false' 2>/dev/null)
  [ "$running" = "true" ] && return 0
  ( fm_backend_herdr_cli "$session" server >/dev/null 2>&1 & ) || return 1
  for i in $(seq 1 20); do
    running=$(fm_backend_herdr_cli "$session" status --json 2>/dev/null | jq -r '.server.running // false' 2>/dev/null)
    [ "$running" = "true" ] && return 0
    sleep 0.5
  done
  echo "error: herdr server for session '$session' did not report running within 10s" >&2
  return 1
}

# fm_backend_herdr_workspace_find: this HOME's own workspace id inside
# <session> (fm_backend_herdr_workspace_label), or empty (never creates).
# Read-only, safe for recovery/list paths. Label-collision semantics
# (docs/herdr-backend.md "Label collisions"): herdr enforces no label
# uniqueness at all, so this adopts the FIRST matching workspace `jq` returns
# (list order, normally creation order/oldest) rather than disambiguating -
# identical in spirit to the pre-existing tab duplicate-label check below.
fm_backend_herdr_workspace_find() {  # <session>
  local session=$1 label list
  label=$(fm_backend_herdr_workspace_label)
  list=$(fm_backend_herdr_cli "$session" workspace list 2>/dev/null) || return 0
  # NOTE: the jq variable is $want, NOT $label - `label` is a jq reserved
  # keyword (label/break), so declaring a jq variable named "label" is a
  # compile error that `2>/dev/null` would silently swallow, making this find
  # ALWAYS return empty and every spawn mint a fresh "firstmate" workspace
  # (the workspace leak).
  printf '%s' "$list" | jq -r --arg want "$label" \
    '.result.workspaces[]? | select(.label == $want) | .workspace_id' 2>/dev/null | head -1
}

# fm_backend_herdr_workspace_prune_seeded_default_tab: close EXACTLY
# <seeded_tab_id>, the auto-created default tab id that THIS SAME
# fm_backend_herdr_workspace_ensure call captured straight from its own
# `workspace create` response (never re-derived from a label pattern at
# create_task time - see the incident note below). Best-effort: a failure
# here never fails the caller, mirroring the fm_backend_herdr_kill `|| true`
# contract.
#
# Live-fire incident fix (2026-07-02): the prior implementation
# (fm_backend_herdr_workspace_prune_default_tabs, removed) re-derived
# "prunable" at create_task time from a pure label heuristic - exactly one
# tab, labeled "1" - run against whatever workspace fm_backend_herdr_workspace_find
# had just resolved. Herdr enforces no label uniqueness (docs/herdr-backend.md
# "Label collisions") and derives an unlabeled workspace's DISPLAYED label from
# its pane cwd's basename, so a captain launching herdr directly inside a
# directory named "firstmate" produces a workspace that looks byte-identical,
# by label alone, to firstmate's own auto-created container - one tab, label
# "1". workspace_find adopted that pre-existing (captain-owned, LIVE) workspace
# by the label match, the heuristic matched too, and the very next spawn
# closed the captain's own live pane 27ms after creating its task tab. The
# fix is structural, not another heuristic: only a workspace THIS SAME
# fm_backend_herdr_workspace_ensure call just created carries a non-empty
# seeded_tab_id at all (see FM_BACKEND_HERDR_WS_SEEDED_TAB_ID below); an
# ADOPTED workspace's seeded_tab_id is always empty, so create_task never
# calls this function for one, regardless of how its tabs happen to be
# labeled.
#
# Defense in depth on top of that gate (not the primary safety mechanism):
# re-verify <seeded_tab_id> is still present, still carries label "1" (a
# human could have renamed or repurposed it in the interim), and refuse to
# close it if its pane hosts an actively working agent per herdr's own
# agent-state detection (`agent get`) - belt-and-suspenders against any other
# unforeseen path landing a live agent in a tab this function was about to
# close.
#
# Verified real-herdr behavior (not modeled by the canned-response fake-CLI
# unit tests; modeled by make_herdr_statefake): closing a workspace's LAST
# remaining tab deletes the whole workspace, not just the tab. So this must
# never run while the seeded default tab is still the ONLY tab in the
# workspace - callers only invoke it once at least one other (real task) tab
# exists alongside it, never right after workspace creation - and this
# function independently re-checks the tab count as a second layer.
fm_backend_herdr_workspace_prune_seeded_default_tab() {  # <session> <workspace_id> <seeded_tab_id>
  local session=$1 wsid=$2 tab_id=$3 tabs tab_count current_label pane_id agent_out agent_status
  [ -n "$tab_id" ] || return 0
  tabs=$(fm_backend_herdr_cli "$session" tab list --workspace "$wsid" 2>/dev/null) || return 0
  tab_count=$(printf '%s' "$tabs" | jq -r '.result.tabs? // [] | length' 2>/dev/null)
  case "$tab_count" in ''|*[!0-9]*|0|1) return 0 ;; esac
  current_label=$(printf '%s' "$tabs" | jq -r --arg t "$tab_id" '.result.tabs[]? | select(.tab_id == $t) | .label' 2>/dev/null)
  [ "$current_label" = "1" ] || return 0
  pane_id=$(fm_backend_herdr_pane_for_tab "$session" "$wsid" "$tab_id") || return 0
  [ -n "$pane_id" ] || return 0
  agent_out=$(fm_backend_herdr_cli "$session" agent get "$pane_id" 2>/dev/null)
  agent_status=$(printf '%s' "$agent_out" | jq -r '.result.agent.agent_status // empty' 2>/dev/null)
  [ "$agent_status" = working ] && return 0
  fm_backend_herdr_cli "$session" pane close "$pane_id" >/dev/null 2>&1 || true
}

# fm_backend_herdr_workspace_ensure: this HOME's persistent workspace inside
# <session>, creating it in <cwd> if absent. Must be called as a PLAIN
# STATEMENT, never through command substitution ($(...)) - it communicates
# through these globals, not solely through stdout, and a command
# substitution forks a subshell that would discard them:
#   FM_BACKEND_HERDR_WS_ID          - the resolved workspace_id (also echoed,
#                                      for callers that only need the id)
#   FM_BACKEND_HERDR_WS_SEEDED_TAB_ID - non-empty ONLY when THIS call just
#                                      CREATED the workspace: the tab_id of
#                                      the auto-created default tab herdr
#                                      seeded it with, read straight from the
#                                      `workspace create` response's
#                                      `.result.tab.tab_id` (verified
#                                      empirically against the real binary -
#                                      no follow-up tab-list call needed).
#                                      Empty whenever this call instead
#                                      ADOPTED a pre-existing workspace
#                                      (fm_backend_herdr_workspace_find
#                                      matched by label - docs/herdr-backend.md
#                                      "Label collisions": that match can
#                                      never distinguish an explicitly
#                                      `--label`-created workspace from one
#                                      whose label only coincidentally
#                                      matches this home's own, e.g. a
#                                      cwd-basename-derived label). An
#                                      ADOPTED workspace's tabs are NEVER
#                                      inspected or identified as prunable by
#                                      this function, no matter what they are
#                                      labeled - see
#                                      fm_backend_herdr_workspace_prune_seeded_default_tab.
# --no-focus (docs/herdr-backend.md "Focus behavior"): verified that workspace
# create does NOT focus by default once at least one workspace already exists
# in the session, matching pre-existing (flagless) behavior; the ONE exception
# is the very first workspace ever created in a brand-new session, which
# focuses regardless of --no-focus (herdr always needs something focused to
# attach to). --no-focus is passed unconditionally anyway, for defense in
# depth and because it is a no-op in the already-safe case.
fm_backend_herdr_workspace_ensure() {  # <session> <cwd>
  local session=$1 cwd=$2 wsid out label
  FM_BACKEND_HERDR_WS_ID=""
  FM_BACKEND_HERDR_WS_SEEDED_TAB_ID=""
  wsid=$(fm_backend_herdr_workspace_find "$session")
  if [ -n "$wsid" ]; then
    FM_BACKEND_HERDR_WS_ID=$wsid
    printf '%s' "$wsid"
    return 0
  fi
  label=$(fm_backend_herdr_workspace_label)
  out=$(fm_backend_herdr_cli "$session" workspace create --cwd "$cwd" --label "$label" --no-focus 2>/dev/null) || return 1
  wsid=$(printf '%s' "$out" | jq -r '.result.workspace.workspace_id // empty' 2>/dev/null)
  [ -n "$wsid" ] || return 1
  FM_BACKEND_HERDR_WS_ID=$wsid
  # Herdr seeds a new workspace with one auto-created default tab firstmate
  # never uses. It is NOT pruned here: at this instant it is the workspace's
  # ONLY tab, and closing a workspace's last tab deletes the workspace itself
  # (verified against the real herdr binary) - pruning here would destroy the
  # workspace we just created. fm_backend_herdr_create_task prunes it instead,
  # once the first real task tab exists alongside it, and only ever targets
  # this exact captured tab_id.
  FM_BACKEND_HERDR_WS_SEEDED_TAB_ID=$(printf '%s' "$out" | jq -r '.result.tab.tab_id // empty' 2>/dev/null)
  printf '%s' "$wsid"
}

# fm_backend_herdr_container_ensure: the full spawn-time container-ensure
# sequence (version gate, server, workspace). Echoes
# "<session>:<workspace_id>\t<seeded_default_tab_id>" - a single TAB character
# always separates the two fields (the second is empty for an ADOPTED
# workspace) so a caller can split unambiguously with
# CONTAINER=${RAW%%$'\t'*}; SEEDED_TAB_ID=${RAW#*$'\t'}. The seeded tab id
# must be threaded through to fm_backend_herdr_create_task, which is the only
# function allowed to prune it (fm_backend_herdr_workspace_prune_seeded_default_tab).
fm_backend_herdr_container_ensure() {  # <cwd-for-a-fresh-workspace>
  local cwd=${1:-$PWD} session label
  fm_backend_herdr_version_check || return 1
  session=$(fm_backend_herdr_session)
  fm_backend_herdr_server_ensure "$session" || return 1
  fm_backend_herdr_workspace_ensure "$session" "$cwd" >/dev/null || { label=$(fm_backend_herdr_workspace_label); echo "error: failed to ensure herdr workspace '$label' in session '$session'" >&2; return 1; }
  if [ -z "$FM_BACKEND_HERDR_WS_ID" ]; then
    label=$(fm_backend_herdr_workspace_label)
    echo "error: failed to ensure herdr workspace '$label' in session '$session'" >&2
    return 1
  fi
  printf '%s:%s\t%s' "$session" "$FM_BACKEND_HERDR_WS_ID" "$FM_BACKEND_HERDR_WS_SEEDED_TAB_ID"
}

# --- Child-workspace hierarchy (PROTOTYPE, default OFF) -----------------------
# docs/herdr-backend.md "Child-workspace hierarchy (prototype)". Local-trial
# only: gated entirely behind fm_backend_herdr_child_ws_enabled, so with the
# flag absent/off EVERY path below is skipped and behavior is byte-identical to
# the shipped tab-per-task shape. When ON, a DELEGATED job (a ship/scout
# crewmate, never a --secondmate supervisor) gets its OWN child workspace under
# the home/supervisor workspace instead of a sibling tab inside it, with the
# job's runtime and log views grouped as tabs inside that child workspace.
#
# Herdr 0.7.3 has NO first-class parent<->child workspace edge (verified: the
# api-schema WorkspaceInfo carries no parent field, `workspace create` has no
# --parent flag; workspaces are a flat set ordered by `number`). The DURABLE,
# machine-readable association therefore lives in firstmate's own task meta
# (herdr_parent_ws=, written by fm-spawn.sh), NOT in the label. The child label
# "<home-label>/<id>" is DISPLAY SUGAR ONLY; teardown targets the exact owned
# workspace_id (herdr_ws_owned=1 gate), never a label match - so the
# 2026-07-02 label-collision self-kill class of bug cannot recur through it.

# fm_backend_herdr_child_ws_enabled: true (0) only when the local, gitignored
# flag config/herdr-child-workspaces has "on" as its first non-empty line.
# Absent or any other value -> false (1) -> unchanged tab-per-task behavior.
FM_BACKEND_HERDR_CHILD_WS_FLAG="herdr-child-workspaces"
fm_backend_herdr_child_ws_enabled() {
  local cfg="${FM_CONFIG_OVERRIDE:-$FM_HOME/config}/$FM_BACKEND_HERDR_CHILD_WS_FLAG" val
  [ -f "$cfg" ] || return 1
  val=$(grep -v '^[[:space:]]*$' "$cfg" 2>/dev/null | head -1 | tr -d '[:space:]')
  [ "$val" = "on" ]
}

# fm_backend_herdr_child_label: the human-visible label for a job's own child
# workspace, "<home-label>/<id>". Display only (see the section note above).
fm_backend_herdr_child_label() {  # <id>
  printf '%s/%s' "$(fm_backend_herdr_workspace_label)" "$1"
}

# fm_backend_herdr_create_child_workspace: build a delegated job's OWN child
# workspace under <parent_ws_id> (the already-ensured home/supervisor
# workspace) and populate it with the tabs that GENUINELY belong to that one
# job and nothing else:
#   - runtime tab (label "fm-<id>"): the crewmate agent itself - THE job.
#   - log tab (label "log"): a read-only `tail -F` of the job's own
#     state/<id>.status wake-event log, the exact signal firstmate reads about
#     this job. Best-effort; a failure to add it never fails the spawn.
# No parent/sibling/fleet-wide view is ever placed inside the job workspace.
# Everything is created --no-focus so the captain's active space is preserved
# (docs "Focus behavior"). The seeded default tab is pruned only AFTER the real
# tabs exist alongside it, via the same structural (never label-heuristic)
# prune used for the home workspace. Echoes "<child_ws_id> <runtime_tab_id>
# <runtime_pane_id>".
fm_backend_herdr_create_child_workspace() {  # <session> <parent_ws_id> <id> <cwd> [<status_file>]
  local session=$1 parent=$2 id=$3 cwd=$4 status_file=${5:-} label out child_ws seed_tab rt_out rt_tab rt_pane lg_out lg_pane
  [ -n "$parent" ] || { echo "error: create_child_workspace requires a parent workspace id" >&2; return 1; }
  label=$(fm_backend_herdr_child_label "$id")
  out=$(fm_backend_herdr_cli "$session" workspace create --cwd "$cwd" --label "$label" --no-focus 2>/dev/null) || return 1
  child_ws=$(printf '%s' "$out" | jq -r '.result.workspace.workspace_id // empty' 2>/dev/null)
  seed_tab=$(printf '%s' "$out" | jq -r '.result.tab.tab_id // empty' 2>/dev/null)
  [ -n "$child_ws" ] || { echo "error: could not parse child workspace id from herdr workspace create output" >&2; return 1; }
  rt_out=$(fm_backend_herdr_cli "$session" tab create --workspace "$child_ws" --cwd "$cwd" --label "fm-$id" --no-focus 2>/dev/null) || return 1
  rt_tab=$(printf '%s' "$rt_out" | jq -r '.result.tab.tab_id // empty' 2>/dev/null)
  rt_pane=$(printf '%s' "$rt_out" | jq -r '.result.root_pane.pane_id // empty' 2>/dev/null)
  if [ -z "$rt_tab" ] || [ -z "$rt_pane" ]; then
    echo "error: could not parse runtime tab/pane id from herdr tab create output" >&2
    return 1
  fi
  if [ -n "$status_file" ]; then
    lg_out=$(fm_backend_herdr_cli "$session" tab create --workspace "$child_ws" --cwd "$cwd" --label "log" --no-focus 2>/dev/null)
    lg_pane=$(printf '%s' "$lg_out" | jq -r '.result.root_pane.pane_id // empty' 2>/dev/null)
    if [ -n "$lg_pane" ]; then
      fm_backend_herdr_cli "$session" pane run "$lg_pane" "tail -n +1 -F $status_file" >/dev/null 2>&1 || true
    fi
  fi
  [ -n "$seed_tab" ] && fm_backend_herdr_workspace_prune_seeded_default_tab "$session" "$child_ws" "$seed_tab"
  printf '%s %s %s' "$child_ws" "$rt_tab" "$rt_pane"
}

# fm_backend_herdr_close_owned_workspace: teardown for a child-workspace job -
# close EXACTLY <child_ws_id> and every tab it owns (runtime + log) in one
# operation. Fail-closed safety refuses unless the id is present, differs from
# <parent_ws_id>, and differs from this home's OWN (home-label) workspace, so a
# stale or malformed meta can never make teardown close a parent, a
# supervisor's home, or the default workspace. Best-effort like
# fm_backend_herdr_kill: an already-gone workspace is a safe no-op (verified:
# `workspace close` on a closed id returns workspace_not_found, non-fatal).
# The caller only ever reaches this path when meta records herdr_ws_owned=1,
# which fm-spawn.sh writes solely for a freshly-created, exclusively-owned
# child workspace.
fm_backend_herdr_close_owned_workspace() {  # <session> <child_ws_id> <parent_ws_id>
  local session=$1 child=$2 parent=$3 home_ws
  [ -n "$session" ] && [ -n "$child" ] || return 0
  if [ -n "$parent" ] && [ "$child" = "$parent" ]; then
    echo "error: refusing to close herdr workspace '$child': it equals the recorded parent workspace" >&2
    return 1
  fi
  home_ws=$(fm_backend_herdr_workspace_find "$session")
  if [ -n "$home_ws" ] && [ "$child" = "$home_ws" ]; then
    echo "error: refusing to close herdr workspace '$child': it is this home's own workspace" >&2
    return 1
  fi
  fm_backend_herdr_cli "$session" workspace close "$child" >/dev/null 2>&1 || true
}

# fm_backend_herdr_list_live_children: the child-workspace half of recovery
# orphan-discovery. Enumerates every CHILD workspace of this home (label prefix
# "<home-label>/") and emits its runtime tab (label "fm-<id>") in the same
# "<session>:<pane_id>\t<label>" shape as fm_backend_herdr_list_live. The "log"
# tab is deliberately skipped: it is not a firstmate task endpoint. Read-only;
# emits nothing when child-workspace mode was never used.
fm_backend_herdr_list_live_children() {  # <session>
  local session=$1 prefix ws_list child_ws tabs tab_id label pane_id
  prefix="$(fm_backend_herdr_workspace_label)/"
  ws_list=$(fm_backend_herdr_cli "$session" workspace list 2>/dev/null) || return 0
  while IFS= read -r child_ws; do
    [ -n "$child_ws" ] || continue
    tabs=$(fm_backend_herdr_cli "$session" tab list --workspace "$child_ws" 2>/dev/null) || continue
    while IFS=$'\t' read -r tab_id label; do
      [ -n "$tab_id" ] || continue
      pane_id=$(fm_backend_herdr_pane_for_tab "$session" "$child_ws" "$tab_id") || continue
      [ -n "$pane_id" ] || continue
      printf '%s:%s\t%s\n' "$session" "$pane_id" "$label"
    done < <(printf '%s' "$tabs" | jq -r '.result.tabs[]? | select(.label | startswith("fm-")) | "\(.tab_id)\t\(.label)"' 2>/dev/null)
  done < <(printf '%s' "$ws_list" | jq -r --arg p "$prefix" '.result.workspaces[]? | select(.label | startswith($p)) | .workspace_id' 2>/dev/null)
}

# --- Workspace contiguity (child-workspace prototype ordering) ----------------
# docs/herdr-backend.md "Workspace contiguity (depth-first supervisor order)".
# Herdr 0.7.3 orders workspaces as a flat array and its ONLY reorder primitive
# is the typed socket request workspace.move {workspace_id, insert_index}
# (data/herdr-workspace-reorder-audit-o5/report.md) - non-destructive, not in
# the shipped CLI, reached through bin/backends/herdr-move.py (the write-side
# sibling of herdr-eventwait.py's proven raw-socket transport).
#
# The invariant maintained here: every supervisor's OWNED crew child
# workspaces sit contiguously, immediately after that supervisor's own
# workspace, in the canonical stable DEPTH-FIRST order - supervisor, then ALL
# its direct crews (stable relative order), then each child secondmate
# supervisor followed by that secondmate's own subtree, recursively. Direct
# crews ALWAYS sort before child-secondmate subtrees; that is an intentional
# design choice, not an accident of implementation (see the docs section).
# Each firstmate home reconciles ONLY its own crews (the herdr_ws_owned=1 /
# herdr_parent_ws= records in its own state dir); supervisor anchor
# workspaces and workspaces firstmate did not create are NEVER moved, and
# their relative order is preserved exactly. Per-home reconciles compose into
# the global depth-first render because each home only ever pulls its own
# crews up under its own anchor.
#
# Fail-closed contract: any malformed or ambiguous ownership record, an
# unreadable workspace list, a move the plan would need on an unmanaged
# workspace, or ANY socket/writer error aborts the whole reconcile pass with
# a clear log, leaving the current order untouched. A recorded crew or parent
# that is simply no longer in the live list (normal mid-teardown churn, or a
# recreated anchor with a new id) is skipped with a note - those workspaces
# are left exactly where they are, never guessed at.

# FM_BACKEND_HERDR_MOVE_WRITER: test override for the workspace.move writer
# command (whitespace-split), mirroring FM_BACKEND_HERDR_EVENT_READER.
# Default: python3 bin/backends/herdr-move.py.

# fm_backend_herdr_workspace_order: the live flat workspace order for
# <session>, one workspace_id per line, straight from `workspace list` (the
# authoritative array order - WorkspaceInfo.number is a derived ordinal).
# Returns 1 when the list cannot be read or parsed, or is empty.
fm_backend_herdr_workspace_order() {  # <session>
  local list ids
  list=$(fm_backend_herdr_cli "$1" workspace list 2>/dev/null) || return 1
  ids=$(printf '%s' "$list" | jq -r 'if (.result.workspaces | type) == "array" then .result.workspaces[].workspace_id else error("missing result.workspaces") end' 2>/dev/null) || return 1
  [ -n "$ids" ] || return 1
  printf '%s\n' "$ids"
}

# fm_backend_herdr_workspace_move: send one typed workspace.move request over
# <session>'s control socket via the raw-socket writer. insert_index is the
# WIRE value herdr expects: the 0-based slot in the PRE-removal array the
# workspace is inserted before (verified empirically - docs/herdr-backend.md
# "workspace.move semantics"): a leftward move lands AT insert_index, a
# rightward move lands at insert_index-1, and insert_index == list length is
# valid (lands last; beyond that the server refuses with
# workspace_move_failed). fm_backend_herdr_contiguity_next_move emits this
# wire value directly. Non-destructive: the workspace's id, tabs, panes,
# agents, and focus are untouched. Any failure (unresolvable socket,
# connect/send error, server refusal, timeout) returns non-zero; callers
# must fail closed and abort their reconcile pass.
fm_backend_herdr_workspace_move() {  # <session> <workspace_id> <insert_index>
  local session=$1 wsid=$2 idx=$3 sock word
  [ -n "$wsid" ] || { echo "error: workspace_move requires a workspace id" >&2; return 1; }
  case "$idx" in ''|*[!0-9]*) echo "error: workspace_move insert_index must be a non-negative integer, got '$idx'" >&2; return 1 ;; esac
  local writer=()
  if [ -n "${FM_BACKEND_HERDR_MOVE_WRITER:-}" ]; then
    for word in $FM_BACKEND_HERDR_MOVE_WRITER; do
      writer+=("$word")
    done
  else
    command -v python3 >/dev/null 2>&1 || { echo "error: python3 is required for the herdr workspace.move socket writer" >&2; return 1; }
    writer=(python3 "$FM_BACKEND_HERDR_ROOT/bin/backends/herdr-move.py")
  fi
  sock=$(fm_backend_herdr_socket_path "$session")
  [ -n "$sock" ] || { echo "error: cannot resolve the herdr control socket for session '$session'" >&2; return 1; }
  "${writer[@]}" "$sock" "$wsid" "$idx"
}

# fm_backend_herdr_contiguity_edges: extract this home's ownership edges from
# <state_dir>'s task metas, one "child<TAB>parent" line per herdr_ws_owned=1
# task whose herdr_session matches <session>. herdr_ws_owned=1 is the ONLY
# ownership signal honored: it is written solely by fm-spawn.sh for a
# freshly-created, exclusively-owned child workspace, so every id emitted
# here is an EXACT firstmate-created workspace id. Returns 2 (after emitting
# nothing useful - callers must discard output on failure) when an owned meta
# is missing its child or parent id: that is missing ownership metadata, and
# the whole reconcile must refuse rather than guess.
fm_backend_herdr_contiguity_edges() {  # <state_dir> <session>
  local state=$1 session=$2 meta owned child parent msession
  for meta in "$state"/*.meta; do
    [ -f "$meta" ] || continue
    owned=$(grep '^herdr_ws_owned=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-)
    [ "$owned" = 1 ] || continue
    msession=$(grep '^herdr_session=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-)
    [ "$msession" = "$session" ] || continue
    child=$(grep '^herdr_workspace_id=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-)
    parent=$(grep '^herdr_parent_ws=' "$meta" 2>/dev/null | head -1 | cut -d= -f2-)
    if [ -z "$child" ] || [ -z "$parent" ]; then
      echo "error: $meta records herdr_ws_owned=1 but is missing herdr_workspace_id/herdr_parent_ws; refusing to reconcile" >&2
      return 2
    fi
    printf '%s\t%s\n' "$child" "$parent"
  done
  return 0
}

# fm_backend_herdr_contiguity_target: PURE target-order computation. Takes the
# live flat order (newline-separated ids), the ownership edges
# ("child<TAB>parent" lines), and an optional sibling-order reference (a
# prior order snapshot; ids missing from it sort last, in live order) and
# prints the canonical depth-first target order. The reference exists for
# the reconcile fixpoint: an in-transit rightward push reorders not-yet-fixed
# siblings in the LIVE list, so deriving sibling order from live alone makes
# the target flip-flop between iterations and the pass oscillate instead of
# converging - pinning sibling order to the pass's first snapshot keeps the
# target stable and preserves the pass-start relative order among crews.
# Aborts (return 2, nothing usable printed) on ambiguous or malformed
# ownership: a child claimed twice, a child equal to its parent, an id that is
# both child and parent, or a duplicate id in the live list. A child or
# parent absent from the live list drops that edge with a stderr note and
# leaves those workspaces exactly where they are (safe skip, never a guess).
# Unmanaged workspaces and supervisor anchors keep their live relative order.
fm_backend_herdr_contiguity_target() {  # <live-order> <edges> [<sibling-order-ref>]
  # Multi-line inputs travel via ENVIRON, not awk -v: BSD awk (macOS) rejects
  # a newline inside a -v value ("newline in string"), while ENVIRON carries
  # it fine on BSD awk, gawk, and mawk alike.
  FM_CONTIG_EDGES="$2" FM_CONTIG_REF="${3:-}" awk '
    BEGIN {
      FS = "\t"
      err = ""
      nref = split(ENVIRON["FM_CONTIG_REF"], refraw, "\n")
      nranks = 0
      for (k = 1; k <= nref; k++) {
        if (refraw[k] == "" || refraw[k] in rank) continue
        rank[refraw[k]] = ++nranks
      }
      nedges = split(ENVIRON["FM_CONTIG_EDGES"], raw, "\n")
      for (k = 1; k <= nedges; k++) {
        if (raw[k] == "") continue
        m = split(raw[k], f, "\t")
        if (m != 2 || f[1] == "" || f[2] == "") { err = "malformed ownership edge: " raw[k]; exit 2 }
        if (f[1] == f[2]) { err = "ambiguous ownership: workspace " f[1] " is its own parent"; exit 2 }
        if (f[1] in parent_of) { err = "ambiguous ownership: workspace " f[1] " is claimed by two tasks"; exit 2 }
        parent_of[f[1]] = f[2]
        is_parent[f[2]] = 1
      }
      for (c in parent_of) {
        if (c in is_parent) { err = "ambiguous ownership: workspace " c " is recorded as both a crew and a parent"; exit 2 }
      }
    }
    {
      if ($0 == "") next
      if ($0 in live_seen) { err = "duplicate workspace id in the live list: " $0; exit 2 }
      live_seen[$0] = 1
      live[++nlive] = $0
    }
    END {
      if (err != "") { print "herdr-contiguity: " err > "/dev/stderr"; exit 2 }
      for (c in parent_of) {
        if (!(c in live_seen)) {
          print "herdr-contiguity: note: owned workspace " c " is not in the live list; skipping it" > "/dev/stderr"
          delete parent_of[c]
          continue
        }
        if (!(parent_of[c] in live_seen)) {
          print "herdr-contiguity: note: parent " parent_of[c] " of " c " is not in the live list; leaving that group untouched" > "/dev/stderr"
          delete parent_of[c]
        }
      }
      # Crews per parent - the "stable order within each sibling class" rule:
      # siblings sort by their position in the reference snapshot when one
      # was given (the fixpoint pins the pass-start order this way), falling
      # back to live relative order (ids absent from the reference sort
      # last, in live order).
      for (i = 1; i <= nlive; i++) {
        w = live[i]
        if (w in parent_of) {
          p = parent_of[w]
          cnt[p]++
          crew_ws[p, cnt[p]] = w
          crew_key[p, cnt[p]] = (w in rank) ? rank[w] : nranks + nlive + i
        }
      }
      # Splice: every supervisor anchor is emitted in its live relative order,
      # immediately followed by ALL of its owned crews, BEFORE any later
      # workspace - including a child secondmate anchor that follows it in the
      # flat order. This is what makes a supervisor DIRECT crews always sort
      # before its child-secondmate subtrees: the canonical, intentional
      # depth-first rule (docs/herdr-backend.md "Workspace contiguity").
      for (i = 1; i <= nlive; i++) {
        w = live[i]
        if (w in parent_of) continue
        print w
        if (w in cnt) {
          n2 = cnt[w]
          for (a = 1; a <= n2; a++) { cw[a] = crew_ws[w, a]; ck[a] = crew_key[w, a] }
          for (a = 2; a <= n2; a++) {
            tw = cw[a]; tk = ck[a]; b = a - 1
            while (b >= 1 && ck[b] > tk) { cw[b + 1] = cw[b]; ck[b + 1] = ck[b]; b-- }
            cw[b + 1] = tw; ck[b + 1] = tk
          }
          for (a = 1; a <= n2; a++) print cw[a]
        }
      }
      exit 0
    }
  ' <<EOF
$1
EOF
}

# fm_backend_herdr_contiguity_next_move: PURE single-step planner. Given the
# live order, the target order, and the ownership edges, prints ONE safe move
# ("workspace_id<TAB>insert_index") that makes progress toward the target, or
# nothing when live already equals target (converged - the idempotence
# guarantee: a correct order plans zero moves). insert_index is the WIRE
# pre-removal value workspace.move expects (see
# fm_backend_herdr_workspace_move): a leftward pull emits the final slot
# itself, a rightward push emits final slot + 1 to compensate for the
# removal shift. Only ever plans a move for an OWNED crew id from the edges;
# if progress would require moving any other workspace it returns 2 and the
# caller must abort. Both emitted move shapes keep every unmanaged
# workspace's relative order intact: a leftward pull of the first
# out-of-place crew into its block, or a rightward push of a crew that sits
# inside a block it does not belong to.
fm_backend_herdr_contiguity_next_move() {  # <live-order> <target-order> <edges>
  # ENVIRON instead of awk -v for the same BSD-awk newline reason as
  # fm_backend_herdr_contiguity_target above.
  FM_CONTIG_LIVE="$1" FM_CONTIG_TARGET="$2" FM_CONTIG_EDGES="$3" awk '
    BEGIN {
      nl = split(ENVIRON["FM_CONTIG_LIVE"], L, "\n"); n = 0
      for (i = 1; i <= nl; i++) if (L[i] != "") LV[++n] = L[i]
      nt = split(ENVIRON["FM_CONTIG_TARGET"], T, "\n"); m = 0
      for (i = 1; i <= nt; i++) if (T[i] != "") TG[++m] = T[i]
      ne = split(ENVIRON["FM_CONTIG_EDGES"], E, "\n")
      for (i = 1; i <= ne; i++) {
        if (E[i] == "") continue
        split(E[i], f, "\t")
        if (f[1] != "") movable[f[1]] = 1
      }
      if (n != m) { print "herdr-contiguity: live/target length mismatch (" n " vs " m ")" > "/dev/stderr"; exit 2 }
      first = 0
      for (i = 1; i <= n; i++) if (LV[i] != TG[i]) { first = i; break }
      if (first == 0) exit 0
      x = TG[first]
      if (x in movable) { printf "%s\t%d\n", x, first - 1; exit 0 }
      y = LV[first]
      if (!(y in movable)) { print "herdr-contiguity: reaching the target order would move unmanaged workspace " y "; refusing" > "/dev/stderr"; exit 2 }
      t = 0
      for (i = 1; i <= m; i++) if (TG[i] == y) { t = i; break }
      if (t == 0) { print "herdr-contiguity: workspace " y " is missing from the target order; refusing" > "/dev/stderr"; exit 2 }
      # Rightward push: y (at 0-based first-1) must land at 0-based final
      # slot t-1, which on the wire is pre-removal insert_index t (the
      # removal of y from its earlier slot shifts everything after it left
      # by one - verified against real herdr 0.7.3).
      printf "%s\t%d\n", y, t
      exit 0
    }
  '
}

# fm_backend_herdr_contiguity_reconcile: the contiguity fixpoint loop. Reads
# this home's ownership edges from <state_dir>, then repeatedly: fresh live
# order -> pure target -> one planned move -> typed socket workspace.move,
# until converged. Re-reading the live list before EVERY move is what makes
# concurrent task start/finish safe: each move is planned against reality,
# never a stale snapshot. Bounded at (2*edges+4) iterations; hitting the
# bound (pathological concurrent churn) aborts with a log and returns 1 -
# the next lifecycle reconcile converges once the churn settles. No owned
# edges is a healthy no-op (return 0). EVERY failure path (unreadable list,
# ambiguous ownership, unmanaged-move plan, writer/socket error, no
# convergence) aborts immediately, leaves the current order untouched from
# that point, logs clearly, and returns 1.
fm_backend_herdr_contiguity_reconcile() {  # <session> <state_dir>
  local session=$1 state=$2 edges live target move wsid idx iter max nedges moves=0 sibling_ref=
  edges=$(fm_backend_herdr_contiguity_edges "$state" "$session") || {
    echo "error: herdr contiguity reconcile aborted: unusable ownership metadata in $state" >&2
    return 1
  }
  [ -n "$edges" ] || return 0
  nedges=$(printf '%s\n' "$edges" | grep -c .)
  max=$((nedges * 2 + 4))
  for ((iter = 0; iter < max; iter++)); do
    live=$(fm_backend_herdr_workspace_order "$session") || {
      echo "error: herdr contiguity reconcile aborted: cannot read the workspace list for session '$session'" >&2
      return 1
    }
    # Pin sibling order to this pass's FIRST snapshot: an in-transit
    # rightward push reorders not-yet-fixed siblings in the live list, and
    # re-deriving sibling order from live would flip-flop the target between
    # iterations (oscillation instead of convergence).
    [ -n "$sibling_ref" ] || sibling_ref=$live
    target=$(fm_backend_herdr_contiguity_target "$live" "$edges" "$sibling_ref") || {
      echo "error: herdr contiguity reconcile aborted for session '$session' (see the reason above); order left untouched" >&2
      return 1
    }
    move=$(fm_backend_herdr_contiguity_next_move "$live" "$target" "$edges") || {
      echo "error: herdr contiguity reconcile aborted for session '$session' (see the reason above); order left untouched" >&2
      return 1
    }
    if [ -z "$move" ]; then
      [ "$moves" -gt 0 ] && echo "herdr-contiguity: session '$session' reconciled with $moves move(s)" >&2
      return 0
    fi
    wsid=${move%%$'\t'*}
    idx=${move#*$'\t'}
    fm_backend_herdr_workspace_move "$session" "$wsid" "$idx" || {
      echo "error: herdr contiguity reconcile aborted: workspace.move failed for '$wsid' (session '$session'); order left as-is" >&2
      return 1
    }
    moves=$((moves + 1))
  done
  echo "error: herdr contiguity reconcile for session '$session' did not converge within $max moves (concurrent churn?); aborting" >&2
  return 1
}

# fm_backend_herdr_pane_agent_state: classify <pane_id> in <session> as one of
# dead|no-agent|live|unknown, purely from the JSON body of two read-only
# calls - never from process exit status, since a business-logic "not found"
# response is a normal, expected outcome here, not a call failure (real herdr
# 0.7.1 exits 1 for it; the canned-response test fakes exit 0; parsing only
# the JSON keeps this function correct against either).
#
#   dead     - `pane get` responds with error code pane_not_found: the pane
#              itself is gone (closed, or its process died and herdr already
#              reaped it - verified empirically: killing a pane's shell pid
#              on a live server makes herdr immediately drop both the pane
#              and its tab from `pane get`/`tab list`).
#   no-agent - `pane get` succeeds (the pane structurally exists) but `agent
#              get` responds with error code agent_not_found: nothing is
#              registered in it - exactly what a herdr session-layout restore
#              produces (verified empirically: `session stop` + fresh `herdr
#              server` restart leaves the pane alive, agent_status "unknown",
#              agent get -> agent_not_found - docs/herdr-backend.md "ID
#              stability across a server restart"), and what a future
#              `resume_agents_on_restore = false` restore would produce too
#              (a plain shell, never an agent).
#   live     - `agent get` succeeds and reports a real agent_status (working,
#              idle, done, or blocked - any registered value). An idle or
#              blocked agent is still a genuine, still-registered agent, not
#              a restored husk, so it is never a close-and-replace candidate.
#   unknown  - anything else: an unparseable/unexpected response from either
#              call, or a `pane get` success whose own echoed pane_id does not
#              round-trip (guards against misreading a herdr response shape
#              change as "the pane exists"). The caller must fail safe toward
#              refusal here, never toward closing - this is the conservative
#              backstop the husk check depends on.
fm_backend_herdr_pane_agent_state() {  # <session> <pane_id>
  local session=$1 pane_id=$2 out code pid status
  # 2>&1, not 2>/dev/null: verified empirically that real herdr 0.7.1 writes
  # an error response's JSON body to STDERR (success bodies go to stdout), so
  # discarding stderr here would blind this function to exactly the
  # error.code values (pane_not_found, agent_not_found) it exists to read -
  # every OTHER call site in this file discards stderr safely only because
  # its caller collapses both the error and the not-an-error paths to the
  # same final answer, which this function's dead/no-agent/live/unknown
  # distinction cannot afford to do.
  out=$(fm_backend_herdr_cli "$session" pane get "$pane_id" 2>&1)
  code=$(printf '%s' "$out" | jq -r '.error.code // empty' 2>/dev/null)
  if [ -n "$code" ]; then
    [ "$code" = "pane_not_found" ] && printf 'dead' || printf 'unknown'
    return 0
  fi
  pid=$(printf '%s' "$out" | jq -r '.result.pane.pane_id // empty' 2>/dev/null)
  if [ "$pid" != "$pane_id" ]; then
    printf 'unknown'
    return 0
  fi
  out=$(fm_backend_herdr_cli "$session" agent get "$pane_id" 2>&1)
  code=$(printf '%s' "$out" | jq -r '.error.code // empty' 2>/dev/null)
  if [ -n "$code" ]; then
    [ "$code" = "agent_not_found" ] && printf 'no-agent' || printf 'unknown'
    return 0
  fi
  status=$(printf '%s' "$out" | jq -r '.result.agent.agent_status // empty' 2>/dev/null)
  case "$status" in
    working|idle|done|blocked) printf 'live' ;;
    *) printf 'unknown' ;;
  esac
}

# fm_backend_herdr_tab_is_husk: true (0) only for the two conservative husk
# states (dead, no-agent) fm_backend_herdr_pane_agent_state can positively
# confirm; live and unknown both refuse (1), so an inconclusive read never
# licenses closing anything. Restored-layout recovery depends on this
# fail-safe-toward-refusal behavior.
fm_backend_herdr_tab_is_husk() {  # <session> <pane_id>
  case "$(fm_backend_herdr_pane_agent_state "$1" "$2")" in
    dead|no-agent) return 0 ;;
    *) return 1 ;;
  esac
}

# fm_backend_herdr_agent_alive: CONFIDENT liveness of a live harness-agent
# PROCESS under <target> ("<session>:<pane_id>"), for the same
# session-start secondmate-liveness sweep fm_backend_tmux_agent_alive serves
# (bin/fm-bootstrap.sh; docs/herdr-backend.md "Agent liveness probe reuses the
# husk classifier"). Reuses fm_backend_herdr_pane_agent_state, the
# already-verified husk classifier ("Respawn idempotency" above): `dead`
# (structurally gone pane) and `no-agent` (a restored, agent-less bare shell
# - EXACTLY the shape a dead secondmate leaves behind) both collapse to
# `dead`; `live` (a real registered agent_status, including idle/blocked)
# maps to `alive`; `unknown` stays `unknown` - fail-safe toward refusal,
# exactly like the husk check itself. Callers must never treat `unknown` as a
# confirmed-dead signal.
fm_backend_herdr_agent_alive() {  # <target>
  local target=$1
  fm_backend_herdr_parse_target "$target" || { printf 'unknown'; return 0; }
  case "$(fm_backend_herdr_pane_agent_state "$FM_BACKEND_HERDR_SESSION" "$FM_BACKEND_HERDR_PANE")" in
    dead|no-agent) printf 'dead' ;;
    live) printf 'alive' ;;
    *) printf 'unknown' ;;
  esac
}

# fm_backend_herdr_create_task: create the task's tab (one pane) in
# <container> ("session:workspace_id"). Herdr does NOT enforce label
# uniqueness itself (verified: two tabs can share a label), so the duplicate
# check is ours, mirroring tmux's manual check.
#
# A same-labeled tab already existing no longer means an automatic refusal:
# herdr persists and restores its whole session layout (workspaces/tabs/
# panes) across a server restart, including a reboot, and a restored fm-<id>
# task tab comes back a HUSK - a dead pane, or (today, and unconditionally
# once a future `resume_agents_on_restore = false` config ships) a plain
# agent-less shell sitting in the saved cwd, never the crewmate that used to
# be there. Before this fix, every fleet respawn after such a restart needed
# the operator to manually close each husk pane first before firstmate could
# spawn into it again. fm_backend_herdr_tab_is_husk classifies the existing
# tab's pane conservatively (dead or no-agent only; anything live or
# ambiguous refuses exactly as before) and, when it is a confirmed husk,
# this function CLOSES AND REPLACES it instead of refusing.
#
# Ordering is deliberate: the REPLACEMENT tab is created FIRST, and the husk
# is closed only AFTER that succeeds - never the reverse. Closing a
# workspace's LAST remaining tab deletes the whole workspace on real herdr
# (docs/herdr-backend.md "Workspace lifecycle"), and a session-restore husk
# can legitimately be that workspace's only tab (e.g. its own seeded default
# tab was already pruned, long before the restart, by a prior real task tab
# existing alongside it). Herdr's lack of label-uniqueness enforcement is
# exactly what makes this safe: the new and the husk tab can briefly share
# the same label with no error, so the workspace never drops to zero tabs.
# This mirrors fm_backend_herdr_workspace_prune_seeded_default_tab's own
# create-before-close safety argument.
#
# --no-focus: verified tab create never focuses by default regardless of
# sibling tabs, so this is defense in depth rather than a behavior change.
# <seeded_default_tab_id> (4th arg, may be empty) is exactly the value
# fm_backend_herdr_workspace_ensure captured as FM_BACKEND_HERDR_WS_SEEDED_TAB_ID
# for THIS SAME container - non-empty only when this spawn's own
# container_ensure call just created the workspace. Once the real task tab
# above is created, this is the ONLY input that may trigger a prune, and it is
# passed by the caller, never re-derived here from tab list contents or
# labels (the live-fire self-kill fix - see
# fm_backend_herdr_workspace_prune_seeded_default_tab for the incident and
# the safety argument). An ADOPTED workspace's caller always passes an empty
# 4th arg, so this function never even queries for a prune candidate in that
# case. Echoes "<tab_id> <pane_id>" on success.
fm_backend_herdr_create_task() {  # <container> <label> <cwd> <seeded_default_tab_id>
  local container=$1 label=$2 cwd=$3 seeded_tab_id=${4:-} session wsid list dup_tabs dup dup_pane dup_tab_ids out tab_id pane_id remaining_dup_tabs
  session=${container%%:*}
  wsid=${container#*:}
  list=$(fm_backend_herdr_cli "$session" tab list --workspace "$wsid" 2>/dev/null) || return 1
  dup_tabs=$(printf '%s' "$list" | jq -r --arg want "$label" 'if (.result.tabs | type) == "array" then .result.tabs[] | select(.label == $want) | .tab_id else error("missing result.tabs") end' 2>/dev/null) || {
    echo "error: could not parse herdr tab list output for workspace $wsid (session $session)" >&2
    return 1
  }
  dup_tab_ids=""
  if [ -n "$dup_tabs" ]; then
    while IFS= read -r dup; do
      [ -n "$dup" ] || continue
      dup_pane=$(fm_backend_herdr_pane_for_tab "$session" "$wsid" "$dup")
      if [ -z "$dup_pane" ] || ! fm_backend_herdr_tab_is_husk "$session" "$dup_pane"; then
        echo "error: herdr tab '$label' already exists in workspace $wsid (session $session)" >&2
        return 1
      fi
      dup_tab_ids="${dup_tab_ids}${dup}"$'\n'
    done <<EOF
$dup_tabs
EOF
  fi
  out=$(fm_backend_herdr_cli "$session" tab create --workspace "$wsid" --cwd "$cwd" --label "$label" --no-focus 2>/dev/null) || return 1
  tab_id=$(printf '%s' "$out" | jq -r '.result.tab.tab_id // empty' 2>/dev/null)
  pane_id=$(printf '%s' "$out" | jq -r '.result.root_pane.pane_id // empty' 2>/dev/null)
  if [ -z "$tab_id" ] || [ -z "$pane_id" ]; then
    echo "error: could not parse tab/pane id from herdr tab create output" >&2
    return 1
  fi
  [ -z "$seeded_tab_id" ] || fm_backend_herdr_workspace_prune_seeded_default_tab "$session" "$wsid" "$seeded_tab_id"
  if [ -n "$dup_tab_ids" ]; then
    while IFS= read -r dup; do
      [ -n "$dup" ] || continue
      fm_backend_herdr_cli "$session" tab close "$dup" >/dev/null 2>&1 || true
    done <<EOF
$dup_tab_ids
EOF
    list=$(fm_backend_herdr_cli "$session" tab list --workspace "$wsid" 2>/dev/null) || {
      echo "error: could not verify herdr husk removal for tab '$label' in workspace $wsid (session $session)" >&2
      return 1
    }
    if ! printf '%s' "$list" | jq -e '(.result.tabs | type) == "array"' >/dev/null 2>&1; then
      echo "error: could not parse herdr tab list output for workspace $wsid (session $session)" >&2
      return 1
    fi
    remaining_dup_tabs=$(printf '%s' "$list" | jq -r --arg want "$label" --arg replacement "$tab_id" \
      '.result.tabs[]? | select(.label == $want and .tab_id != $replacement) | .tab_id' 2>/dev/null)
    remaining_dup_tabs=${remaining_dup_tabs//$'\n'/ }
    if [ -n "$remaining_dup_tabs" ]; then
      echo "error: failed to remove preexisting herdr tab(s) $remaining_dup_tabs for label '$label' in workspace $wsid (session $session)" >&2
      return 1
    fi
  fi
  printf '%s %s' "$tab_id" "$pane_id"
}

# fm_backend_herdr_parse_target: split "<session>:<pane_id>" (pane_id itself
# contains a colon, e.g. "w1:p2") on the FIRST colon only. Sets
# FM_BACKEND_HERDR_SESSION and FM_BACKEND_HERDR_PANE for the caller.
fm_backend_herdr_parse_target() {  # <target>
  local target=$1
  FM_BACKEND_HERDR_SESSION=${target%%:*}
  FM_BACKEND_HERDR_PANE=${target#*:}
  [ -n "$FM_BACKEND_HERDR_SESSION" ] && [ -n "$FM_BACKEND_HERDR_PANE" ] && [ "$FM_BACKEND_HERDR_PANE" != "$target" ]
}

fm_backend_herdr_target_ready() {  # <target>
  fm_backend_herdr_parse_target "$1" || return 1
  fm_backend_herdr_server_ensure "$FM_BACKEND_HERDR_SESSION" || return 1
}

# fm_backend_herdr_current_path: the live FOREGROUND process's cwd, or empty on
# any error. Mirrors tmux's pane_current_path poll used for worktree-path
# discovery after `treehouse get`.
#
# Verified pitfall: `pane get`'s `.result.pane.cwd` is the pane's cwd AT
# CREATION TIME - the top-level shell's cwd - and does NOT update when that
# shell `cd`s or enters a subshell (as `treehouse get` does). Reading it here
# would make fm-spawn.sh's worktree-discovery poll never see the pane "leave"
# the project directory, since `cwd` stays frozen at the original path forever.
# `.result.pane.foreground_cwd` tracks the ACTUALLY RUNNING foreground
# process's cwd instead, which is what changes when `treehouse get` enters its
# worktree subshell - confirmed live against a real treehouse acquisition.
fm_backend_herdr_current_path() {  # <target>
  fm_backend_herdr_target_ready "$1" || return 0
  fm_backend_herdr_cli "$FM_BACKEND_HERDR_SESSION" pane get "$FM_BACKEND_HERDR_PANE" 2>/dev/null \
    | jq -r '.result.pane.foreground_cwd // empty' 2>/dev/null
}

# fm_backend_herdr_send_text_line: send one line of TEXT then submit,
# ATOMICALLY - mirrors tmux's `send-keys -t T text Enter`. Used for the fixed
# spawn-time commands (treehouse get, the GOTMPDIR export). `pane run` types
# the command and submits it in one call (verified).
fm_backend_herdr_send_text_line() {  # <target> <text>
  fm_backend_herdr_target_ready "$1" || return 1
  fm_backend_herdr_cli "$FM_BACKEND_HERDR_SESSION" pane run "$FM_BACKEND_HERDR_PANE" "$2" >/dev/null 2>&1
}

# fm_backend_herdr_send_literal: send TEXT as literal, UNSUBMITTED input - the
# caller sends Enter separately. Mirrors tmux's `send-keys -t T -l text`.
# Verified: `pane send-text` does NOT auto-submit (contrary to the addendum's
# original guess); it behaves exactly like tmux's `-l` literal send.
fm_backend_herdr_send_literal() {  # <target> <text>
  fm_backend_herdr_target_ready "$1" || return 1
  fm_backend_herdr_cli "$FM_BACKEND_HERDR_SESSION" pane send-text "$FM_BACKEND_HERDR_PANE" "$2" >/dev/null 2>&1
}

# fm_backend_herdr_normalize_key: map firstmate's key vocabulary (Enter,
# Escape, C-c, as used by fm-send.sh --key and stuck-crewmate-recovery) onto
# herdr's `pane send-keys` names. Verified empirically: enter, escape/esc, and
# both ctrl+c/C-c all work (case-insensitive on herdr's side, but normalize
# explicitly rather than relying on that).
fm_backend_herdr_normalize_key() {  # <key>
  case "$1" in
    Enter|enter) printf 'enter' ;;
    Escape|escape|Esc|esc) printf 'escape' ;;
    C-c|c-c|ctrl+c|Ctrl+C) printf 'ctrl+c' ;;
    *) printf '%s' "$1" ;;
  esac
}

# fm_backend_herdr_send_key: one named special key. Mirrors fm-send.sh's --key
# path (tmux's `send-keys -t T key`).
fm_backend_herdr_send_key() {  # <target> <key>
  fm_backend_herdr_target_ready "$1" || return 1
  local key
  key=$(fm_backend_herdr_normalize_key "$2")
  fm_backend_herdr_cli "$FM_BACKEND_HERDR_SESSION" pane send-keys "$FM_BACKEND_HERDR_PANE" "$key" >/dev/null 2>&1
}

# fm_backend_herdr_capture: bounded plain-text pane capture. Mirrors
# fm-peek.sh's/fm-watch.sh's `tmux capture-pane -p -t T -S -N`. --source recent
# is the closest herdr analogue to tmux's scrollback-bounded capture.
#
# Verified CLI quirk (herdr-verification-p2.md "pane read --lines bug", v0.7.1):
# `pane read --source recent --lines N` returns COMPLETELY EMPTY output when N
# is smaller than the pane's current viewport height (observed threshold ~23
# rows for a default-sized pane), instead of clamping to the last N lines - it
# does not merely ignore the bound, it drops the read entirely. This silently
# broke exactly the small bounded reads this adapter relies on most (including
# the composer-state guard/fallback reads around submit and injection). Workaround:
# always request a generous fetch far above any realistic viewport height, then
# trim to the caller's requested bound ourselves with `tail`.
fm_backend_herdr_capture() {  # <target> <lines>
  fm_backend_herdr_target_ready "$1" || return 1
  local lines=${2:-200} fetch out
  case "$lines" in ''|*[!0-9]*) lines=200 ;; esac
  fetch=$lines
  case "$fetch" in ''|*[!0-9]*) fetch=200 ;; *) [ "$fetch" -ge 200 ] || fetch=200 ;; esac
  out=$(fm_backend_herdr_cli "$FM_BACKEND_HERDR_SESSION" pane read "$FM_BACKEND_HERDR_PANE" --source recent --lines "$fetch" 2>/dev/null) || return 1
  printf '%s' "$out" | tail -n "$lines"
}

fm_backend_herdr_capture_ansi() {  # <target> <lines>
  fm_backend_herdr_target_ready "$1" || return 1
  local lines=${2:-200} fetch out
  case "$lines" in ''|*[!0-9]*) lines=200 ;; esac
  fetch=$lines
  case "$fetch" in ''|*[!0-9]*) fetch=200 ;; *) [ "$fetch" -ge 200 ] || fetch=200 ;; esac
  out=$(fm_backend_herdr_cli "$FM_BACKEND_HERDR_SESSION" pane read "$FM_BACKEND_HERDR_PANE" --source recent --lines "$fetch" --format ansi 2>/dev/null) || return 1
  printf '%s' "$out" | tail -n "$lines"
}

# Thin adapter over the shared plain-text stripper (bin/fm-composer-lib.sh),
# used only for STRUCTURAL row/shape detection where ghost text must be kept so
# the box border or bare prompt glyph is still visible. Content extraction uses
# the shared fm_composer_strip_ghost instead.
fm_backend_herdr_strip_ansi() {  # <text>
  printf '%s' "$1" | fm_composer_strip_ansi
}

# fm_backend_herdr_composer_state: classify the composer's own row as
# empty|pending|unknown, scanning a generous tail-window capture of <target>.
# herdr's CLI exposes no cursor-row primitive (unlike tmux's #{cursor_y}), so
# this locates the composer row structurally, recognizing TWO row shapes and
# keeping whichever match comes LAST (scanning forward), so a shape earlier in
# scrollback/a popup can never outrank the real (bottom-anchored) composer row:
#
#   bordered - a boxed composer (verified grok 0.2.82): the row's TRIMMED
#              content both STARTS and ENDS with the same border glyph (│, ┃,
#              or a plain ASCII |). The box's own top/bottom rows use rounded
#              corners (╭─…─╮ / ╰─…─╯), which never match; popup item rows and
#              horizontal separator rows carry no border glyph at all; the
#              footer help line ("Enter:send │ … │ …") uses │ only as an
#              INTERIOR separator and does not start with one, so it never
#              matches either.
#   bare     - an UNBORDERED composer (verified real claude 2.x and codex
#              0.142.x, both under herdr 0.7.1, docs/herdr-backend.md
#              "Incident (2026-07-07)"): the row's TRIMMED content starts with
#              one of the verified agent-specific prompt glyphs but carries no
#              closing border at all - claude's own live input row is a bare
#              "❯ …" with no surrounding │, and codex's is a bare "› …". Both
#              harnesses ALSO render bordered decorative boxes elsewhere (a
#              startup welcome banner, an update-available notice) that
#              satisfy the bordered shape above; requiring a match on EITHER
#              shape and keeping the last (bottom-most) one is what keeps the
#              live composer winning over a stale decorative box still sitting
#              in the same capture window - a bordered box is only ever
#              followed later on screen by the actual live composer, never the
#              reverse, in every harness observed so far. The bare shape is
#              deliberately narrower than the bordered content classifier so a
#              no-agent shell fallback prompt (`>`, `$`, `%`, or `#`) falls
#              through to `unknown` instead of being misread as delivered.
#
#   empty   - blank, a bare prompt glyph, known ghost/placeholder text
#             ("Type a message...", verified grok 0.2.82's empty-composer
#             placeholder), or only de-emphasised ANSI ghost/placeholder text
#             recognized by the shared fm_composer_strip_ghost extractor
#             (dim/faint or dark-TRUECOLOR foreground). Safe to treat as
#             submitted.
#   pending - real, unsubmitted text sits in the composer. This deliberately
#             also covers a slash-command popup that just closed but only
#             auto-completed or filled an argument-hint placeholder into the
#             composer (e.g. "/compact" -> "/compact compaction
#             instructions", verified live against real grok 0.2.82) - that
#             first Enter is a SELECTION, not a submission.
#   unknown - the pane could not be read, or no composer row (of either shape)
#             was found in the captured window.
#
# Ghost/placeholder note: herdr's ANSI pane read preserves the harness's own
# de-emphasis styling, and the classifier extracts real typed content with the
# shared fm_composer_strip_ghost (bin/fm-composer-lib.sh), which drops dim/faint
# runs (claude's rotating prompt suggestion, codex's idle suggestion after the
# bare `›` prompt) AND dark/muted truecolor foreground runs (grok's placeholder),
# while keeping non-de-emphasised real typed input. This is the same owner the
# tmux adapter routes through, so the two backends cannot drift (task
# afk-herdr-false-pending); it superseded a herdr-only faint byte-pattern check
# that recognized only codex's bold-wrapped bare prompt and missed claude's own
# dim ghost - the overnight away-mode injection wedge on the primary claude pane.
FM_BACKEND_HERDR_COMPOSER_LINES=${FM_BACKEND_HERDR_COMPOSER_LINES:-20}
# Known ghost/placeholder composer text. Extend this if another
# herdr-verified harness needs its own idle placeholder recognized.
FM_BACKEND_HERDR_IDLE_RE=${FM_BACKEND_HERDR_IDLE_RE:-'^Type a message\.\.\.$'}
# Known bare (unbordered) prompt glyphs a composer row may start with: ❯
# (claude) and › (codex) only. Generic shell-style glyphs > $ % # are still
# recognized after a bordered composer row has already been structurally found.
FM_BACKEND_HERDR_BARE_PROMPT_RE=${FM_BACKEND_HERDR_BARE_PROMPT_RE:-'^[❯›]'}

fm_backend_herdr_composer_state() {  # <target> -> empty|pending|unknown
  local target=$1 cap line trimmed found=0 shape="" raw_match="" bordered=0 stripped
  cap=$(fm_backend_herdr_capture_ansi "$target" "$FM_BACKEND_HERDR_COMPOSER_LINES" 2>/dev/null \
    || fm_backend_herdr_capture "$target" "$FM_BACKEND_HERDR_COMPOSER_LINES") || { printf 'unknown'; return 0; }
  # Structural scan: locate the bottom-most composer row and remember its RAW
  # (styled) bytes. Shape detection runs on the plain row (fm_backend_herdr_strip_ansi
  # keeps ghost text so the border/prompt glyph is still visible); the raw row is
  # kept for ANSI-aware content extraction after the scan.
  while IFS= read -r line; do
    trimmed=$(fm_backend_herdr_strip_ansi "$line")
    trimmed="${trimmed#"${trimmed%%[![:space:]]*}"}"
    trimmed="${trimmed%"${trimmed##*[![:space:]]}"}"
    [ -n "$trimmed" ] || continue
    case "$trimmed" in
      '│'*'│'|'┃'*'┃'|'|'*'|')
        shape=bordered
        raw_match=$line
        found=1
        ;;
      *)
        if printf '%s' "$trimmed" | grep -qE "$FM_BACKEND_HERDR_BARE_PROMPT_RE"; then
          shape=bare
          raw_match=$line
          found=1
        fi
        ;;
    esac
  done < <(printf '%s\n' "$cap")
  [ "$found" -eq 1 ] || { printf 'unknown'; return 0; }
  # Content: extract the real typed text from the raw row with the shared,
  # fleet-wide ghost stripper (bin/fm-composer-lib.sh), which drops dim/faint AND
  # dark-truecolor ghost/placeholder runs. This replaces the former herdr-only
  # faint byte-pattern check (which recognized only Codex's bold-wrapped bare
  # prompt and missed claude's own dim prompt-suggestion ghost - the overnight
  # afk-herdr-false-pending wedge) and, in a dark theme, drops the composer's own
  # dark box border too, which is why the bordered flag was read from the plain
  # shape above, not from this ghost-stripped content.
  stripped=$(printf '%s\n' "$raw_match" | fm_composer_strip_ghost)
  stripped="${stripped#"${stripped%%[![:space:]]*}"}"
  stripped="${stripped%"${stripped##*[![:space:]]}"}"
  if [ "$shape" = bordered ]; then
    bordered=1
    stripped=${stripped//│/}
    stripped=${stripped//┃/}
    stripped=${stripped//|/}
    stripped="${stripped#"${stripped%%[![:space:]]*}"}"
    stripped="${stripped%"${stripped##*[![:space:]]}"}"
  fi
  # Delegate the empty/pending/unknown decision to the shared owner. The bare
  # shape only ever starts with an AGENT glyph (FM_BACKEND_HERDR_BARE_PROMPT_RE
  # is '^[❯›]'), so a bare shell prompt never reaches here - it stays 'unknown'
  # via the no-composer-row path above, exactly as before.
  fm_composer_classify_content "$bordered" "$stripped" "$FM_BACKEND_HERDR_IDLE_RE"
}

# fm_backend_herdr_send_text_submit: type <text> into <target> once (raw,
# unsubmitted, via send_literal), then submit with a named Enter key, retried
# (Enter only, never retyped) until herdr's NATIVE agent-state (agent get)
# confirms a real turn started. Verified hazard (herdr-verification-p2.md
# "slash/$ autocomplete popup"): a `/`- or `$`-prefixed send opens a
# completion popup within ~0.1s, exactly like tmux's claude/codex popups, so
# the caller's <settle> before the first Enter matters here the same way it
# does for tmux.
#
# Confirmation signal (rewritten for the 2026-07-07 incident below;
# superseded a composer-content read that itself replaced a delta-based check
# for the 2026-07-03 incident): when the target is legibly idle before Enter,
# submission is confirmed by fm_backend_herdr_wait_for_working observing a
# submit-active agent_status after Enter, NOT by reading the composer's own
# row. This makes the normal confirmation path cross-agent: it is the same
# semantic signal regardless of what text a harness's idle composer happens
# to display.
#
# Incident (2026-07-07, followed up on 2026-07-08): a redelivery loop in the
# away-mode daemon. Root cause: composer-content submit confirmation was too
# sensitive to harness rendering details. Real claude/codex use bare prompt
# rows, and real codex adds dynamic idle suggestions after `›`; the later
# ANSI-aware composer classifier now handles the pre-injection guard for that
# Codex shape, but idle-baseline submit confirmation deliberately stays on
# native agent-state so delivery does not depend on composer text. Composer
# content is retained for other callers (the away-mode daemon's PRE-injection
# empty-box guard, still dispatched via fm_backend_composer_state /
# fm_backend_herdr_composer_state) and for submit attempts whose pre-Enter
# agent-state baseline is not legibly idle.
#
# This also still correctly handles the earlier 2026-07-03 incident (a
# slash-command popup selection/placeholder-fill on the FIRST Enter is not a
# genuine submission) without any popup-specific logic at all: filling a
# composer placeholder never starts a turn, so agent_status simply never
# reports "working" for that Enter, and the retry loop below sends a second
# Enter exactly as it did before - the fix generalizes instead of special-
# casing the popup shape.
#
# Failure-mode analysis (the two directions the caller-facing contract must
# not get wrong - see docs/herdr-backend.md "Native agent-state submit
# confirmation" for the empirical timing behind this):
#   - Slow transition: fm_backend_herdr_wait_for_working samples repeatedly
#     across herdr's per-attempt confirmation budget (not once at the end), so a
#     transition landing partway through a window is still caught before this
#     loop gives up and sends a needless extra Enter.
#   - Instant round-trip (a turn starts AND returns to idle between two
#     polls): unavoidable in the absolute, but bounded by how tightly polls
#     are packed into the budget; real claude/codex measured first-working
#     at 90-490ms, comfortably inside a several-hundred-ms, multiply-sampled
#     window, so this has not been observed in practice. On the (unobserved)
#     residual chance it happens, the verdict is "pending" and the caller
#     never retypes - only re-sends Enter, which lands on an already-empty
#     composer and is a no-op, not a duplicate delivery of <text> (see
#     fm-send.sh/fm-supervise-daemon.sh: retyping only happens if a caller
#     re-invokes this function from scratch with the same text after seeing
#     an error, which is a human/escalation decision, not an automatic
#     retry).
# Echoes empty|pending|unknown|send-failed, the SAME vocabulary fm-send.sh
# already branches on for tmux ("empty" means "confirmed submitted" for every
# backend; how each backend confirms it is an internal decision - herdr's is
# no longer literally "the composer read empty").
fm_backend_herdr_send_text_submit() {  # <target> <text> <retries> <enter-sleep> <settle>
  local target=$1 text=$2 retries=$3 sleep_s=$4 settle=$5 i=0 verdict baseline confirm_sleep
  fm_backend_herdr_parse_target "$target" || { printf 'unknown'; return 0; }
  fm_backend_herdr_send_literal "$target" "$text" || { printf 'send-failed'; return 0; }
  sleep "$settle"
  baseline=$(fm_backend_herdr_classify_submit_agent_status \
    "$(fm_backend_herdr_agent_status_raw "$FM_BACKEND_HERDR_SESSION" "$FM_BACKEND_HERDR_PANE")")
  confirm_sleep=$(fm_backend_herdr_submit_confirm_budget "$sleep_s")
  while :; do
    fm_backend_herdr_send_key "$target" Enter || true
    if [ "$baseline" = idle ]; then
      verdict=$(fm_backend_herdr_wait_for_working "$FM_BACKEND_HERDR_SESSION" "$FM_BACKEND_HERDR_PANE" \
        "$confirm_sleep" "$FM_BACKEND_HERDR_SUBMIT_POLLS")
    else
      sleep "$sleep_s"
      verdict=$(fm_backend_herdr_composer_state "$target")
    fi
    case "$verdict" in
      busy) printf 'empty'; return 0 ;;
      empty) printf 'empty'; return 0 ;;
      unknown) printf 'unknown'; return 0 ;;
    esac
    i=$((i + 1))
    [ "$i" -lt "$retries" ] || { printf 'pending'; return 0; }
  done
}

# fm_backend_herdr_kill: remove the task's pane, best-effort (mirrors
# tmux-kill-window's `|| true` contract). Verified: closing a tab's only pane
# closes the tab too, so a separate tab close is unnecessary.
fm_backend_herdr_kill() {  # <target>
  fm_backend_herdr_target_ready "$1" || return 0
  fm_backend_herdr_cli "$FM_BACKEND_HERDR_SESSION" pane close "$FM_BACKEND_HERDR_PANE" >/dev/null 2>&1 || true
}

# fm_backend_herdr_classify_agent_status: map a raw `agent get` agent_status
# value to the adapter's watcher busy|idle|unknown vocabulary. working ->
# busy (actively generating); idle/done -> idle; blocked -> idle (a blocked
# agent is stuck waiting on the human, not grinding - the watcher should
# treat it like a stale pane needing attention, not suppress it as busy);
# unknown/unparseable/empty -> unknown, the caller's cue to fall back to
# pane-regex detection.
fm_backend_herdr_classify_agent_status() {  # <raw-agent_status>
  case "$1" in
    working) printf 'busy' ;;
    idle|done) printf 'idle' ;;
    blocked) printf 'idle' ;;
    *) printf 'unknown' ;;
  esac
}

fm_backend_herdr_classify_submit_agent_status() {  # <raw-agent_status>
  case "$1" in
    working|blocked) printf 'busy' ;;
    idle|done) printf 'idle' ;;
    *) printf 'unknown' ;;
  esac
}

# fm_backend_herdr_agent_status_raw: one `agent get` read, echoing the raw
# agent_status string (working/idle/done/blocked/...), or empty on any
# failure. Deliberately skips fm_backend_herdr_target_ready's server-ensure
# round trip (an extra `status --json` call) that fm_backend_herdr_busy_state
# pays on every call: fm_backend_herdr_wait_for_working polls this in a tight
# loop right after a caller has already parsed the target and confirmed the
# server is live (e.g. fm_backend_herdr_send_text_submit, immediately after a
# successful send-text), so re-checking server liveness on every poll would
# only add latency without adding safety.
fm_backend_herdr_agent_status_raw() {  # <session> <pane_id>
  local session=$1 pane_id=$2 out
  out=$(fm_backend_herdr_cli "$session" agent get "$pane_id" 2>/dev/null) || { printf ''; return 0; }
  printf '%s' "$out" | jq -r '.result.agent.agent_status // empty' 2>/dev/null
}

# fm_backend_herdr_busy_state: semantic busy state from herdr's native
# agent-state detection (agent.get), the "first backend where fm_session_busy_state
# gets real semantics" per the design report. See
# fm_backend_herdr_classify_agent_status for the status->busy/idle/unknown
# mapping.
fm_backend_herdr_busy_state() {  # <target>
  fm_backend_herdr_target_ready "$1" || { printf 'unknown'; return 0; }
  fm_backend_herdr_classify_agent_status \
    "$(fm_backend_herdr_agent_status_raw "$FM_BACKEND_HERDR_SESSION" "$FM_BACKEND_HERDR_PANE")"
}

# fm_backend_herdr_wait_for_working: poll <session>:<pane_id>'s NATIVE
# agent-state (agent get) up to <polls> times spread evenly across
# <budget-seconds>, returning on stdout the STRONGEST signal observed:
#
#   busy    - a submit-active status was observed at least once. This is
#             confirmation that a real turn started or reached a prompt -
#             the submit landed - independent of
#             whatever the composer's own text happens to show (docs/
#             herdr-backend.md "Incident (2026-07-07)": composer content is
#             what fooled the OLD confirmation on codex's dynamic idle-tip
#             text). Returned the INSTANT it is seen, without waiting out the
#             rest of the budget.
#   idle    - the target was legibly read at least once and never reported
#             "busy" across the whole window - a genuine "not (yet)
#             submitted" signal, not a read failure. The caller retries
#             Enter on this verdict.
#   unknown - EVERY poll in the window failed to read the target at all (a
#             hard I/O failure - pane gone, socket error - not a timing
#             race). The caller must not keep retrying Enter against a target
#             it cannot even read.
#
# <polls> spread across <budget-seconds> (rather than one check at the end)
# is what makes this robust against a SLOW transition: a caller now gets
# several samples across that window instead of a single one, so a transition
# that lands partway through is not missed just because it had not landed by
# the FIRST sample.
# Empirical evidence (docs/herdr-backend.md "Native agent-state submit
# confirmation"): real claude and codex observed first-working at 90-490ms
# after Enter, so a several-hundred-ms budget sampled repeatedly reliably
# catches it. The remaining, inherent gap - a turn so fast it starts AND
# returns to idle between two samples - is bounded by how tightly <polls> is
# packed into <budget-seconds>; nothing observed in real testing has come
# close to that, but it is a residual risk, not a mathematical impossibility
# (see the doc section for the full characterization and the failure-mode
# analysis for both directions this must guard).
# FM_BACKEND_HERDR_SUBMIT_POLLS (default 6): how many samples
# fm_backend_herdr_send_text_submit spreads across each Enter attempt's
# confirmation budget. Overridable for tests (a value of 1
# reproduces the old single-check-at-the-end timing exactly, for byte-for-byte
# call-count assertions).
FM_BACKEND_HERDR_SUBMIT_POLLS=${FM_BACKEND_HERDR_SUBMIT_POLLS:-6}
FM_BACKEND_HERDR_SUBMIT_MIN_SLEEP=${FM_BACKEND_HERDR_SUBMIT_MIN_SLEEP:-0.6}

fm_backend_herdr_submit_confirm_budget() {  # <caller-budget-seconds>
  awk -v b="${1:-0}" -v m="$FM_BACKEND_HERDR_SUBMIT_MIN_SLEEP" 'BEGIN {
    b += 0
    m += 0
    if (b < 0) b = 0
    if (m < 0) m = 0
    if (m > b) b = m
    printf "%.4f", b
  }' 2>/dev/null || printf '%s' "${1:-0}"
}

fm_backend_herdr_wait_for_working() {  # <session> <pane_id> <budget-seconds> <polls>
  local session=$1 pane_id=$2 budget=$3 polls=${4:-1} i interval raw bs saw_idle=0
  case "$polls" in ''|*[!0-9]*|0) polls=1 ;; esac
  interval=$(awk -v b="$budget" -v p="$polls" 'BEGIN { d = p - 1; if (d < 1) d = 1; v = b / d; if (v < 0) v = 0; printf "%.4f", v }' 2>/dev/null)
  case "$interval" in ''|*[!0-9.]*) interval=0 ;; esac
  for ((i = 0; i < polls; i++)); do
    if [ "$polls" -eq 1 ] || [ "$i" -gt 0 ]; then
      sleep "$interval"
    fi
    raw=$(fm_backend_herdr_agent_status_raw "$session" "$pane_id")
    bs=$(fm_backend_herdr_classify_submit_agent_status "$raw")
    case "$bs" in
      busy) printf 'busy'; return 0 ;;
      idle) saw_idle=1 ;;
    esac
  done
  if [ "$saw_idle" -eq 1 ]; then
    printf 'idle'
  else
    printf 'unknown'
  fi
}

# fm_backend_herdr_pane_for_tab: the root pane id for <tab_id> in <workspace_id>
# of <session>, via one pane list call filtered by tab_id (never assumes a
# tab-number/pane-number correspondence - herdr numbers them independently).
fm_backend_herdr_pane_for_tab() {  # <session> <workspace_id> <tab_id>
  local session=$1 wsid=$2 tab_id=$3 panes
  panes=$(fm_backend_herdr_cli "$session" pane list --workspace "$wsid" 2>/dev/null) || return 1
  printf '%s' "$panes" | jq -r --arg tab "$tab_id" \
    '.result.panes[]? | select(.tab_id == $tab) | .pane_id' 2>/dev/null | head -1
}

# fm_backend_herdr_resolve_bare_selector: the live-tab-listing fallback for an
# ad hoc selector with no meta (mirrors tmux's list-windows grep). Searches
# every RUNNING named herdr session (herdr session list) for a tab whose label
# matches <name>, since herdr sessions are not addressed by one ambient
# server the way a single tmux server is. Rare path in practice (herdr tasks
# normally carry meta), best-effort.
fm_backend_herdr_resolve_bare_selector() {  # <name>
  local name=$1 sessions session tabs tab_id wsid pane_id
  sessions=$(herdr session list --json 2>/dev/null | jq -r '.sessions[]? | select(.running == true) | .name' 2>/dev/null)
  while IFS= read -r session; do
    [ -n "$session" ] || continue
    tabs=$(fm_backend_herdr_cli "$session" tab list 2>/dev/null) || continue
    tab_id=$(printf '%s' "$tabs" | jq -r --arg want "$name" \
      '.result.tabs[]? | select(.label == $want) | .tab_id' 2>/dev/null | head -1)
    [ -n "$tab_id" ] || continue
    wsid=$(printf '%s' "$tabs" | jq -r --arg tab "$tab_id" '.result.tabs[]? | select(.tab_id == $tab) | .workspace_id' 2>/dev/null | head -1)
    [ -n "$wsid" ] || continue
    pane_id=$(fm_backend_herdr_pane_for_tab "$session" "$wsid" "$tab_id") || continue
    [ -n "$pane_id" ] || continue
    printf '%s:%s' "$session" "$pane_id"
    return 0
  done <<EOF
$sessions
EOF
  echo "error: no herdr tab named $name in any running session" >&2
  return 1
}

# fm_backend_herdr_list_live: recovery/orphan discovery. Lists every tab whose
# label looks like a firstmate task window (fm-<id>) in <session>'s, THIS
# HOME'S OWN workspace (fm_backend_herdr_workspace_label - never another
# home's), by LABEL - never by trusting a stored pane id, since ids are not
# guaranteed stable across every server lifecycle (see herdr-verification-p2.md
# "ID stability"). A caller running as a given home (e.g. a secondmate
# recovering its own in-flight work) naturally scopes to that home's own
# workspace because FM_HOME already names it - no glue needed, unlike the
# primary-spawns-a-secondmate path in fm-spawn.sh. Read-only: a session/
# workspace that does not exist yet simply lists nothing. One
# "<session>:<pane_id>\t<label>" line per live task tab.
fm_backend_herdr_list_live() {  # <session>
  local session=$1 wsid tabs tab_id label pane_id
  wsid=$(fm_backend_herdr_workspace_find "$session") || return 0
  [ -n "$wsid" ] || return 0
  tabs=$(fm_backend_herdr_cli "$session" tab list --workspace "$wsid" 2>/dev/null) || return 0
  while IFS=$'\t' read -r tab_id label; do
    [ -n "$tab_id" ] || continue
    pane_id=$(fm_backend_herdr_pane_for_tab "$session" "$wsid" "$tab_id") || continue
    [ -n "$pane_id" ] || continue
    printf '%s:%s\t%s\n' "$session" "$pane_id" "$label"
  done < <(printf '%s' "$tabs" | jq -r '.result.tabs[]? | select(.label | startswith("fm-")) | "\(.tab_id)\t\(.label)"' 2>/dev/null)
  # Child-workspace prototype (default OFF): also recover jobs that live in
  # their own child workspaces rather than as tabs in the home workspace.
  if fm_backend_herdr_child_ws_enabled; then
    fm_backend_herdr_list_live_children "$session"
  fi
}

# --- native event push: pane.agent_status_changed subscriber -----------------
#
# The push half of the immediate blocked-state escalation (AGENTS.md section 8,
# docs/herdr-backend.md "Native pane.agent_status_changed push escalation").
# fm_backend_herdr_wait_transition is the watcher's bounded wait primitive for
# herdr homes: instead of a blind sleep, it blocks on herdr's native event
# stream and returns the instant a subscribed pane transitions to `blocked`, so
# a crew waiting on the human wakes its supervisor sub-second instead of after
# the ~240s stale-pane wedge timer. Everything not `blocked` is streamed too
# (the policy, not the subscription, makes `blocked` the sole immediate action)
# so `working` edges clear the per-pane dedupe marker. Polling stays the
# permanent fail-closed backstop: below-capability, a connect/subscribe failure,
# or a missing reader all fall back to the caller sleeping the same budget.

# fm_backend_herdr_socket_path: the control-socket path for <session>, read from
# `herdr session list --json` (the default session's socket differs from a named
# session's - verified: default -> ~/.config/herdr/herdr.sock, named ->
# ~/.config/herdr/sessions/<name>/herdr.sock). Empty on any failure.
fm_backend_herdr_socket_path() {  # <session>
  local session=$1
  herdr session list --json 2>/dev/null \
    | jq -r --arg name "$session" '.sessions[]? | select(.name == $name) | .socket_path // empty' 2>/dev/null \
    | head -1
}

# fm_backend_herdr_events_capable: the version/capability gate for the event
# fast-path (report section 5c trigger 1). Fails closed to the poll loop unless
# ALL hold: herdr+jq present; the raw-socket reader available (python3, unless a
# reader override is configured); client protocol >= FM_BACKEND_HERDR_MIN_EVENTS_PROTOCOL;
# and both `events.subscribe` and `pane.agent_status_changed` present in `herdr
# api schema`. FM_BACKEND_HERDR_EVENTS_FORCE overrides the whole verdict for
# tests (1 = capable, 0 = incapable) without touching the real binary. The
# `api schema` read is ~220KB, so callers (the watcher) memoize this per session
# for a process lifetime rather than probing every poll.
fm_backend_herdr_events_capable() {  # <session>
  local session=$1 protocol schema
  case "${FM_BACKEND_HERDR_EVENTS_FORCE:-}" in
    1) return 0 ;;
    0) return 1 ;;
  esac
  fm_backend_herdr_tool_check || return 1
  if [ -z "${FM_BACKEND_HERDR_EVENT_READER:-}" ]; then
    command -v python3 >/dev/null 2>&1 || return 1
  fi
  protocol=$(herdr status --json 2>/dev/null | jq -r '.client.protocol // empty' 2>/dev/null)
  case "$protocol" in ''|*[!0-9]*) return 1 ;; esac
  [ "$protocol" -ge "$FM_BACKEND_HERDR_MIN_EVENTS_PROTOCOL" ] || return 1
  schema=$(herdr api schema --json 2>/dev/null) || return 1
  printf '%s' "$schema" | grep -Fq 'events.subscribe' || return 1
  printf '%s' "$schema" | grep -Fq 'pane.agent_status_changed' || return 1
  return 0
}

# fm_backend_herdr_normalize_event: THE single normalize point (report section 5
# refinement: one backend transition shape, one parse point). Both the stream
# reader's projected lines AND the level-reconcile's `agent get` reads flow
# through here into the shared normalized-transition record. herdr's event
# carries no previous status and its stream is edge-triggered, so from_status is
# left empty; to_status drives the policy.
fm_backend_herdr_normalize_event() {  # <pane_id> <workspace_id> <agent_status> <agent>
  fm_transition_record "${1:-}" "${2:-}" "" "${3:-}" "${4:-}"
}

# fm_backend_herdr_event_reader_cmd: emit the reader argv (one word per line) for
# the raw-socket subscriber. Default: `python3 <this dir>/herdr-eventwait.py`.
# FM_BACKEND_HERDR_EVENT_READER overrides it with a whitespace-split command so
# tests can substitute a fake reader that replays canned stream lines.
fm_backend_herdr_event_reader_cmd() {
  local word
  if [ -n "${FM_BACKEND_HERDR_EVENT_READER:-}" ]; then
    for word in $FM_BACKEND_HERDR_EVENT_READER; do
      printf '%s\n' "$word"
    done
    return 0
  fi
  printf 'python3\n'
  printf '%s\n' "$FM_BACKEND_HERDR_ROOT/bin/backends/herdr-eventwait.py"
}

# fm_backend_herdr_escalation_marker: the per-pane dedupe marker path for a
# <window> ("<session>:<pane_id>"), keyed identically to the watcher's
# .stale-<key> (tr ':/.' '___'), under <state_dir>.
fm_backend_herdr_escalation_marker() {  # <state_dir> <window>
  local state=$1 window=$2 key
  key=$(printf '%s' "$window" | tr ':/.' '___')
  printf '%s/%s%s' "$state" "$FM_BACKEND_HERDR_ESCALATED_PREFIX" "$key"
}

# fm_backend_herdr_apply_transition: route one normalized record through the
# shared policy table, maintaining the per-pane dedupe marker under <state_dir>.
# On a fresh `actionable` (blocked) edge - policy actionable AND no marker yet -
# it prints the record on stdout and returns 0 (the caller stops and hands the
# record up). The caller commits the marker only after handling the record.
# `absorb` (working) clears the marker and
# returns 1. `defer`/`fallback`, and an already-marked `actionable`, return 1
# with no output. <session> reconstructs the window ("<session>:<pane_id>") for
# the marker key, matching the watcher's own key scheme.
fm_backend_herdr_apply_transition() {  # <state_dir> <session> <record>
  local state=$1 session=$2 record=$3 pane_id to action window marker
  pane_id=$(fm_transition_pane_id "$record")
  [ -n "$pane_id" ] || return 1
  to=$(fm_transition_to_status "$record")
  action=$(fm_transition_policy "$to")
  window="$session:$pane_id"
  marker=$(fm_backend_herdr_escalation_marker "$state" "$window")
  case "$action" in
    actionable)
      if [ ! -e "$marker" ]; then
        printf '%s' "$record"
        return 0
      fi
      ;;
    absorb)
      rm -f "$marker" 2>/dev/null || true
      ;;
  esac
  return 1
}

fm_backend_herdr_commit_transition() {  # <state_dir> <session> <record>
  local state=$1 session=$2 record=$3 pane_id window marker
  pane_id=$(fm_transition_pane_id "$record")
  [ -n "$pane_id" ] || return 1
  window="$session:$pane_id"
  marker=$(fm_backend_herdr_escalation_marker "$state" "$window")
  : > "$marker"
}

fm_backend_herdr_clear_transition() {  # <state_dir> <window>
  local state=$1 window=$2 marker
  [ -n "$window" ] || return 0
  marker=$(fm_backend_herdr_escalation_marker "$state" "$window")
  rm -f "$marker" 2>/dev/null || true
}

# fm_backend_herdr_wait_transition: the bounded event wait. Blocks up to
# <timeout_secs> for one of <pane_window...> ("<session>:<pane_id>") to reach a
# fresh `blocked` edge, then prints the normalized record and returns 0.
# Returns 1 on a clean timeout (the reader ran the full budget, no fresh
# actionable edge - the caller has effectively already slept and just continues)
# and 2 when the event path is unusable (not capable, socket unresolved, reader
# failed to run/subscribe - the caller sleeps the budget itself, the fail-closed
# backstop). See the header block above for the full contract.
fm_backend_herdr_wait_transition() {  # <session> <timeout_secs> <state_dir> <pane_window...>
  local session=$1 timeout=$2 state=$3
  shift 3
  local windows=("$@")
  [ "${#windows[@]}" -gt 0 ] || return 2
  if [ "${FM_BACKEND_EVENTS_CAPABILITY_CONFIRMED:-0}" != 1 ]; then
    fm_backend_herdr_events_capable "$session" || return 2
  fi
  local sock
  sock=$(fm_backend_herdr_socket_path "$session")
  [ -n "$sock" ] || return 2

  # Map each window to its herdr pane id (strip the leading "<session>:").
  local w pane_id
  local pane_ids=()
  for w in "${windows[@]}"; do
    pane_id=${w#*:}
    if [ -z "$pane_id" ] || [ "$pane_id" = "$w" ]; then
      continue
    fi
    pane_ids+=("$pane_id")
  done
  [ "${#pane_ids[@]}" -gt 0 ] || return 2

  # Start the raw-socket reader and wait for its subscription acknowledgement
  # before level reconciliation, so edges occurring during reconciliation are
  # already buffered in the live stream.
  local reader=()
  while IFS= read -r w; do
    reader+=("$w")
  done < <(fm_backend_herdr_event_reader_cmd)
  [ "${#reader[@]}" -gt 0 ] || return 2

  local fifo_dir fifo reader_pid line ws status agent raw record hit rc=1 reader_rc=0
  fifo_dir=$(mktemp -d "${TMPDIR:-/tmp}/fm-herdr-eventwait.XXXXXX") || return 2
  fifo="$fifo_dir/events"
  if ! mkfifo "$fifo" 2>/dev/null; then
    rm -rf "$fifo_dir" 2>/dev/null || true
    return 2
  fi
  "${reader[@]}" "$sock" "$timeout" "${pane_ids[@]}" > "$fifo" 2>/dev/null &
  reader_pid=$!
  if ! exec 9< "$fifo"; then
    kill "$reader_pid" 2>/dev/null || true
    wait "$reader_pid" 2>/dev/null || true
    rm -rf "$fifo_dir" 2>/dev/null || true
    return 2
  fi
  if ! IFS= read -r -u 9 line || [ "$line" != "@subscribed" ]; then
    rc=2
  fi

  # Level reconcile on (re)connect (report section 3d): a pane already `blocked`
  # during the gap since the last subscription is returned now, once, while
  # newer edges accumulate in the active stream. `working` panes clear their
  # marker here too.
  if [ "$rc" -ne 2 ]; then
    for w in "${windows[@]}"; do
      pane_id=${w#*:}
      if [ -z "$pane_id" ] || [ "$pane_id" = "$w" ]; then
        continue
      fi
      raw=$(fm_backend_herdr_agent_status_raw "$session" "$pane_id")
      [ -n "$raw" ] || continue
      record=$(fm_backend_herdr_normalize_event "$pane_id" "" "$raw" "")
      if hit=$(fm_backend_herdr_apply_transition "$state" "$session" "$record"); then
        printf '%s' "$hit"
        rc=0
        break
      fi
    done
  fi

  # Drain stream edges until a fresh blocked edge or the timeout. The reader is
  # a subprocess of this call (NOT a second watcher), and is killed the instant
  # a blocked edge is found.
  # Split each raw projected line (pane_id\tworkspace_id\tagent_status\tagent)
  # with `cut`, NOT `IFS=$'\t' read`: a tab is IFS-whitespace, so `read` would
  # collapse an empty middle field (e.g. an absent workspace_id) and shift the
  # status into the wrong column. `cut` preserves empty fields.
  while [ "$rc" -eq 1 ] && IFS= read -r line <&9; do
    [ -n "$line" ] || continue
    pane_id=$(printf '%s' "$line" | cut -f1)
    ws=$(printf '%s' "$line" | cut -f2)
    status=$(printf '%s' "$line" | cut -f3)
    agent=$(printf '%s' "$line" | cut -f4)
    [ -n "$pane_id" ] || continue
    record=$(fm_backend_herdr_normalize_event "$pane_id" "$ws" "$status" "$agent")
    if hit=$(fm_backend_herdr_apply_transition "$state" "$session" "$record"); then
      printf '%s' "$hit"
      rc=0
      break
    fi
  done
  if [ "$rc" -eq 0 ]; then
    kill "$reader_pid" 2>/dev/null || true
  fi
  if [ "$rc" -eq 2 ]; then
    kill "$reader_pid" 2>/dev/null || true
  fi
  # No actionable edge: distinguish a clean full-budget wait (reader exit 0 ->
  # return 1, caller already waited) from a reader error (connect/subscribe
  # failure, exit non-zero -> return 2, caller sleeps and counts toward the
  # runtime-disable threshold).
  wait "$reader_pid" 2>/dev/null || reader_rc=$?
  exec 9<&-
  rm -rf "$fifo_dir" 2>/dev/null || true
  [ "$rc" -eq 0 ] && return 0
  [ "$rc" -eq 2 ] && return 2
  [ "$reader_rc" -eq 0 ] && return 1
  return 2
}
