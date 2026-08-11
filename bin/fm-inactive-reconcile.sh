#!/usr/bin/env bash
# fm-inactive-reconcile.sh - bounded reconciliation of suspicious inactive terminal outcomes.
#
# Usage:
#   fm-inactive-reconcile.sh scan [--startup]
#   fm-inactive-reconcile.sh acknowledge <fingerprint>
#
# This is an adjunct to the existing watcher poll loop and session-start path,
# not a watcher, daemon, PR poll, or forge client of its own.
# `scan` evaluates at most once per FM_INACTIVE_RECONCILE_SECS (default 900,
# valid 60..1800) per home, except that --startup performs the same cheap gate
# during a locked session start.
#
# It considers only a direct ordinary crewmate whose durable activity is older
# than that interval and whose last status is not captain-held.
# It then uses fm-crew-state.sh as the sole current-state source.
# Only a done or failed state is suspicious enough to create a durable terminal
# outcome record or wake the supervisor.
# Working, paused, parked, blocked, unknown, persistent secondmates, and
# captain-held work retain their existing supervision semantics.
#
# A terminal-outcomes/<fingerprint>.pending record remains until its upstream
# receipt is durable.
# In a secondmate home, that receipt is an idempotent parent-channel status
# append.
# In a main home, a presentation-stage record is acknowledged by fm-wake-drain
# only after its corresponding inactive-outcome wake is handled.
# A receipt is intentionally independent of .hb-surfaced-* bookkeeping.
#
# Main homes also inspect an inactive direct secondmate's validated structured
# home summary.
# A terminal child missing the parent report creates one marked correlated
# request through fm-send and a durable reconciliation obligation.
# The existing pending-reply owner handles its repost and escalation lifecycle.
# Remote summaries use the established fm-on route, never a forge endpoint.
#
# The scan never invokes gh, gh-axi, curl, fm-pr-check.sh, fm-pr-poll.sh, or a
# state *.check.sh.
# It reads only durable local state, fm-crew-state.sh, and a validated
# secondmate-home summary.
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FM_ROOT="${FM_ROOT_OVERRIDE:-$(cd "$SCRIPT_DIR/.." && pwd)}"
FM_HOME="${FM_HOME:-${FM_ROOT_OVERRIDE:-$FM_ROOT}}"
STATE="${FM_STATE_OVERRIDE:-$FM_HOME/state}"
OUTCOME_DIR="$STATE/terminal-outcomes"
SCAN_MARKER="$STATE/.inactive-outcome-reconcile"
SCAN_LOCK="$STATE/.inactive-outcome-reconcile.lock"
CREW_STATE_BIN="${FM_INACTIVE_CREW_STATE_BIN:-$SCRIPT_DIR/fm-crew-state.sh}"
SUMMARY_BIN="${FM_INACTIVE_RECONCILE_SUMMARY_BIN:-$SCRIPT_DIR/fm-fleet-snapshot.sh}"
ON_BIN="${FM_INACTIVE_RECONCILE_ON_BIN:-$SCRIPT_DIR/fm-on.sh}"
SEND_BIN="${FM_INACTIVE_RECONCILE_SEND_BIN:-$SCRIPT_DIR/fm-send.sh}"

# shellcheck source=bin/fm-timeout-lib.sh
. "$SCRIPT_DIR/fm-timeout-lib.sh"
# shellcheck source=bin/fm-wake-lib.sh
. "$SCRIPT_DIR/fm-wake-lib.sh"
# shellcheck source=bin/fm-classify-lib.sh
. "$SCRIPT_DIR/fm-classify-lib.sh"
# shellcheck source=bin/fm-secondmate-parent-lib.sh
. "$SCRIPT_DIR/fm-secondmate-parent-lib.sh"
# shellcheck source=bin/fm-ff-lib.sh
. "$SCRIPT_DIR/fm-ff-lib.sh"
# shellcheck source=bin/fm-pending-reply-lib.sh
. "$SCRIPT_DIR/fm-pending-reply-lib.sh"
# shellcheck source=bin/fm-episode-id-lib.sh
. "$SCRIPT_DIR/fm-episode-id-lib.sh"

FM_INACTIVE_RECONCILE_SECS=${FM_INACTIVE_RECONCILE_SECS:-900}
FM_SNAPSHOT_SECONDMATE_TIMEOUT=${FM_SNAPSHOT_SECONDMATE_TIMEOUT:-8}
FM_SNAPSHOT_SECONDMATE_MAX_BYTES=${FM_SNAPSHOT_SECONDMATE_MAX_BYTES:-262144}
FM_SNAPSHOT_SECONDMATE_MIN_BYTES=4096
case "$FM_INACTIVE_RECONCILE_SECS" in
  ''|*[!0-9]*|0)
    printf 'fm-inactive-reconcile: FM_INACTIVE_RECONCILE_SECS must be a whole number from 60 to 1800\n' >&2
    exit 2
    ;;
