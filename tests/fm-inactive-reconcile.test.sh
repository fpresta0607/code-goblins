#!/usr/bin/env bash
# Behavioral coverage for bounded inactive terminal-outcome reconciliation.
set -u

# shellcheck source=tests/lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

RECON="$ROOT/bin/fm-inactive-reconcile.sh"
DRAIN="$ROOT/bin/fm-wake-drain.sh"
WATCH="$ROOT/bin/fm-watch.sh"
TMP_ROOT=$(fm_test_tmproot fm-inactive-reconcile)

set_mtime() { # <epoch> <path>
  local epoch=$1 path=$2 stamp
  if stamp=$(date -r "$epoch" +%Y%m%d%H%M.%S 2>/dev/null); then
    touch -t "$stamp" "$path"
  else
    stamp=$(date -d "@$epoch" +%Y%m%d%H%M.%S)
    touch -t "$stamp" "$path"
  fi
}

age() { # <path>...
  local path now
  now=$(( $(date +%s) - 120 ))
  for path in "$@"; do set_mtime "$now" "$path"; done
}

make_tools() { # <world>
  local world=$1 fake
  fake="$world/fakebin"
  mkdir -p "$fake"
  cat > "$fake/fm-crew-state.sh" <<'SH'
#!/usr/bin/env bash
printf 'state: %s · source: fake\n' "${FM_FAKE_CREW_STATE:-unknown}"
SH
  cat > "$fake/fm-fleet-snapshot.sh" <<'SH'
#!/usr/bin/env bash
if [ "${FM_TEST_PRESERVE_SUMMARY_HOME:-0}" = 1 ]; then
  summary=$(cat "$FM_HOME/summary.json")
else
  summary=$(jq --arg home "$FM_HOME" '.home=$home' "$FM_HOME/summary.json")
fi
if [ "${FM_TEST_PAGED_SUMMARY:-0}" = 1 ]; then
  printf '%s' "$summary" | jq --arg after "${FM_SNAPSHOT_SECONDMATE_TERMINAL_AFTER:-}" --argjson n "${FM_SNAPSHOT_SECONDMATE_CHILDREN:-2}" '
    (.terminal_children | sort_by(.id)) as $all
    | ([$all[] | select($after == "" or .id > $after)]) as $remaining
    | ($remaining[:$n]) as $page
    | .terminal_children = $page
    | .terminal_page = {
        after:(if $after == "" then null else $after end),
        next_after:(if ($remaining | length) > $n then $page[-1].id else null end),
        has_more:(($remaining | length) > $n), count:($page | length),
        remaining:($remaining | length), total:($all | length)
      }'
else
  printf '%s\n' "$summary"
fi
SH
  cat > "$fake/fm-send.sh" <<'SH'
#!/usr/bin/env bash
set -u
printf '%s\t%s\n' "$1" "$2" >> "${FM_TEST_SEND_LOG:?}"
if [ -n "${FM_TEST_ATTEMPT_LOG:-}" ]; then
  sed -n 's/^request_attempted=//p' "${FM_STATE_OVERRIDE:?}/terminal-outcomes/"*.pending > "$FM_TEST_ATTEMPT_LOG"
fi
[ "${FM_TEST_SEND_FAIL:-0}" != 1 ] || exit 1
corr=$(printf '%s' "$2" | grep -Eo 'corr=[A-Fa-f0-9]{16}' | head -1 | cut -d= -f2- || true)
if [ -n "$corr" ]; then
  # The real fm-send confirms a successfully delivered existing expectation.
  # Preserve that observable contract in this deterministic transport fake.
  # shellcheck source=/dev/null
  . "${FM_TEST_ROOT:?}/bin/fm-pending-reply-lib.sh"
  fm_pending_reply_confirm_delivery "${FM_STATE_OVERRIDE:?}" "$corr"
fi
SH
  cat > "$fake/tmux" <<'SH'
#!/usr/bin/env bash
case "${1:-}" in
  display-message) printf '%%1\n' ;;
  capture-pane) printf 'idle\n> \n' ;;
esac
SH
  local tool
  for tool in gh gh-axi curl; do
    cat > "$fake/$tool" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$(basename "$0")" >> "${FM_FORGE_LOG:?}"