esac
if [ "$FM_INACTIVE_RECONCILE_SECS" -lt 60 ] || [ "$FM_INACTIVE_RECONCILE_SECS" -gt 1800 ]; then
  printf 'fm-inactive-reconcile: FM_INACTIVE_RECONCILE_SECS must be a whole number from 60 to 1800\n' >&2
  exit 2
fi
case "$FM_SNAPSHOT_SECONDMATE_TIMEOUT" in
  ''|*[!0-9]*|0) FM_SNAPSHOT_SECONDMATE_TIMEOUT=8 ;;
esac
case "$FM_SNAPSHOT_SECONDMATE_MAX_BYTES" in
  ''|*[!0-9]*|0)
    printf 'fm-inactive-reconcile: FM_SNAPSHOT_SECONDMATE_MAX_BYTES must be an integer of at least %s\n' "$FM_SNAPSHOT_SECONDMATE_MIN_BYTES" >&2
    exit 2
    ;;
esac
if [ "$FM_SNAPSHOT_SECONDMATE_MAX_BYTES" -lt "$FM_SNAPSHOT_SECONDMATE_MIN_BYTES" ]; then
  printf 'fm-inactive-reconcile: FM_SNAPSHOT_SECONDMATE_MAX_BYTES must be an integer of at least %s\n' "$FM_SNAPSHOT_SECONDMATE_MIN_BYTES" >&2
  exit 2
fi

if [ "$(uname)" = Darwin ]; then
  file_mtime() { stat -f %m "$1" 2>/dev/null; }
else
  file_mtime() { stat -c %Y "$1" 2>/dev/null; }
fi

reconcile_now() {
  case "${FM_INACTIVE_RECONCILE_NOW:-}" in
    ''|*[!0-9]*) date +%s ;;
    *) printf '%s\n' "$FM_INACTIVE_RECONCILE_NOW" ;;
  esac
}

clean_field() {
  printf '%s' "$1" | LC_ALL=C tr '\t\r\n' '   ' | cut -c1-1200
}

valid_id() {
  case "$1" in ''|*[!A-Za-z0-9._-]*) return 1 ;; esac
  return 0
}

sha256_text() {
  if command -v shasum >/dev/null 2>&1; then
    printf '%s' "$1" | shasum -a 256 | awk '{print substr($1, 1, 32)}'
  elif command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | awk '{print substr($1, 1, 32)}'
  else
    printf '%s' "$1" | cksum | awk '{printf "%08x%08x", $1, $2}'
  fi
}

record_path() { printf '%s/%s.%s\n' "$OUTCOME_DIR" "$1" "$2"; }

record_value() {
  local record=$1 key=$2
  [ -f "$record" ] && [ ! -L "$record" ] || return 0
  grep "^${key}=" "$record" 2>/dev/null | tail -1 | cut -d= -f2- || true
}

record_phase_set() {
  local record=$1 phase=$2 tmp line
  [ -f "$record" ] && [ ! -L "$record" ] || return 1
  tmp=$(mktemp "$OUTCOME_DIR/.record.XXXXXX") || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in phase=*) continue ;; esac
    printf '%s\n' "$line" >> "$tmp" || { rm -f "$tmp"; return 1; }
  done < "$record"
  printf 'phase=%s\n' "$phase" >> "$tmp" || { rm -f "$tmp"; return 1; }
  chmod 600 "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$record"
}

record_field_set() {
  local record=$1 key=$2 value=$3 tmp line
  [ -f "$record" ] && [ ! -L "$record" ] || return 1
  tmp=$(mktemp "$OUTCOME_DIR/.record.XXXXXX") || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in "${key}="*) continue ;; esac
    printf '%s\n' "$line" >> "$tmp" || { rm -f "$tmp"; return 1; }
  done < "$record"
  printf '%s=%s\n' "$key" "$value" >> "$tmp" || { rm -f "$tmp"; return 1; }
  chmod 600 "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$record"
}

ensure_record() { # <fingerprint> <task> <state> <outcome-key> <origin> <phase> <pr>
  local fingerprint=$1 task=$2 state=$3 outcome_key=$4 origin=$5 phase=$6 pr=$7 tmp
  RECORD_PENDING=$(record_path "$fingerprint" pending)
  RECORD_PRESENTED=$(record_path "$fingerprint" presented)
  RECORD_REPORTED=$(record_path "$fingerprint" reported)
  if [ -f "$RECORD_PRESENTED" ] || [ -f "$RECORD_REPORTED" ]; then
    RECORD_PENDING=
    return 0
  fi
  if [ -f "$RECORD_PENDING" ] && [ ! -L "$RECORD_PENDING" ]; then
    return 0
  fi
  mkdir -p "$OUTCOME_DIR" || return 1
  [ ! -L "$OUTCOME_DIR" ] || return 1
  tmp=$(mktemp "$OUTCOME_DIR/.pending.XXXXXX") || return 1
  {
    printf 'schema=fm-terminal-outcome.v1\n'
    printf 'fingerprint=%s\n' "$fingerprint"
    printf 'task_id=%s\n' "$task"
    printf 'state=%s\n' "$state"
    printf 'outcome_key=%s\n' "$outcome_key"
    printf 'origin=%s\n' "$origin"
    printf 'phase=%s\n' "$phase"
    printf 'pr=%s\n' "$pr"
    printf 'created_epoch=%s\n' "$(reconcile_now)"
    printf 'request_attempted=0\n'
    printf 'notice_emitted=0\n'
  } > "$tmp" || { rm -f "$tmp"; return 1; }
  chmod 600 "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$RECORD_PENDING" || { rm -f "$tmp"; return 1; }
}

mark_reported() { # <record>
  local record=$1 reported
  [ -f "$record" ] && [ ! -L "$record" ] || return 1
  reported=${record%.pending}.reported
  mv -f "$record" "$reported"
}

queue_key_exists() { # <key>
  local key=$1 queued
  queued=$(fm_wake_queued_keys check 2>/dev/null || true)
  printf '%s\n' "$queued" | grep -Fx -- "$key" >/dev/null 2>&1
}

queue_notice_once() { # <record> <key> <payload>
  local record=$1 key=$2 payload=$3
  if queue_key_exists "$key"; then
    return 1
  fi
  fm_wake_append check "$key" "$payload" || return 2
  record_field_set "$record" notice_emitted 1 || return 2
  printf 'actionable: %s\n' "$payload"
  return 0
}

queue_presentation() { # <record> <fingerprint> <payload>
  local record=$1 fingerprint=$2 payload=$3 key
  key="inactive-outcome:$fingerprint"
  if queue_key_exists "$key"; then
    return 1
  fi
  fm_wake_append check "$key" "$payload" || return 2
  printf 'actionable: %s\n' "$payload"
  return 0
}

last_activity_age() { # <meta> <status> <turn-ended>
  local meta=$1 status=$2 turn=$3 now m newest=0 file
  now=$(reconcile_now)
  for file in "$meta" "$status" "$turn"; do
    [ -e "$file" ] || continue
    m=$(file_mtime "$file" 2>/dev/null || true)
    case "$m" in ''|*[!0-9]*) continue ;; esac
    [ "$m" -le "$newest" ] || newest=$m
  done
  [ "$newest" -gt 0 ] || { printf '0\n'; return; }
  if [ "$now" -lt "$newest" ]; then printf '0\n'; else printf '%s\n' $((now - newest)); fi
}

scan_marker_age() {
  local now m
  [ -e "$SCAN_MARKER" ] || { printf '999999\n'; return; }
  now=$(reconcile_now)
  m=$(file_mtime "$SCAN_MARKER" 2>/dev/null || true)
  case "$m" in ''|*[!0-9]*) printf '999999\n'; return ;; esac
  if [ "$now" -lt "$m" ]; then printf '0\n'; else printf '%s\n' $((now - m)); fi
}

meta_field() {
  grep "^$2=" "$1" 2>/dev/null | tail -1 | cut -d= -f2- || true
}

pr_for_task() { # <meta> <status>
  local pr=$1 status=$2 value
  value=$(meta_field "$pr" pr)
  if [ -z "$value" ] && [ -f "$status" ]; then
    value=$(grep -Eo 'https?://[^[:space:])"]+/pull/[0-9]+' "$status" 2>/dev/null | head -1 || true)
  fi
  clean_field "$value"
}

home_secondmate_id() {
  local marker="$FM_HOME/.fm-secondmate-home" id
  [ -f "$marker" ] && [ ! -L "$marker" ] || return 1
  id=$(head -1 "$marker" 2>/dev/null || true)
  valid_id "$id" || return 1
  printf '%s\n' "$id"
}

append_once() { # <path> <line>
  local path=$1 line=$2
  [ ! -L "$path" ] || return 1
  mkdir -p "$(dirname "$path")" || return 1
  if grep -Fqx -- "$line" "$path" 2>/dev/null; then
    return 0
  fi
  printf '%s\n' "$line" >> "$path"
}

report_to_parent() { # <self-id> <task> <state> <outcome-key> <fingerprint> <pr>
  local self=$1 task=$2 state=$3 outcome_key=$4 fingerprint=$5 pr=$6 parent_record destination line
  parent_record="$FM_HOME/.fm-secondmate-parent"
  fm_secondmate_parent_record_parse "$parent_record" || return 1
  case "$FM_SECONDMATE_PARENT_ROUTE" in
    local)
      [ -n "$FM_SECONDMATE_PARENT_HOME" ] || return 1
      destination="$FM_SECONDMATE_PARENT_HOME/state/$self.status"
      ;;
    remote)
      destination="$STATE/parent-replies.status"
      ;;
    *) return 1 ;;
  esac
  line="$state [key=$outcome_key]: inactive terminal child=$task fingerprint=$fingerprint"
  [ -z "$pr" ] || line="$line pr=$pr"
  append_once "$destination" "$line"
}