exit 97
SH
  done
  chmod +x "$fake"/*
}

make_world() { # <name>
  WORLD="$TMP_ROOT/$1"
  MAIN="$WORLD/main"
  MATE="$WORLD/mate"
  mkdir -p "$WORLD/root" "$MAIN"/{state,data,config,projects} "$MATE"/{state,data,config,projects,bin}
  : > "$MATE/AGENTS.md"
  make_tools "$WORLD"
  : > "$WORLD/forge.log"
  : > "$WORLD/send.log"
}

bind_secondmate() { # <local|remote>
  local route=$1
  printf 'mate\n' > "$MATE/.fm-secondmate-home"
  if [ "$route" = local ]; then
    cat > "$MATE/.fm-secondmate-parent" <<EOF
schema=fm-secondmate-parent.v1
route=local
parent_home=$MAIN
EOF
  else
    cat > "$MATE/.fm-secondmate-parent" <<'EOF'
schema=fm-secondmate-parent.v1
route=remote
EOF
  fi
}

write_child() { # <home> <id> <status>
  local home=$1 id=$2 status=$3
  fm_write_meta "$home/state/$id.meta" \
    "window=firstmate:fm-$id" "worktree=$home/projects/$id" "project=alpha" \
    'harness=codex' 'kind=ship' 'mode=no-mistakes' 'yolo=off' \
    'pr=https://example.test/owner/repo/pull/1'
  printf '%s\n' "$status" > "$home/state/$id.status"
  : > "$home/state/$id.turn-ended"
  age "$home/state/$id.meta" "$home/state/$id.status" "$home/state/$id.turn-ended"
}

write_mate_meta() {
  fm_write_secondmate_meta "$MAIN/state/mate.meta" "$MATE"
  printf 'working: delegated scope\n' > "$MAIN/state/mate.status"
  age "$MAIN/state/mate.meta" "$MAIN/state/mate.status"
}

write_summary() { # <state>
  local generated
  generated=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  cat > "$MATE/summary.json" <<EOF
{"schema":"fm-secondmate-home-summary.v1","generated":"$generated","home":"$MATE","valid":false,"reason":"terminal child","invalidity":{"kind":"terminal_in_flight","ids":["child"]},"state":"unknown","active_children":[],"terminal_children":[{"id":"child","state":"$1"}],"terminal_page":{"after":null,"next_after":null,"has_more":false,"count":1,"remaining":1,"total":1},"decisions_open":[],"holds":[],"queued":[],"landed":[],"endpoints":[],"counts":{},"omitted":[]}
EOF
}

run_reconcile() { # <home> [--startup]
  local home=$1 option=${2:-}
  PATH="$WORLD/fakebin:$PATH" FM_ROOT_OVERRIDE="$WORLD/root" FM_HOME="$home" \
    FM_STATE_OVERRIDE="$home/state" FM_DATA_OVERRIDE="$home/data" FM_CONFIG_OVERRIDE="$home/config" \
    FM_INACTIVE_RECONCILE_SECS=60 FM_INACTIVE_CREW_STATE_BIN="$WORLD/fakebin/fm-crew-state.sh" \
    FM_INACTIVE_RECONCILE_SUMMARY_BIN="$WORLD/fakebin/fm-fleet-snapshot.sh" \
    FM_INACTIVE_RECONCILE_SEND_BIN="$WORLD/fakebin/fm-send.sh" \
    FM_TEST_ROOT="$ROOT" FM_TEST_SEND_LOG="$WORLD/send.log" FM_FORGE_LOG="$WORLD/forge.log" "$RECON" scan ${option:+"$option"}
}

wake_count() { # <home> <key prefix>
  grep -c "$2" "$1/state/.wake-queue" 2>/dev/null || true
}

outcome_count() { # <home> <suffix>
  find "$1/state/terminal-outcomes" -type f -name "*.$2" 2>/dev/null | wc -l | tr -d ' '
}

prime_seen() { # <state> <status>
  local state=$1 status=$2 sig
  if [ "$(uname)" = Darwin ]; then sig=$(stat -f '%z:%Fm' "$status"); else sig=$(stat -c '%s:%Y' "$status"); fi
  printf '%s' "$sig" > "$state/.seen-$(basename "$status" | tr '.' '_')"
}

reap() { kill "$1" 2>/dev/null || true; wait "$1" 2>/dev/null || true; }

# The secondmate independently reports a genuinely terminal inactive child, and
# the main keeps its own presentation receipt until the resulting wake is acked.
test_local_secondmate_report_and_main_receipt() {
  local err seq generation
  make_world local; bind_secondmate local; write_child "$MATE" child 'done: PR https://example.test/owner/repo/pull/1 checks green'
  FM_FAKE_CREW_STATE='done' run_reconcile "$MATE" --startup
  grep -Fq 'done [key=inactive-outcome-mate-child-done]:' "$MAIN/state/mate.status" \
    || fail "secondmate did not append its durable parent report"
  [ "$(outcome_count "$MATE" reported)" = 1 ] || fail "secondmate report receipt was not durable"

  write_mate_meta; write_summary "done"
  printf 'done [key=inactive-outcome-mate-child-done]: inactive terminal child=child\n' >> "$MAIN/state/mate.status"
  age "$MAIN/state/mate.meta" "$MAIN/state/mate.status"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(wake_count "$MAIN" 'inactive-outcome:')" = 1 ] || fail "main did not queue terminal presentation"
  [ "$(outcome_count "$MAIN" pending)" = 1 ] || fail "main did not retain presentation receipt"

  err="$WORLD/drain.err"
  FM_HOME="$MAIN" FM_STATE_OVERRIDE="$MAIN/state" "$DRAIN" >/dev/null 2> "$err"
  seq=$(sed -n 's/^WAKE_ACK_REQUIRED:.*--ack-through \([0-9][0-9]*\) --recovery-generation .*/\1/p' "$err")
  generation=$(sed -n 's/^WAKE_ACK_REQUIRED:.*--recovery-generation \([A-Za-z0-9._-][A-Za-z0-9._-]*\)$/\1/p' "$err")
  [ -n "$seq" ] && [ -n "$generation" ] || fail "main presentation did not require durable acknowledgement"
  FM_HOME="$MAIN" FM_STATE_OVERRIDE="$MAIN/state" "$DRAIN" --ack-through "$seq" --recovery-generation "$generation"
  [ "$(outcome_count "$MAIN" presented)" = 1 ] || fail "acknowledged presentation did not receive its own receipt"
  pass "local child report and main presentation use separate durable receipts"
}