terminal_outcome_key() { # <home-id> <child-id> <state> <episode-id>
  local home_id=$1 child=$2 state=$3 episode=${4:-}
  printf 'inactive-outcome-%s-%s-%s' "$home_id" "$child" "$state"
  [ -z "$episode" ] || printf -- '-%s' "$episode"
}

parent_has_outcome_report() { # <secondmate-id> <outcome-key>
  local mate=$1 key=$2 status
  status="$STATE/$mate.status"
  [ -f "$status" ] || return 1
  grep -Eq "^(done|failed) \[key=${key//./\\.}\]:" "$status" 2>/dev/null
}

run_bounded_summary() { # <command> [args...]
  local tmp rc bytes limit
  limit=$((FM_SNAPSHOT_SECONDMATE_MAX_BYTES + 1))
  tmp=$(mktemp "$OUTCOME_DIR/.summary.XXXXXX") || return 1
  fm_run_timed "$FM_SNAPSHOT_SECONDMATE_TIMEOUT" bash -o pipefail -c '
    limit=$1
    shift
    "$@" | LC_ALL=C head -c "$limit"
    status=("${PIPESTATUS[@]}")
    [ "${status[1]}" -eq 0 ] || exit "${status[1]}"
    case "${status[0]}" in 0|141) exit 0 ;; *) exit "${status[0]}" ;; esac
  ' _ "$limit" "$@" < /dev/null > "$tmp" 2>/dev/null
  rc=$?
  if [ "$rc" -ne 0 ]; then
    rm -f "$tmp"
    return "$rc"
  fi
  bytes=$(LC_ALL=C wc -c < "$tmp" | tr -d ' ')
  if [ "$bytes" -gt "$FM_SNAPSHOT_SECONDMATE_MAX_BYTES" ]; then
    rm -f "$tmp"
    return 1
  fi
  cat "$tmp"
  rc=$?
  rm -f "$tmp"
  return "$rc"
}

summary_for_secondmate() { # <id> <meta> <terminal-after>
  local id=$1 meta=$2 terminal_after=$3 home remote_host
  local -a summary_args
  home=$(meta_field "$meta" home)
  remote_host=$(meta_field "$meta" remote_host)
  summary_args=(--secondmate-home-summary --summary-max-bytes "$FM_SNAPSHOT_SECONDMATE_MAX_BYTES")
  [ -z "$terminal_after" ] || summary_args+=(--terminal-after "$terminal_after")
  SUMMARY_EXPECTED_HOME=$home
  if [ -n "$remote_host" ]; then
    run_bounded_summary "$ON_BIN" "$id" fm-fleet-snapshot.sh "${summary_args[@]}"
    return
  fi
  validate_secondmate_home "$id" "$home" 2>/dev/null || return 1
  SUMMARY_EXPECTED_HOME=$VALIDATED_HOME
  run_bounded_summary env \
    FM_ROOT_OVERRIDE="$FM_ROOT" \
    FM_HOME="$VALIDATED_HOME" \
    FM_STATE_OVERRIDE="$VALIDATED_HOME/state" \
    FM_DATA_OVERRIDE="$VALIDATED_HOME/data" \
    FM_CONFIG_OVERRIDE="$VALIDATED_HOME/config" \
    FM_PROJECTS_OVERRIDE="$VALIDATED_HOME/projects" \
    "$SUMMARY_BIN" "${summary_args[@]}"
}

summary_generated_is_fresh() { # <generated>
  local generated=$1 epoch now age
  epoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$generated" +%s 2>/dev/null \
    || date -u -d "$generated" +%s 2>/dev/null \
    || true)
  case "$epoch" in ''|*[!0-9]*) return 1 ;; esac
  now=$(reconcile_now)
  age=$((now - epoch))
  [ "$age" -ge -60 ] && [ "$age" -le "$FM_INACTIVE_RECONCILE_SECS" ]
}

summary_is_authoritative() { # <summary> <expected-home> <terminal-after>
  local summary=$1 expected_home=$2 terminal_after=$3 generated
  SUMMARY_VALIDATION_REASON=shape
  printf '%s' "$summary" | jq -e --arg home "$expected_home" --arg after "$terminal_after" '
    .schema == "fm-secondmate-home-summary.v1"
    and .home == $home
    and (.generated | type) == "string"
    and (.valid | type) == "boolean"
    and (.state | type) == "string"
    and (.invalidity | type) == "object"
    and (.invalidity.kind == null or (.invalidity.kind | type) == "string")
    and (.invalidity.ids | type) == "array"
    and all(.invalidity.ids[]; type == "string" and test("^[A-Za-z0-9._-]+$"))
    and (if .valid then
      .reason == null
      and .invalidity.kind == null
      and (.invalidity.ids | length) == 0
      and (.state == "captain_decision" or .state == "active_child_work"
        or .state == "externally_held" or .state == "no_active_work")
    else
      (.reason | type) == "string" and (.reason | length) > 0
      and (.invalidity.kind | type) == "string" and (.invalidity.kind | length) > 0
      and .state == "unknown"
    end)
    and (.active_children | type) == "array"
    and (.decisions_open | type) == "array"
    and (.holds | type) == "array"
    and (.queued | type) == "array"
    and (.landed | type) == "array"
    and (.endpoints | type) == "array"
    and (.counts | type) == "object"
    and (.omitted | type) == "array"
    and (has("terminal_children") == has("terminal_page"))
    and (if has("terminal_children") then
      (.terminal_children | type) == "array"
      and all(.terminal_children[];
        (type == "object")
        and (.id | type) == "string" and (.id | test("^[A-Za-z0-9._-]+$"))
        and (.state == "done" or .state == "failed")
        and ((.episode_id // null) == null or
          ((.episode_id | type) == "string" and (.episode_id | test("^([0-9a-f]{16}|[0-9a-f]{24})$")))))
      and ([.terminal_children[].id] == ([.terminal_children[].id] | sort | unique))
      and (if .valid then (.terminal_children | length) == 0 else true end)
      and (.terminal_page | type) == "object"
      and .terminal_page.after == (if $after == "" then null else $after end)
      and (.terminal_page.has_more | type) == "boolean"
      and (.terminal_page.count | type) == "number"
      and (.terminal_page.remaining | type) == "number"
      and (.terminal_page.total | type) == "number"
      and all([.terminal_page.count, .terminal_page.remaining, .terminal_page.total][];
        floor == . and . >= 0)
      and .terminal_page.count == (.terminal_children | length)
      and .terminal_page.count <= .terminal_page.remaining
      and .terminal_page.remaining <= .terminal_page.total
      and (if $after == "" then .terminal_page.remaining == .terminal_page.total else true end)
      and all(.terminal_children[]; $after == "" or .id > $after)
      and .terminal_page.has_more == (.terminal_page.remaining > .terminal_page.count)
      and (if .terminal_page.has_more then
        .terminal_page.count > 0
        and (.terminal_page.next_after | type) == "string"
        and (.terminal_page.next_after | test("^[A-Za-z0-9._-]+$"))
        and .terminal_page.next_after == .terminal_children[-1].id
      else .terminal_page.next_after == null end)
      and (if .valid then .terminal_page.total == 0 else true end)
    else true end)
  ' >/dev/null 2>&1 || return 1
  generated=$(printf '%s' "$summary" | jq -r '.generated')
  SUMMARY_VALIDATION_REASON=stale
  summary_generated_is_fresh "$generated" || return 1
  SUMMARY_VALIDATION_REASON=
}

summary_obligation() { # <secondmate-id> <reason>
  local mate=$1 reason=$2 fingerprint payload
  fingerprint=$(sha256_text "secondmate-summary|$mate")
  ensure_record "$fingerprint" "$mate" unknown "inactive-summary-$mate" "secondmate-summary:$mate" upstream "" || return 1
  [ -n "$RECORD_PENDING" ] || return 0
  payload="inactive secondmate summary needs reconciliation: secondmate=$mate reason=$(clean_field "$reason")"
  queue_notice_once "$RECORD_PENDING" "inactive-reconcile:$fingerprint" "$payload" || true
}

clear_summary_obligation() { # <secondmate-id>
  local mate=$1 fingerprint record
  fingerprint=$(sha256_text "secondmate-summary|$mate")
  for record in "$(record_path "$fingerprint" pending)" "$(record_path "$fingerprint" reported)"; do
    [ ! -f "$record" ] || rm -f "$record" || return 1
  done
}

request_secondmate_report() { # <record> <secondmate-id> <child-id> <state> <outcome-key>
  local record=$1 mate=$2 child=$3 state=$4 outcome_key=$5 msg corr rc=0 owner_key
  msg="INACTIVE OUTCOME RECONCILIATION: child $child is authoritatively $state but has no parent report. Append a terminal parent status report with [key=$outcome_key]."
  owner_key="inactive-terminal-outcome:$(record_value "$record" fingerprint)"
  corr=$(record_value "$record" correlation)
  if [ -z "$corr" ]; then
    corr=$(fm_pending_reply_find_owned "$STATE" "$mate" "$owner_key" 2>/dev/null || true)
  fi
  if [ -z "$corr" ]; then
    corr=$(fm_pending_reply_create "$FM_HOME" "$STATE" "$mate" "$msg" "$owner_key" 2>/dev/null || true)
    [ -n "$corr" ] || return 2
  fi
  if [ "$(record_value "$record" correlation)" != "$corr" ]; then
    record_field_set "$record" correlation "$corr" || return 2
  fi
  fm_pending_reply_corr_reusable "$STATE" "$corr" "$mate" || return 2
  [ "$(record_value "$record" request_attempted)" != 1 ] || return 1
  fm_pending_reply_prepare_delivery "$STATE" "$corr" || return 2
  msg="$msg Include corr=$corr in that report."
  fm_pending_reply_embed_corr "$msg" "$corr" msg || return 2
  record_field_set "$record" request_attempted 1 || return 2
  FM_PENDING_REPLY_EXISTING_CORR="$corr" "$SEND_BIN" "$mate" "$msg" >/dev/null 2>&1 || rc=$?
  record_field_set "$record" request_result "$rc" || true
  if [ "$rc" -ne 0 ]; then
    fm_pending_reply_mark_delivery_unknown "$STATE" "$corr" || true
  fi
  return "$rc"
}

recover_secondmate_request_link() { # <record> <secondmate-id>
  local record=$1 mate=$2 corr owner_key
  [ -z "$(record_value "$record" correlation)" ] || return 0
  owner_key="inactive-terminal-outcome:$(record_value "$record" fingerprint)"
  corr=$(fm_pending_reply_find_owned "$STATE" "$mate" "$owner_key" 2>/dev/null || true)
  [ -n "$corr" ] || return 0
  record_field_set "$record" correlation "$corr"
}

settle_secondmate_request_if_reported() { # <record> <secondmate-id> <fingerprint>
  local record=$1 mate=$2 fingerprint=$3 corr line
  corr=$(record_value "$record" correlation)
  case "$corr" in [A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9][A-Fa-f0-9]) ;; *) return 0 ;; esac
  fm_pending_reply_prepare_delivery "$STATE" "$corr" || return 1
  line="resolved [key=inactive-outcome-receipt-$fingerprint]: matching terminal parent report received corr=$corr"
  append_once "$STATE/$mate.status" "$line" || return 1
  fm_pending_reply_try_resolve "$STATE" "$corr" "$STATE/$mate.status" >/dev/null 2>&1
}