# If the secondmate never reports, the main reads its validated summary and makes
# one correlated request rather than depending on the secondmate's own watcher.
test_main_cross_home_discovers_missing_report_once() {
  make_world cross-home; bind_secondmate local; write_mate_meta; write_summary failed
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(wake_count "$MAIN" 'inactive-reconcile:')" = 1 ] || fail "missing secondmate report did not wake main"
  [ "$(wc -l < "$WORLD/send.log" | tr -d ' ')" = 1 ] || fail "main did not send exactly one reconciliation request"
  [ "$(find "$MAIN/state/pending-replies" -type f 2>/dev/null | wc -l | tr -d ' ')" = 1 ] \
    || fail "missing report did not create a pending-reply obligation"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(wc -l < "$WORLD/send.log" | tr -d ' ')" = 1 ] || fail "duplicate scan resent reconciliation request"
  printf 'failed [key=inactive-outcome-mate-child-failed]: terminal child report\n' >> "$MAIN/state/mate.status"
  age "$MAIN/state/mate.status"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  grep -Fq 'phase=resolved' "$MAIN/state/pending-replies/"* \
    || fail "matching automatic report did not settle its correlated request"
  [ "$(wake_count "$MAIN" 'inactive-outcome:')" = 1 ] || fail "matching parent report did not create presentation"
  pass "main cross-home backstop discovers, requests, and settles a missing terminal report"
}

# A transport failure retains the one correlation under the pending-reply owner;
# later inactive scans only re-announce that durable obligation.
test_failed_delivery_preserves_one_correlated_obligation() {
  local corr
  make_world failed-send; bind_secondmate local; write_mate_meta; write_summary failed
  FM_TEST_SEND_FAIL=1 FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(find "$MAIN/state/pending-replies" -type f ! -name '.*' | wc -l | tr -d ' ')" = 1 ] \
    || fail "failed delivery did not preserve exactly one pending reply"
  corr=$(sed -n 's/^correlation=//p' "$MAIN/state/terminal-outcomes/"*.pending)
  [ -n "$corr" ] || fail "failed delivery lost the outcome correlation"
  grep -Fq 'phase=delivery_unknown' "$MAIN/state/pending-replies/$corr" \
    || fail "failed delivery was not handed to pending-reply recovery"
  FM_TEST_SEND_FAIL=1 FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(wc -l < "$WORLD/send.log" | tr -d ' ')" = 1 ] || fail "inactive scan created a second delivery loop"
  [ "$(find "$MAIN/state/pending-replies" -type f ! -name '.*' | wc -l | tr -d ' ')" = 1 ] \
    || fail "inactive scan created a second correlation"
  pass "failed delivery preserves one pending-reply-owned obligation"
}