reconcile_direct_child() { # <id> <meta> <secondmate-id-or-empty>
  local id=$1 meta=$2 self=${3:-} status turn last age state_line state pr episode fingerprint outcome_key payload
  status="$STATE/$id.status"
  turn="$STATE/$id.turn-ended"
  last=$(last_status_line "$status")
  status_line_verb "$last" | grep -Fx captain-held >/dev/null 2>&1 && return 0
  age=$(last_activity_age "$meta" "$status" "$turn")
  [ "$age" -ge "$FM_INACTIVE_RECONCILE_SECS" ] || return 0
  state_line=$(FM_HOME="$FM_HOME" FM_STATE_OVERRIDE="$STATE" "$CREW_STATE_BIN" "$id" 2>/dev/null || true)
  case "$state_line" in
    'state: done '*) state='done' ;;
    'state: failed '*) state='failed' ;;
    *) return 0 ;;
  esac
  pr=$(pr_for_task "$meta" "$status")
  episode=$(fm_episode_id_normalize "$(meta_field "$meta" episode_id)" 2>/dev/null || true)
  if [ -n "$self" ]; then
    outcome_key=$(terminal_outcome_key "$self" "$id" "$state" "$episode")
  else
    outcome_key=$(terminal_outcome_key main "$id" "$state" "$episode")
  fi
  fingerprint=$(sha256_text "direct|$outcome_key")
  ensure_record "$fingerprint" "$id" "$state" "$outcome_key" direct "upstream" "$pr" || return 1
  [ -n "$RECORD_PENDING" ] || return 0
  if [ -n "$self" ]; then
    if report_to_parent "$self" "$id" "$state" "$outcome_key" "$fingerprint" "$pr"; then
      mark_reported "$RECORD_PENDING" || return 1
    else
      payload="inactive terminal outcome needs parent report: child=$id state=$state"
      queue_notice_once "$RECORD_PENDING" "inactive-reconcile:$fingerprint" "$payload" || true
    fi
    return 0
  fi
  record_phase_set "$RECORD_PENDING" presentation || return 1
  payload="inactive terminal outcome awaiting captain presentation: child=$id state=$state"
  [ -z "$pr" ] || payload="$payload pr=$pr"
  queue_presentation "$RECORD_PENDING" "$fingerprint" "$payload" || true
}

reconcile_secondmate_record() { # <record> <secondmate-id> <child-id> <state> <outcome-key> <fingerprint>
  local record=$1 mate=$2 child=$3 state=$4 outcome_key=$5 fingerprint=$6 payload
  recover_secondmate_request_link "$record" "$mate" || return 1
  if parent_has_outcome_report "$mate" "$outcome_key"; then
    settle_secondmate_request_if_reported "$record" "$mate" "$fingerprint" || return 1
    record_phase_set "$record" presentation || return 1
    payload="inactive terminal outcome awaiting captain presentation: secondmate=$mate child=$child state=$state"
    queue_presentation "$record" "$fingerprint" "$payload" || true
    return 0
  fi
  request_secondmate_report "$record" "$mate" "$child" "$state" "$outcome_key" || true
  payload="inactive terminal outcome missing parent report: secondmate=$mate child=$child state=$state"
  queue_notice_once "$record" "inactive-reconcile:$fingerprint" "$payload" || true
}

reconcile_secondmate_child() { # <secondmate-id> <child-id> <state> <episode-id>
  local mate=$1 child=$2 state=$3 episode=${4:-} fingerprint outcome_key
  valid_id "$mate" && valid_id "$child" || return 0
  case "$state" in done|failed) ;; *) return 0 ;; esac
  episode=$(fm_episode_id_normalize "$episode" 2>/dev/null || true)
  outcome_key=$(terminal_outcome_key "$mate" "$child" "$state" "$episode")
  fingerprint=$(sha256_text "secondmate|$outcome_key")
  ensure_record "$fingerprint" "$child" "$state" "$outcome_key" "secondmate:$mate" upstream "" || return 1
  [ -n "$RECORD_PENDING" ] || return 0
  reconcile_secondmate_record "$RECORD_PENDING" "$mate" "$child" "$state" "$outcome_key" "$fingerprint"
}