# Wrong-home and malformed summaries are never consumed as terminal authority,
# but their unreadable reconciliation boundary remains durably visible.
test_invalid_summary_raises_obligation_without_terminal_request() {
  make_world invalid-summary; bind_secondmate local; write_mate_meta; write_summary done
  jq '.home="/wrong/home"' "$MATE/summary.json" > "$MATE/summary.tmp" && mv "$MATE/summary.tmp" "$MATE/summary.json"
  FM_TEST_PRESERVE_SUMMARY_HOME=1 FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ ! -s "$WORLD/send.log" ] || fail "wrong-home summary produced a terminal request"
  [ "$(outcome_count "$MAIN" pending)" = 1 ] || fail "invalid summary did not preserve a reconciliation obligation"
  [ "$(wake_count "$MAIN" 'inactive-reconcile:')" = 1 ] || fail "invalid summary did not surface its obligation"

  jq '.valid=true | .reason=null | .invalidity={kind:null,ids:[]} | .state="no_active_work"
      | .terminal_children=[]
      | .terminal_page={after:null,next_after:null,has_more:false,count:0,remaining:0,total:0}' \
    "$MATE/summary.json" > "$MATE/summary.tmp" && mv "$MATE/summary.tmp" "$MATE/summary.json"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(outcome_count "$MAIN" pending)" = 0 ] || fail "valid summary did not close the failure episode"
  jq '.home="/wrong/again"' "$MATE/summary.json" > "$MATE/summary.tmp" && mv "$MATE/summary.tmp" "$MATE/summary.json"
  FM_TEST_PRESERVE_SUMMARY_HOME=1 FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(outcome_count "$MAIN" pending)" = 1 ] || fail "recurring summary failure was suppressed by its old receipt"
  pass "invalid structured summaries fail safe and can recur after recovery"
}

test_request_attempt_is_durable_before_transport() {
  make_world prepared-send; bind_secondmate local; write_mate_meta; write_summary failed
  FM_TEST_ATTEMPT_LOG="$WORLD/attempt.log" FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(cat "$WORLD/attempt.log")" = 1 ] || fail "transport began before the durable request attempt"
  pass "request attempt is durable before uncertain transport"
}

test_crashed_pending_reply_link_is_recovered() {
  local record fingerprint owner corr
  make_world crashed-link; bind_secondmate local; write_mate_meta; write_summary failed
  : > "$MAIN/state/pending-replies"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  rm -f "$MAIN/state/pending-replies"
  record=$(find "$MAIN/state/terminal-outcomes" -type f -name '*.pending' | head -1)
  fingerprint=$(sed -n 's/^fingerprint=//p' "$record")
  owner="inactive-terminal-outcome:$fingerprint"
  corr=$(
    # shellcheck source=bin/fm-pending-reply-lib.sh
    . "$ROOT/bin/fm-pending-reply-lib.sh"
    fm_pending_reply_create "$MAIN" "$MAIN/state" mate 'crash-window request' "$owner"
  )
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(find "$MAIN/state/pending-replies" -type f ! -name '.*' | wc -l | tr -d ' ')" = 1 ] \
    || fail "restart created a second pending reply after the link crash"
  grep -Fq "correlation=$corr" "$record" || fail "restart did not recover the owned correlation"
  [ "$(wc -l < "$WORLD/send.log" | tr -d ' ')" = 1 ] || fail "recovered request was not delivered exactly once"
  pass "crashed pending-reply links recover their original correlation"
}

test_orphaned_owned_reply_is_settled_after_report() {
  local record fingerprint owner corr
  make_world orphaned-settle; bind_secondmate local; write_mate_meta; write_summary failed
  : > "$MAIN/state/pending-replies"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  rm -f "$MAIN/state/pending-replies"
  record=$(find "$MAIN/state/terminal-outcomes" -type f -name '*.pending' | head -1)
  fingerprint=$(sed -n 's/^fingerprint=//p' "$record")
  owner="inactive-terminal-outcome:$fingerprint"
  corr=$(
    . "$ROOT/bin/fm-pending-reply-lib.sh"
    fm_pending_reply_create "$MAIN" "$MAIN/state" mate 'crash-window request' "$owner"
  )
  printf 'failed [key=inactive-outcome-mate-child-failed]: terminal report\n' >> "$MAIN/state/mate.status"
  age "$MAIN/state/mate.meta" "$MAIN/state/mate.status"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  grep -Fq "correlation=$corr" "$record" || fail "reported outcome did not recover its owned correlation"
  grep -Fq 'phase=resolved' "$MAIN/state/pending-replies/$corr" \
    || fail "recovered owned expectation remained unresolved after its report"
  [ ! -s "$WORLD/send.log" ] || fail "already reported orphaned expectation was redelivered"
  pass "reported outcomes settle orphaned owned expectations"
}

test_terminal_episode_identity_allows_task_id_reuse() {
  make_world episode-reuse; bind_secondmate remote
  write_child "$MATE" child 'done: first episode'
  printf 'episode_id=episode-one\n' >> "$MATE/state/child.meta"
  age "$MATE/state/child.meta"
  FM_FAKE_CREW_STATE=done run_reconcile "$MATE" --startup
  rm -f "$MATE/state/child.meta" "$MATE/state/child.status" "$MATE/state/child.turn-ended"
  write_child "$MATE" child 'done: second episode'
  printf 'episode_id=episode-two\n' >> "$MATE/state/child.meta"
  age "$MATE/state/child.meta"
  FM_FAKE_CREW_STATE=done run_reconcile "$MATE" --startup
  [ "$(grep -c 'inactive terminal child=child' "$MATE/state/parent-replies.status")" = 2 ] \
    || fail "reused task id suppressed a distinct terminal episode"
  [ "$(outcome_count "$MATE" reported)" = 2 ] \
    || fail "distinct terminal episodes shared one durable receipt"

  make_world summarized-episode-reuse; bind_secondmate local; write_mate_meta; write_summary failed
  jq '.terminal_children[0].episode_id="episode-one"' "$MATE/summary.json" > "$MATE/summary.tmp" \
    && mv "$MATE/summary.tmp" "$MATE/summary.json"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  jq '.terminal_children[0].episode_id="episode-two"' "$MATE/summary.json" > "$MATE/summary.tmp" \
    && mv "$MATE/summary.tmp" "$MATE/summary.json"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(wc -l < "$WORLD/send.log" | tr -d ' ')" = 2 ] \
    || fail "summary reconciliation collapsed distinct terminal episodes"
  [ "$(outcome_count "$MAIN" pending)" = 2 ] \
    || fail "summarized terminal episodes shared one obligation"
  pass "terminal episode identity permits safe task id reuse"
}

test_legacy_summary_without_terminal_channel_is_compatible() {
  make_world legacy-summary; bind_secondmate local; write_mate_meta; write_summary done
  jq 'del(.terminal_children,.terminal_page)
      | .valid=true | .reason=null | .invalidity={kind:null,ids:[]} | .state="no_active_work"' \
    "$MATE/summary.json" > "$MATE/summary.tmp" && mv "$MATE/summary.tmp" "$MATE/summary.json"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(outcome_count "$MAIN" pending)" = 0 ] || fail "legacy healthy summary raised a false obligation"
  [ ! -s "$WORLD/send.log" ] || fail "legacy summary produced a terminal request"
  pass "legacy structured summaries remain compatible"
}

test_competing_invalidity_does_not_hide_terminal_page() {
  make_world competing-invalidity; bind_secondmate local; write_mate_meta; write_summary failed
  jq '.invalidity={kind:"unstructured_current",ids:[]}
      | .reason="unstructured current backlog row"' "$MATE/summary.json" > "$MATE/summary.tmp" \
    && mv "$MATE/summary.tmp" "$MATE/summary.json"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(wc -l < "$WORLD/send.log" | tr -d ' ')" = 1 ] || fail "competing invalidity hid terminal evidence"
  pass "terminal evidence is validated independently from primary invalidity"
}

test_malformed_terminal_page_is_not_consumed() {
  local generated mutation
  for mutation in unsorted bad-after bad-count bad-total valid-terminal missing-page orphan-page bad-validity bad-invalidity; do
    make_world "malformed-page-$mutation"; bind_secondmate local; write_mate_meta
    generated=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    jq -n --arg generated "$generated" --arg home "$MATE" --arg mutation "$mutation" '
      {schema:"fm-secondmate-home-summary.v1",generated:$generated,home:$home,valid:false,
       reason:"terminal children",invalidity:{kind:"terminal_in_flight",ids:[]},state:"unknown",
       active_children:[],terminal_children:[{id:"child2",state:"failed"},{id:"child1",state:"done"}],
       terminal_page:{after:null,next_after:null,has_more:false,count:2,remaining:2,total:2},
       decisions_open:[],holds:[],queued:[],landed:[],endpoints:[],counts:{},omitted:[]}
      | if $mutation == "bad-after" then .terminal_children=[{id:"child1",state:"failed"}]
          | .terminal_page={after:"child1",next_after:null,has_more:false,count:1,remaining:1,total:2}
        elif $mutation == "bad-count" then .terminal_children=[{id:"child1",state:"failed"}]
          | .terminal_page.count=2 | .terminal_page.remaining=1
        elif $mutation == "bad-total" then .terminal_children=[{id:"child1",state:"failed"}]
          | .terminal_page={after:null,next_after:null,has_more:false,count:1,remaining:1,total:3}
        elif $mutation == "valid-terminal" then .valid=true | .reason=null
          | .invalidity={kind:null,ids:[]} | .state="no_active_work"
        elif $mutation == "missing-page" then del(.terminal_page)
        elif $mutation == "orphan-page" then del(.terminal_children)
        elif $mutation == "bad-validity" then .valid=true
        elif $mutation == "bad-invalidity" then .invalidity={kind:null,ids:[]}
        else . end' > "$MATE/summary.json"
    if [ "$mutation" = bad-after ]; then
      mkdir -p "$MAIN/state/terminal-outcomes"
      printf 'child1\n' > "$MAIN/state/terminal-outcomes/.summary-cursor-mate"
    fi
    FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
    [ ! -s "$WORLD/send.log" ] || fail "$mutation terminal page was consumed"
    [ "$(wake_count "$MAIN" 'inactive-reconcile:')" = 1 ] || fail "$mutation terminal page did not surface"
  done
  pass "malformed terminal page semantics fail safe"
}