replay_pending_outcomes() { # <secondmate-id-or-empty>
  local self=${1:-} record schema fingerprint task state outcome_key origin pr mate payload
  for record in "$OUTCOME_DIR"/*.pending; do
    [ -f "$record" ] && [ ! -L "$record" ] || continue
    schema=$(record_value "$record" schema)
    [ "$schema" = fm-terminal-outcome.v1 ] || return 1
    fingerprint=$(record_value "$record" fingerprint)
    task=$(record_value "$record" task_id)
    state=$(record_value "$record" state)
    outcome_key=$(record_value "$record" outcome_key)
    origin=$(record_value "$record" origin)
    pr=$(record_value "$record" pr)
    case "$fingerprint" in ''|*[!A-Fa-f0-9]*) return 1 ;; esac
    valid_id "$task" || return 1
    case "$state" in done|failed) ;; unknown) [ "$origin" = "secondmate-summary:$task" ] || return 1 ;; *) return 1 ;; esac
    case "$origin" in
      direct)
        if [ -n "$self" ]; then
          if report_to_parent "$self" "$task" "$state" "$outcome_key" "$fingerprint" "$pr"; then
            mark_reported "$record" || return 1
          else
            payload="inactive terminal outcome needs parent report: child=$task state=$state"
            queue_notice_once "$record" "inactive-reconcile:$fingerprint" "$payload" || true
          fi
        elif [ -e "$FM_HOME/.fm-secondmate-home" ]; then
          return 1
        else
          record_phase_set "$record" presentation || return 1
          payload="inactive terminal outcome awaiting captain presentation: child=$task state=$state"
          [ -z "$pr" ] || payload="$payload pr=$pr"
          queue_presentation "$record" "$fingerprint" "$payload" || true
        fi
        ;;
      secondmate:*)
        mate=${origin#secondmate:}
        valid_id "$mate" || return 1
        reconcile_secondmate_record "$record" "$mate" "$task" "$state" "$outcome_key" "$fingerprint" || return 1
        ;;
      secondmate-summary:*) ;;
      *) return 1 ;;
    esac
  done
}

summary_cursor_get() { # <secondmate-id>
  local path="$OUTCOME_DIR/.summary-cursor-$1" cursor
  [ -f "$path" ] && [ ! -L "$path" ] || return 0
  cursor=$(head -1 "$path" 2>/dev/null || true)
  valid_id "$cursor" && printf '%s\n' "$cursor"
}

summary_cursor_set() { # <secondmate-id> <cursor-or-empty>
  local mate=$1 cursor=$2 path tmp
  path="$OUTCOME_DIR/.summary-cursor-$mate"
  [ ! -e "$path" ] || { [ -f "$path" ] && [ ! -L "$path" ]; } || return 1
  if [ -z "$cursor" ]; then
    rm -f "$path"
    return
  fi
  valid_id "$cursor" || return 1
  tmp=$(mktemp "$OUTCOME_DIR/.cursor.XXXXXX") || return 1
  printf '%s\n' "$cursor" > "$tmp" || { rm -f "$tmp"; return 1; }
  chmod 600 "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$path"
}

scan_secondmates() {
  local meta mate kind status last age summary child state episode expected_home remote_host terminal_after next_after
  for meta in "$STATE"/*.meta; do
    [ -f "$meta" ] || continue
    kind=$(meta_field "$meta" kind)
    [ "$kind" = secondmate ] || continue
    mate=$(basename "$meta" .meta)
    valid_id "$mate" || continue
    status="$STATE/$mate.status"
    last=$(last_status_line "$status")
    status_line_verb "$last" | grep -Fx captain-held >/dev/null 2>&1 && continue
    age=$(last_activity_age "$meta" "$status" "$STATE/$mate.turn-ended")
    [ "$age" -ge "$FM_INACTIVE_RECONCILE_SECS" ] || continue
    expected_home=$(meta_field "$meta" home)
    remote_host=$(meta_field "$meta" remote_host)
    if [ -n "$remote_host" ] && [ "${SCAN_STARTUP:-0}" = 1 ]; then
      continue
    fi
    if [ -z "$remote_host" ]; then
      if ! validate_secondmate_home "$mate" "$expected_home" 2>/dev/null; then
        summary_obligation "$mate" invalid-home
        continue
      fi
      expected_home=$VALIDATED_HOME
    fi
    terminal_after=$(summary_cursor_get "$mate" || true)
    if ! summary=$(summary_for_secondmate "$mate" "$meta" "$terminal_after"); then
      summary_obligation "$mate" unavailable
      continue
    fi
    SUMMARY_VALIDATION_REASON=invalid
    if ! summary_is_authoritative "$summary" "$expected_home" "$terminal_after"; then
      summary_obligation "$mate" "$SUMMARY_VALIDATION_REASON"
      continue
    fi
    while IFS=$(printf '\t') read -r child state episode; do
      [ -n "$child" ] || continue
      if ! reconcile_secondmate_child "$mate" "$child" "$state" "$episode"; then
        summary_obligation "$mate" child-persist || true
        return 1
      fi
    done < <(printf '%s' "$summary" | jq -r '.terminal_children[]? | [.id, .state, (.episode_id // "")] | @tsv')
    next_after=$(printf '%s' "$summary" | jq -r 'if (.terminal_page.has_more // false) then .terminal_page.next_after else "" end')
    if ! summary_cursor_set "$mate" "$next_after"; then
      summary_obligation "$mate" cursor-persist
      return 1
    fi
    clear_summary_obligation "$mate" || return 1
  done
}

scan() {
  local startup=${1:-0} self='' meta id kind
  SCAN_STARTUP=$startup
  mkdir -p "$STATE" "$OUTCOME_DIR" || return 1
  [ ! -L "$OUTCOME_DIR" ] || return 1
  if [ "$(scan_marker_age)" -lt "$FM_INACTIVE_RECONCILE_SECS" ]; then
    return 0
  fi
  printf '%s\n' "$(reconcile_now)" > "$SCAN_MARKER" || return 1
  self=$(home_secondmate_id || true)
  replay_pending_outcomes "$self" || return 1
  for meta in "$STATE"/*.meta; do
    [ -f "$meta" ] || continue
    id=$(basename "$meta" .meta)
    valid_id "$id" || continue
    kind=$(meta_field "$meta" kind)
    [ "$kind" = secondmate ] && continue
    reconcile_direct_child "$id" "$meta" "$self" || return 1
  done
  if [ -z "$self" ]; then
    scan_secondmates
  fi
}

acknowledge() { # <fingerprint>
  local fingerprint=$1 pending presented phase
  case "$fingerprint" in ''|*[!A-Fa-f0-9]*) return 2 ;; esac
  [ -d "$OUTCOME_DIR" ] && [ ! -L "$OUTCOME_DIR" ] || return 1
  pending=$(record_path "$fingerprint" pending)
  presented=$(record_path "$fingerprint" presented)
  [ -f "$pending" ] && [ ! -L "$pending" ] || return 0
  phase=$(record_value "$pending" phase)
  [ "$phase" = presentation ] || return 0
  mv -f "$pending" "$presented"
}

mode=${1:-scan}
case "$mode" in
  scan)
    startup=0
    case "${2:-}" in
      '') ;;
      --startup) startup=1 ;;
      *) printf 'usage: fm-inactive-reconcile.sh scan [--startup]\n' >&2; exit 2 ;;
    esac
    fm_lock_acquire_wait "$SCAN_LOCK" || exit 1
    trap 'fm_lock_release "$SCAN_LOCK"' EXIT
    scan "$startup"
    ;;
  acknowledge)
    [ "$#" -eq 2 ] || { printf 'usage: fm-inactive-reconcile.sh acknowledge <fingerprint>\n' >&2; exit 2; }
    fm_lock_acquire_wait "$SCAN_LOCK" || exit 1
    trap 'fm_lock_release "$SCAN_LOCK"' EXIT
    acknowledge "$2"
    ;;
  -h|--help)
    sed -n '2,40{s/^# \{0,1\}//;p;}' "$0"
    ;;
  *)
    printf 'usage: fm-inactive-reconcile.sh scan [--startup]\n' >&2
    printf '       fm-inactive-reconcile.sh acknowledge <fingerprint>\n' >&2
    exit 2
    ;;
esac