test_terminal_pages_eventually_expose_every_child() {
  local i generated
  make_world paged; bind_secondmate local; write_mate_meta
  generated=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  jq -n --arg generated "$generated" --arg home "$MATE" '
    {schema:"fm-secondmate-home-summary.v1",generated:$generated,home:$home,valid:false,
     reason:"terminal children",invalidity:{kind:"terminal_in_flight",ids:[]},state:"unknown",
     active_children:[],terminal_children:[range(0;5) | {id:("child" + tostring),state:"failed"}],
     decisions_open:[],holds:[],queued:[],landed:[],endpoints:[],counts:{},omitted:[]}' > "$MATE/summary.json"
  i=0
  while [ "$i" -lt 3 ]; do
    FM_TEST_PAGED_SUMMARY=1 FM_SNAPSHOT_SECONDMATE_CHILDREN=2 FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
    i=$((i + 1))
  done
  [ "$(wc -l < "$WORLD/send.log" | tr -d ' ')" = 5 ] || fail "paged summary did not expose every terminal child"
  [ "$(outcome_count "$MAIN" pending)" = 5 ] || fail "paged terminal obligations were dropped"
  pass "bounded terminal pages eventually expose every child"
}

test_child_reconciliation_failure_retains_page_cursor() {
  local generated record
  make_world child-persist-failure; bind_secondmate local; write_mate_meta
  generated=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  jq -n --arg generated "$generated" --arg home "$MATE" '
    {schema:"fm-secondmate-home-summary.v1",generated:$generated,home:$home,valid:false,
     reason:"terminal children",invalidity:{kind:"terminal_in_flight",ids:[]},state:"unknown",
     active_children:[],terminal_children:[{id:"child0",state:"failed"},{id:"child1",state:"failed"}],
     decisions_open:[],holds:[],queued:[],landed:[],endpoints:[],counts:{},omitted:[]}' > "$MATE/summary.json"
  FM_TEST_PAGED_SUMMARY=1 FM_SNAPSHOT_SECONDMATE_CHILDREN=1 FM_FAKE_CREW_STATE=unknown \
    run_reconcile "$MAIN" --startup
  rm -f "$MAIN/state/terminal-outcomes/.summary-cursor-mate"
  record=$(grep -l '^task_id=child0$' "$MAIN/state/terminal-outcomes/"*.pending | head -1)
  printf 'correlation=0123456789abcdef\n' >> "$record"
  printf 'failed [key=inactive-outcome-mate-child0-failed]: terminal report\n' \
    >> "$MAIN/state/mate.status"
  cat > "$WORLD/fakebin/mv" <<'SH'
#!/usr/bin/env bash
case "${2:-}:${3:-}" in
  */terminal-outcomes/.record.*:*/terminal-outcomes/*.pending) exit 73 ;;
esac
exec /bin/mv "$@"
SH
  chmod +x "$WORLD/fakebin/mv"
  age "$MAIN/state/mate.meta" "$MAIN/state/mate.status"
  if FM_TEST_PAGED_SUMMARY=1 FM_SNAPSHOT_SECONDMATE_CHILDREN=1 FM_FAKE_CREW_STATE=unknown \
      run_reconcile "$MAIN" --startup; then
    fail "child reconciliation failure did not fail the page"
  fi
  [ ! -e "$MAIN/state/terminal-outcomes/.summary-cursor-mate" ] \
    || fail "failed child reconciliation advanced the page cursor"
  [ "$(wake_count "$MAIN" 'inactive-reconcile:')" -ge 1 ] \
    || fail "child reconciliation failure was not actionable"
  pass "failed child reconciliation retains its page cursor"
}

test_cursor_persistence_failure_is_actionable() {
  local generated
  make_world cursor-failure; bind_secondmate local; write_mate_meta
  generated=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  jq -n --arg generated "$generated" --arg home "$MATE" '
    {schema:"fm-secondmate-home-summary.v1",generated:$generated,home:$home,valid:false,
     reason:"terminal children",invalidity:{kind:"terminal_in_flight",ids:[]},state:"unknown",
     active_children:[],terminal_children:[{id:"child0",state:"failed"},{id:"child1",state:"failed"}],
     decisions_open:[],holds:[],queued:[],landed:[],endpoints:[],counts:{},omitted:[]}' > "$MATE/summary.json"
  mkdir "$MAIN/state/terminal-outcomes"
  mkdir "$MAIN/state/terminal-outcomes/.summary-cursor-mate"
  if FM_TEST_PAGED_SUMMARY=1 FM_SNAPSHOT_SECONDMATE_CHILDREN=1 FM_FAKE_CREW_STATE=unknown \
      run_reconcile "$MAIN" --startup; then
    fail "cursor persistence failure did not fail reconciliation"
  fi
  [ "$(wake_count "$MAIN" 'inactive-reconcile:')" -ge 1 ] || fail "cursor persistence failure was not actionable"
  [ "$(outcome_count "$MAIN" pending)" = 2 ] || fail "cursor failure did not preserve child and summary obligations"
  pass "cursor persistence failure preserves an actionable obligation"
}

test_remote_startup_deferral_is_silent() {
  make_world remote-startup; write_mate_meta
  printf 'remote_host=example.invalid\n' >> "$MAIN/state/mate.meta"
  FM_FAKE_CREW_STATE=unknown run_reconcile "$MAIN" --startup
  [ "$(outcome_count "$MAIN" pending)" = 0 ] || fail "remote startup deferral created a summary obligation"
  [ ! -s "$MAIN/state/.wake-queue" ] || fail "remote startup deferral emitted a false alert"
  pass "remote summaries are silently deferred at startup"
}

# A remote child route writes the existing mirror input once even across restarts.
test_remote_parent_reply_is_idempotent() {
  make_world remote; bind_secondmate remote; write_child "$MATE" child 'done: green'
  FM_FAKE_CREW_STATE='done' run_reconcile "$MATE" --startup
  printf 'done: revised detail with late PR https://example.test/owner/repo/pull/99\n' >> "$MATE/state/child.status"
  printf 'pr=https://example.test/owner/repo/pull/99\n' >> "$MATE/state/child.meta"
  age "$MATE/state/child.meta" "$MATE/state/child.status"
  FM_FAKE_CREW_STATE='done' run_reconcile "$MATE" --startup
  [ "$(grep -c 'inactive-outcome-mate-child-done' "$MATE/state/parent-replies.status")" = 1 ] \
    || fail "mutable terminal details duplicated the remote parent reply"
  [ "$(outcome_count "$MATE" reported)" = 1 ] || fail "mutable terminal details duplicated the durable receipt"
  pass "remote parent replies use stable terminal episode identity"
}

# Heartbeat backoff state is deliberately irrelevant to the independent cadence.
test_heartbeat_cap_does_not_delay_reconciliation() {
  make_world heartbeat; write_child "$MAIN" child 'done: PR https://example.test/owner/repo/pull/1 checks green'
  printf '12\n' > "$MAIN/state/.heartbeat-streak"
  : > "$MAIN/state/.last-heartbeat"
  FM_FAKE_CREW_STATE='done' run_reconcile "$MAIN" --startup
  [ "$(wake_count "$MAIN" 'inactive-outcome:')" = 1 ] || fail "heartbeat cap suppressed inactive terminal reconciliation"
  pass "terminal reconciliation ignores heartbeat backoff state"
}

# Only authoritative terminal states qualify. A captain-held item is excluded too.
test_nonterminal_and_captain_held_states_do_not_report() {
  local state
  for state in working paused parked unknown; do
    make_world "nonterminal-$state"; write_child "$MAIN" child 'working: still active'
    FM_FAKE_CREW_STATE="$state" run_reconcile "$MAIN" --startup
    [ "$(outcome_count "$MAIN" pending)" = 0 ] || fail "$state produced a terminal outcome"
  done
  make_world captain-held; write_child "$MAIN" child 'captain-held: awaiting captain'
  FM_FAKE_CREW_STATE='done' run_reconcile "$MAIN" --startup
  [ "$(outcome_count "$MAIN" pending)" = 0 ] || fail "captain-held item was reconciled"
  pass "nonterminal and captain-held workers remain outside inactive terminal reporting"
}

# The actual watcher poll invokes the helper, while an idle secondmate remains
# exempt from wedge escalation and emits no false wake.
test_watcher_hook_and_idle_secondmate_exemption() {
  local out pid i
  make_world watcher; write_child "$MAIN" child 'done: green'; prime_seen "$MAIN/state" "$MAIN/state/child.status"
  out="$WORLD/watch.out"
  PATH="$WORLD/fakebin:$PATH" FM_HOME="$MAIN" FM_STATE_OVERRIDE="$MAIN/state" \
    FM_INACTIVE_RECONCILE_SECS=60 FM_INACTIVE_CREW_STATE_BIN="$WORLD/fakebin/fm-crew-state.sh" \
    FM_INACTIVE_RECONCILE_SUMMARY_BIN="$WORLD/fakebin/fm-fleet-snapshot.sh" \
    FM_INACTIVE_RECONCILE_SEND_BIN="$WORLD/fakebin/fm-send.sh" FM_TEST_SEND_LOG="$WORLD/send.log" \
    FM_FORGE_LOG="$WORLD/forge.log" FM_POLL=1 FM_SIGNAL_GRACE=1 FM_CHECK_INTERVAL=999999 FM_HEARTBEAT=999999 \
    FM_FAKE_CREW_STATE='done' "$WATCH" > "$out" 2>&1 &
  pid=$!
  i=0
  while [ "$i" -lt 40 ]; do
    kill -0 "$pid" 2>/dev/null || break
    [ "$(wake_count "$MAIN" 'inactive-outcome:')" = 1 ] && break
    sleep 0.1
    i=$((i + 1))
  done
  wait "$pid" 2>/dev/null || true
  grep -Fq 'check: inactive-outcome' "$out" || fail "watcher did not surface its reconciliation result"

  make_world idle-secondmate; bind_secondmate local; write_mate_meta; prime_seen "$MAIN/state" "$MAIN/state/mate.status"
  PATH="$WORLD/fakebin:$PATH" FM_HOME="$MAIN" FM_STATE_OVERRIDE="$MAIN/state" FM_POLL=1 FM_SIGNAL_GRACE=1 \
    FM_CHECK_INTERVAL=999999 FM_HEARTBEAT=999999 "$WATCH" > "$WORLD/idle.out" 2>&1 &
  pid=$!; sleep 2; kill -0 "$pid" 2>/dev/null || fail "idle secondmate watcher exited unexpectedly"; reap "$pid"
  grep -F 'stale:' "$WORLD/idle.out" >/dev/null && fail "idle secondmate was treated as a wedge"
  [ ! -s "$MAIN/state/.wake-queue" ] || fail "idle secondmate emitted a false wake"
  pass "watcher hook wakes for terminal loss and preserves idle secondmate exemption"
}

# Forge command shims fail loudly. A successful scan proves this path never uses
# them, even while inspecting local and secondmate terminal outcomes.
test_reconciliation_never_calls_forge() {
  make_world forge; bind_secondmate local; write_child "$MAIN" child 'done: green'; write_mate_meta; write_summary "done"
  FM_FAKE_CREW_STATE='done' run_reconcile "$MAIN" --startup
  [ ! -s "$WORLD/forge.log" ] || fail "reconciliation invoked a forge command: $(cat "$WORLD/forge.log")"
  pass "reconciliation makes zero forge or PR API calls"
}

test_local_secondmate_report_and_main_receipt
test_main_cross_home_discovers_missing_report_once
test_failed_delivery_preserves_one_correlated_obligation
test_invalid_summary_raises_obligation_without_terminal_request
test_request_attempt_is_durable_before_transport
test_crashed_pending_reply_link_is_recovered
test_orphaned_owned_reply_is_settled_after_report
test_terminal_episode_identity_allows_task_id_reuse
test_legacy_summary_without_terminal_channel_is_compatible
test_competing_invalidity_does_not_hide_terminal_page
test_malformed_terminal_page_is_not_consumed
test_terminal_pages_eventually_expose_every_child
test_child_reconciliation_failure_retains_page_cursor
test_cursor_persistence_failure_is_actionable
test_remote_startup_deferral_is_silent
test_remote_parent_reply_is_idempotent
test_heartbeat_cap_does_not_delay_reconciliation
test_nonterminal_and_captain_held_states_do_not_report
test_watcher_hook_and_idle_secondmate_exemption
test_reconciliation_never_calls_forge

echo "all inactive reconciliation tests passed"
