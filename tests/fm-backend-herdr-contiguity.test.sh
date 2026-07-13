#!/usr/bin/env bash
# tests/fm-backend-herdr-contiguity.test.sh - unit tests for the herdr
# workspace contiguity reconciler (bin/backends/herdr.sh: workspace_order,
# workspace_move, contiguity_edges/target/next_move/reconcile;
# docs/herdr-backend.md "Workspace contiguity").
#
# The ordering logic is pure (live order + ownership edges in, target order /
# one planned move out), so most cases drive the pure functions directly. The
# reconcile fixpoint is driven end to end against a small STATEFUL fake herdr
# CLI (an order-file-backed `workspace list`) plus a fake workspace.move
# writer (FM_BACKEND_HERDR_MOVE_WRITER) that applies remove-then-insert
# final-index semantics - the semantics verified against the real binary in
# tests/fm-backend-herdr-contiguity-e2e.test.sh - and logs every call, so
# move counts (idempotence) and fail-closed aborts are asserted exactly.
#
# The canonical depth-first order these tests pin (captain-mandated, and an
# intentional design choice documented in docs/herdr-backend.md): a
# supervisor, then ALL its DIRECT crews (stable order), then each child
# secondmate followed by that secondmate's own subtree - direct crews ALWAYS
# sort before child-secondmate subtrees.
set -u

# shellcheck source=tests/lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

command -v jq >/dev/null 2>&1 || { echo "skip: jq not found (required by the herdr adapter)"; exit 0; }

TMP_ROOT=$(fm_test_tmproot fm-backend-herdr-contiguity)

FM_HOME="$TMP_ROOT/home"
mkdir -p "$FM_HOME"
export FM_HOME
# shellcheck source=bin/backends/herdr.sh
. "$ROOT/bin/backends/herdr.sh"

SES=labS

# --- stateful fake herdr CLI + fake move writer -------------------------------
# The fake `herdr` answers `workspace list` from the order file (one id per
# line; label mirrors the id) and `session list --json` with a fake socket for
# $SES. FM_FAKE_LIST_FAIL forces list failures; FM_FAKE_CHURN_AFTER/
# FM_FAKE_CHURN_APPEND appends a workspace id to the order file right after
# the Nth list call (simulating a concurrent create between fixpoint
# iterations). The fake writer applies the move with FINAL-INDEX
# (remove-then-insert) semantics and logs "<sock> <ws> <idx>" per call;
# FM_FAKE_MOVE_FAIL makes it fail without mutating anything.
FB="$TMP_ROOT/fakebin"
mkdir -p "$FB"
cat > "$FB/herdr" <<'SH'
#!/usr/bin/env bash
set -u
ORDER="${FM_FAKE_ORDER:?}"
CLILOG="${FM_FAKE_CLI_LOG:?}"
printf '%s\n' "$*" >> "$CLILOG"
case "${1:-} ${2:-}" in
  "workspace list")
    [ -n "${FM_FAKE_LIST_FAIL:-}" ] && exit 1
    jq -Rn '[inputs | select(length > 0) | {workspace_id: ., label: .}] | {result: {workspaces: .}}' < "$ORDER"
    if [ -n "${FM_FAKE_CHURN_AFTER:-}" ]; then
      n=$(grep -c '^workspace list' "$CLILOG")
      if [ "$n" -eq "$FM_FAKE_CHURN_AFTER" ]; then
        printf '%s\n' "${FM_FAKE_CHURN_APPEND:?}" >> "$ORDER"
      fi
    fi
    ;;
  "session list")
    printf '{"sessions":[{"name":"%s","running":true,"socket_path":"%s"}]}\n' "${FM_FAKE_SESSION:?}" "${FM_FAKE_SOCK:?}"
    ;;
  *) : ;;
esac
exit 0
SH
chmod +x "$FB/herdr"
# The fake writer models the EMPIRICALLY VERIFIED wire semantics of real
# herdr 0.7.3 (docs/herdr-backend.md "workspace.move semantics"): insert_index
# addresses the PRE-removal array, so a leftward move lands AT insert_index,
# a rightward move lands at insert_index-1, insert_index == length is valid
# (lands last), and anything beyond is refused without mutating the order.
cat > "$FB/fake-move" <<'SH'
#!/usr/bin/env bash
set -u
ORDER="${FM_FAKE_ORDER:?}"
MLOG="${FM_FAKE_MOVE_LOG:?}"
printf '%s %s %s\n' "$1" "$2" "$3" >> "$MLOG"
[ -n "${FM_FAKE_MOVE_FAIL:-}" ] && exit 2
ws=$2 idx=$3
awk -v ws="$ws" -v idx="$idx" '
  { a[n++] = $0; if ($0 == ws) p = n - 1 }
  END {
    if (idx > n) { print "fake-move: insert_index " idx " is out of bounds" > "/dev/stderr"; exit 3 }
    final = (idx > p) ? idx - 1 : idx
    j = 0
    for (i = 0; i < n; i++) if (a[i] != ws) b[j++] = a[i]
    for (i = 0; i <= j; i++) {
      if (i == final) print ws
      if (i < j) print b[i]
    }
  }' "$ORDER" > "$ORDER.new" || exit 3
mv "$ORDER.new" "$ORDER"
exit 0
SH
chmod +x "$FB/fake-move"

# contig_case <name> <initial-order-lines>: fresh state dir + order file +
# logs for one reconcile-level case; sets CASE_STATE/CASE_ORDER/CASE_CLI_LOG/
# CASE_MOVE_LOG.
contig_case() {
  local name=$1 dir="$TMP_ROOT/case-$1"
  mkdir -p "$dir/state"
  printf '%s\n' "$2" > "$dir/order"
  : > "$dir/cli.log"
  : > "$dir/move.log"
  CASE_STATE="$dir/state"
  CASE_ORDER="$dir/order"
  CASE_CLI_LOG="$dir/cli.log"
  CASE_MOVE_LOG="$dir/move.log"
}

write_owned_meta() {  # <state_dir> <id> <child_ws> <parent_ws> [<session>]
  local state=$1 id=$2 child=$3 parent=$4 session=${5:-$SES}
  {
    echo "window=$session:$child:p1"
    echo "backend=herdr"
    echo "herdr_session=$session"
    echo "herdr_workspace_id=$child"
    echo "herdr_tab_id=$child:t1"
    echo "herdr_pane_id=$child:p1"
    echo "herdr_parent_ws=$parent"
    echo "herdr_ws_owned=1"
  } > "$state/$id.meta"
}

run_reconcile() {  # <state_dir> -> RECONCILE_OUT / rc; runs with the fake CLI + writer
  RECONCILE_OUT=$(PATH="$FB:$PATH" \
    FM_FAKE_ORDER="$CASE_ORDER" FM_FAKE_CLI_LOG="$CASE_CLI_LOG" \
    FM_FAKE_MOVE_LOG="$CASE_MOVE_LOG" FM_FAKE_SESSION="$SES" \
    FM_FAKE_SOCK="$TMP_ROOT/fake.sock" \
    FM_BACKEND_HERDR_MOVE_WRITER="$FB/fake-move" \
    fm_backend_herdr_contiguity_reconcile "$SES" "$1" 2>&1)
}

move_count() { grep -c . "$CASE_MOVE_LOG" 2>/dev/null || true; }
order_now() { cat "$CASE_ORDER"; }

# --- target: the canonical depth-first ordering invariant ---------------------

test_target_canonical_render_direct_crews_before_secondmates() {
  # The captain's exact mandated render, by workspace id (labels mirror ids):
  #   firstmate -> firstmate/release-notes (DIRECT crew, BEFORE any
  #   secondmate) -> 2ndmate-hibit-h9 -> its two crews.
  # Live order is natural creation order with the direct crew spawned LAST
  # (appended at the end of the flat list) - the exact displacement the
  # reconciler exists to fix.
  local live edges target
  live='ws-fm
ws-h9
ws-sb
ws-gp
ws-rn'
  edges=$'ws-rn\tws-fm\nws-sb\tws-h9\nws-gp\tws-h9'
  target=$(fm_backend_herdr_contiguity_target "$live" "$edges") || fail "target computation failed"
  [ "$target" = 'ws-fm
ws-rn
ws-h9
ws-sb
ws-gp' ] || fail "canonical order violated: a supervisor's DIRECT crew must sort before its child-secondmate subtrees, got:"$'\n'"$target"
  pass "target: canonical depth-first render - supervisor, direct crew, then each secondmate subtree (direct-crews-before-secondmates)"
}

test_target_multi_supervisor_contiguous_subtrees_stable_order() {
  # One primary with two direct crews plus TWO secondmates, each with two
  # crews, everything interleaved by concurrent spawn appends. Expected:
  # fm, its direct crews (stable), A + its crews (stable), B + its crews
  # (stable) - each supervisor's subtree contiguous.
  local live edges target
  live='ws-fm
ws-A
ws-B
ws-a1
ws-c1
ws-b1
ws-a2
ws-b2
ws-c2'
  edges=$'ws-c1\tws-fm\nws-c2\tws-fm\nws-a1\tws-A\nws-a2\tws-A\nws-b1\tws-B\nws-b2\tws-B'
  target=$(fm_backend_herdr_contiguity_target "$live" "$edges") || fail "target computation failed"
  [ "$target" = 'ws-fm
ws-c1
ws-c2
ws-A
ws-a1
ws-a2
ws-B
ws-b1
ws-b2' ] || fail "multi-supervisor subtrees not contiguous/stable, got:"$'\n'"$target"
  pass "target: two secondmates each get their own contiguous subtree, direct crews first, stable order within each sibling class"
}

test_target_preserves_unrelated_manual_workspace_order() {
  local live edges target
  live='ws-c1
ws-m1
ws-fm
ws-m2
ws-a1
ws-A
ws-m3'
  edges=$'ws-c1\tws-fm\nws-a1\tws-A'
  target=$(fm_backend_herdr_contiguity_target "$live" "$edges") || fail "target computation failed"
  [ "$target" = 'ws-m1
ws-fm
ws-c1
ws-m2
ws-A
ws-a1
ws-m3' ] || fail "unrelated/manual workspaces must keep their relative order, got:"$'\n'"$target"
  pass "target: manual (non-managed) workspaces keep their relative order exactly; only owned crews are relocated"
}

test_target_skips_missing_child_and_missing_parent_groups() {
  local live edges target err
  live='ws-fm
ws-x1
ws-c1'
  # ws-dead is a recorded crew already closed (mid-teardown churn); ws-o1's
  # parent ws-gone is not live (a recreated anchor's stale record). Both are
  # skipped with a note; ws-c1 still reconciles; ws-x1 (ws-o1's stand-in
  # unmanaged neighbor) is untouched.
  edges=$'ws-c1\tws-fm\nws-dead\tws-fm\nws-x1\tws-gone'
  target=$(fm_backend_herdr_contiguity_target "$live" "$edges" 2>"$TMP_ROOT/skip.err") || fail "skips must not abort the whole computation"
  err=$(cat "$TMP_ROOT/skip.err")
  [ "$target" = 'ws-fm
ws-c1
ws-x1' ] || fail "surviving groups must still reconcile around skipped records, got:"$'\n'"$target"
  assert_contains "$err" "ws-dead" "the missing crew skip must be noted"
  assert_contains "$err" "ws-gone" "the missing parent skip must be noted"
  pass "target: a no-longer-live crew or parent is skipped with a note; its group is left untouched, everything else reconciles"
}

test_target_sibling_order_pinned_by_reference_snapshot() {
  # Mid-pass, a rightward push has already flipped a1/a2 in the live list;
  # the reference snapshot (the pass's first read) must keep the target's
  # sibling order at a1 before a2, or the fixpoint oscillates.
  local target
  target=$(fm_backend_herdr_contiguity_target $'ws-fm\nws-a2\nws-a1\nws-A' $'ws-a1\tws-A\nws-a2\tws-A' $'ws-fm\nws-a1\nws-a2\nws-A') \
    || fail "target with reference failed"
  [ "$target" = $'ws-fm\nws-A\nws-a1\nws-a2' ] || fail "sibling order must come from the reference snapshot, got:"$'\n'"$target"
  pass "target: an optional reference snapshot pins sibling order, immune to in-transit live reshuffles"
}

test_target_aborts_on_ambiguous_ownership() {
  local live
  live='ws-fm
ws-c1'
  fm_backend_herdr_contiguity_target "$live" $'ws-c1\tws-fm\nws-c1\tws-A' >/dev/null 2>&1 && \
    fail "a workspace claimed by two tasks must abort"
  fm_backend_herdr_contiguity_target "$live" $'ws-c1\tws-c1' >/dev/null 2>&1 && \
    fail "a workspace recorded as its own parent must abort"
  fm_backend_herdr_contiguity_target "$live" $'ws-c1\tws-fm\nws-fm\tws-c1' >/dev/null 2>&1 && \
    fail "an id recorded as both crew and parent must abort"
  fm_backend_herdr_contiguity_target $'ws-fm\nws-fm' $'ws-c1\tws-fm' >/dev/null 2>&1 && \
    fail "a duplicate id in the live list must abort"
  pass "target: ambiguous ownership (double claim, self-parent, child-as-parent, duplicate live id) fails closed"
}

# --- next_move: single-step planner -------------------------------------------

test_next_move_converged_is_empty() {
  local live=$'ws-fm\nws-c1\nws-A' out
  out=$(fm_backend_herdr_contiguity_next_move "$live" "$live" $'ws-c1\tws-fm') || fail "converged plan must succeed"
  [ -z "$out" ] || fail "an already-correct order must plan ZERO moves, got '$out'"
  pass "next_move: an already-correct order plans no move (idempotence at the planning layer)"
}

test_next_move_pulls_new_crew_leftward() {
  local out
  out=$(fm_backend_herdr_contiguity_next_move $'ws-fm\nws-A\nws-c1' $'ws-fm\nws-c1\nws-A' $'ws-c1\tws-fm') \
    || fail "leftward plan must succeed"
  [ "$out" = $'ws-c1\t1' ] || fail "expected 'ws-c1<TAB>1', got '$out'"
  pass "next_move: a newly-appended crew is pulled leftward to its final 0-based slot"
}

test_next_move_pushes_stray_crew_rightward() {
  # The recovered-anchor shape: a crew sits BEFORE its (re-ensured) anchor;
  # progress requires pushing the crew right, never moving the anchor. The
  # emitted index is the WIRE pre-removal value: final slot 2 needs
  # insert_index 3 on a rightward move (verified real-herdr semantics).
  local out
  out=$(fm_backend_herdr_contiguity_next_move $'ws-fm\nws-a1\nws-A' $'ws-fm\nws-A\nws-a1' $'ws-a1\tws-A') \
    || fail "rightward plan must succeed"
  [ "$out" = $'ws-a1\t3' ] || fail "expected 'ws-a1<TAB>3', got '$out'"
  pass "next_move: a crew stranded above its anchor is pushed rightward (wire index compensates for the removal shift); the anchor itself is never moved"
}

test_next_move_refuses_moving_unmanaged_workspaces() {
  fm_backend_herdr_contiguity_next_move $'ws-m1\nws-m2' $'ws-m2\nws-m1' '' >/dev/null 2>&1 && \
    fail "a plan that would move an unmanaged workspace must be refused"
  pass "next_move: refuses (fails closed) when progress would require moving a workspace firstmate does not own"
}

# --- edges: ownership extraction ----------------------------------------------

test_edges_extracts_only_owned_metas_for_the_session() {
  local state="$TMP_ROOT/edges-state" out
  mkdir -p "$state"
  write_owned_meta "$state" c1 ws-c1 ws-fm
  write_owned_meta "$state" other ws-o1 ws-fm otherses
  printf 'window=%s:w9:p9\nbackend=herdr\nherdr_session=%s\nherdr_workspace_id=ws-t1\n' "$SES" "$SES" > "$state/tab-task.meta"
  out=$(fm_backend_herdr_contiguity_edges "$state" "$SES") || fail "edges extraction failed"
  [ "$out" = $'ws-c1\tws-fm' ] || fail "expected only the owned, session-matching edge, got '$out'"
  pass "edges: only herdr_ws_owned=1 metas for the requested session are ownership edges (tab-per-task and other sessions excluded)"
}

test_edges_fail_closed_on_missing_ownership_fields() {
  local state="$TMP_ROOT/edges-bad" rc
  mkdir -p "$state"
  printf 'backend=herdr\nherdr_session=%s\nherdr_workspace_id=ws-c1\nherdr_ws_owned=1\n' "$SES" > "$state/bad.meta"
  fm_backend_herdr_contiguity_edges "$state" "$SES" >/dev/null 2>&1
  rc=$?
  expect_code 2 "$rc" "an owned meta missing herdr_parent_ws must fail closed"
  pass "edges: an owned meta with missing ownership fields refuses (return 2) instead of guessing"
}

# --- reconcile: fixpoint against the stateful fake -----------------------------

test_reconcile_canonical_render_single_move() {
  contig_case canonical $'ws-fm\nws-h9\nws-sb\nws-gp\nws-rn'
  write_owned_meta "$CASE_STATE" release-notes ws-rn ws-fm
  write_owned_meta "$CASE_STATE" local-sandbox ws-sb ws-h9
  write_owned_meta "$CASE_STATE" game-parity ws-gp ws-h9
  run_reconcile "$CASE_STATE" || fail "reconcile failed: $RECONCILE_OUT"
  [ "$(order_now)" = $'ws-fm\nws-rn\nws-h9\nws-sb\nws-gp' ] || fail "reconcile must produce the canonical render, got:"$'\n'"$(order_now)"
  [ "$(move_count)" = 1 ] || fail "exactly one move (the displaced direct crew) was needed, got $(move_count):"$'\n'"$(cat "$CASE_MOVE_LOG")"
  assert_contains "$(cat "$CASE_MOVE_LOG")" "ws-rn 1" "the single move must place the direct crew at index 1"
  pass "reconcile: a direct crew spawned after a secondmate subtree is pulled into its block with exactly one non-destructive move"
}

test_reconcile_idempotent_no_churn() {
  contig_case idem $'ws-fm\nws-rn\nws-h9\nws-sb\nws-gp'
  write_owned_meta "$CASE_STATE" release-notes ws-rn ws-fm
  write_owned_meta "$CASE_STATE" local-sandbox ws-sb ws-h9
  write_owned_meta "$CASE_STATE" game-parity ws-gp ws-h9
  run_reconcile "$CASE_STATE" || fail "reconcile failed: $RECONCILE_OUT"
  [ "$(move_count)" = 0 ] || fail "an already-correct order must issue ZERO moves, got $(move_count)"
  [ "$(order_now)" = $'ws-fm\nws-rn\nws-h9\nws-sb\nws-gp' ] || fail "idempotent reconcile must not change the order"
  pass "reconcile: an already-correct order is a strict no-op - no redundant moves, no churn"
}

test_reconcile_converges_under_concurrent_create() {
  # A concurrent (unmanaged) workspace appears right after the first list
  # read; the fixpoint re-reads the live list before every move, converges,
  # and never touches the newcomer.
  contig_case churn $'ws-fm\nws-m1\nws-c1'
  write_owned_meta "$CASE_STATE" c1 ws-c1 ws-fm
  FM_FAKE_CHURN_AFTER=1 FM_FAKE_CHURN_APPEND=ws-new run_reconcile_churn
  [ "$(order_now)" = $'ws-fm\nws-c1\nws-m1\nws-new' ] || fail "churn reconcile must converge with the concurrent workspace untouched at its position, got:"$'\n'"$(order_now)"
  [ "$(move_count)" = 1 ] || fail "only the owned crew may move under churn, got $(move_count) moves"
  pass "reconcile: converges under a concurrent workspace create, replanning from a fresh live list each move"
}
run_reconcile_churn() {
  RECONCILE_OUT=$(PATH="$FB:$PATH" \
    FM_FAKE_ORDER="$CASE_ORDER" FM_FAKE_CLI_LOG="$CASE_CLI_LOG" \
    FM_FAKE_MOVE_LOG="$CASE_MOVE_LOG" FM_FAKE_SESSION="$SES" \
    FM_FAKE_SOCK="$TMP_ROOT/fake.sock" \
    FM_FAKE_CHURN_AFTER="$FM_FAKE_CHURN_AFTER" FM_FAKE_CHURN_APPEND="$FM_FAKE_CHURN_APPEND" \
    FM_BACKEND_HERDR_MOVE_WRITER="$FB/fake-move" \
    fm_backend_herdr_contiguity_reconcile "$SES" "$CASE_STATE" 2>&1) || fail "churn reconcile failed: $RECONCILE_OUT"
}

test_reconcile_recovered_anchor_multi_stray_converges_stably() {
  # The recovered-supervisor shape with TWO stray crews stranded ABOVE their
  # (re-created, end-appended) anchor. Every repair move is a rightward push,
  # each of which reshuffles the not-yet-fixed siblings in the live list -
  # the exact shape that oscillates forever without the pinned sibling
  # reference. Must converge, preserve the pass-start sibling order
  # (a1 before a2), and never move the anchor.
  contig_case recovered $'ws-fm\nws-a1\nws-a2\nws-A'
  write_owned_meta "$CASE_STATE" a1 ws-a1 ws-A
  write_owned_meta "$CASE_STATE" a2 ws-a2 ws-A
  run_reconcile "$CASE_STATE" || fail "recovered-anchor reconcile failed: $RECONCILE_OUT"
  [ "$(order_now)" = $'ws-fm\nws-A\nws-a1\nws-a2' ] || fail "stray crews must regroup under the recovered anchor in pass-start order, got:"$'\n'"$(order_now)"
  grep -q "ws-A" "$CASE_MOVE_LOG" && fail "the anchor itself must never be moved: $(cat "$CASE_MOVE_LOG")"
  pass "reconcile: multiple crews stranded above a recovered anchor converge in pass-start sibling order, moving only crews"
}

test_reconcile_tolerates_concurrent_finish() {
  # A recorded crew's workspace is already gone (concurrent teardown finished
  # between the meta read and the list read): skipped with a note, the rest
  # still reconciles.
  contig_case finish $'ws-fm\nws-h9\nws-sb\nws-rn'
  write_owned_meta "$CASE_STATE" release-notes ws-rn ws-fm
  write_owned_meta "$CASE_STATE" local-sandbox ws-sb ws-h9
  write_owned_meta "$CASE_STATE" gone ws-gone ws-fm
  run_reconcile "$CASE_STATE" || fail "reconcile failed: $RECONCILE_OUT"
  [ "$(order_now)" = $'ws-fm\nws-rn\nws-h9\nws-sb' ] || fail "surviving crews must still reconcile around a concurrently-finished one, got:"$'\n'"$(order_now)"
  assert_contains "$RECONCILE_OUT" "ws-gone" "the skipped record must be noted"
  pass "reconcile: a concurrently-finished crew is skipped safely; the surviving fleet still reaches the canonical order"
}

test_reconcile_socket_error_fails_closed() {
  contig_case sockfail $'ws-fm\nws-h9\nws-rn'
  write_owned_meta "$CASE_STATE" release-notes ws-rn ws-fm
  RECONCILE_OUT=$(PATH="$FB:$PATH" \
    FM_FAKE_ORDER="$CASE_ORDER" FM_FAKE_CLI_LOG="$CASE_CLI_LOG" \
    FM_FAKE_MOVE_LOG="$CASE_MOVE_LOG" FM_FAKE_SESSION="$SES" \
    FM_FAKE_SOCK="$TMP_ROOT/fake.sock" FM_FAKE_MOVE_FAIL=1 \
    FM_BACKEND_HERDR_MOVE_WRITER="$FB/fake-move" \
    fm_backend_herdr_contiguity_reconcile "$SES" "$CASE_STATE" 2>&1)
  local rc=$?
  unset FM_FAKE_MOVE_FAIL
  [ "$rc" -ne 0 ] || fail "a writer/socket error must fail the reconcile"
  [ "$(order_now)" = $'ws-fm\nws-h9\nws-rn' ] || fail "a failed move must leave the order untouched"
  [ "$(move_count)" = 1 ] || fail "the reconcile must abort after the FIRST failed move (no blind retries), got $(move_count) attempts"
  assert_contains "$RECONCILE_OUT" "workspace.move failed" "the abort must be logged clearly"
  pass "reconcile: a socket/writer error aborts immediately, logs clearly, and leaves the current order untouched"
}

test_reconcile_missing_ownership_fails_closed_before_any_move() {
  contig_case badmeta $'ws-fm\nws-rn'
  printf 'backend=herdr\nherdr_session=%s\nherdr_workspace_id=ws-rn\nherdr_ws_owned=1\n' "$SES" > "$CASE_STATE/bad.meta"
  run_reconcile "$CASE_STATE" && fail "missing ownership metadata must fail the reconcile"
  [ "$(move_count)" = 0 ] || fail "no move may be issued on unusable ownership metadata"
  [ "$(order_now)" = $'ws-fm\nws-rn' ] || fail "the order must be untouched"
  assert_contains "$RECONCILE_OUT" "unusable ownership metadata" "the abort must be logged clearly"
  pass "reconcile: missing ownership metadata fails closed before any move is planned"
}

test_reconcile_ambiguous_ownership_fails_closed_before_any_move() {
  contig_case ambiguous $'ws-fm\nws-A\nws-c1'
  write_owned_meta "$CASE_STATE" claim1 ws-c1 ws-fm
  write_owned_meta "$CASE_STATE" claim2 ws-c1 ws-A
  run_reconcile "$CASE_STATE" && fail "ambiguous ownership must fail the reconcile"
  [ "$(move_count)" = 0 ] || fail "no move may be issued on ambiguous ownership"
  [ "$(order_now)" = $'ws-fm\nws-A\nws-c1' ] || fail "the order must be untouched"
  pass "reconcile: a workspace claimed by two tasks fails closed before any move is planned"
}

test_reconcile_unreadable_list_fails_closed() {
  contig_case listfail $'ws-fm\nws-rn'
  write_owned_meta "$CASE_STATE" release-notes ws-rn ws-fm
  RECONCILE_OUT=$(PATH="$FB:$PATH" \
    FM_FAKE_ORDER="$CASE_ORDER" FM_FAKE_CLI_LOG="$CASE_CLI_LOG" \
    FM_FAKE_MOVE_LOG="$CASE_MOVE_LOG" FM_FAKE_SESSION="$SES" \
    FM_FAKE_SOCK="$TMP_ROOT/fake.sock" FM_FAKE_LIST_FAIL=1 \
    FM_BACKEND_HERDR_MOVE_WRITER="$FB/fake-move" \
    fm_backend_herdr_contiguity_reconcile "$SES" "$CASE_STATE" 2>&1) && \
    fail "an unreadable workspace list must fail the reconcile"
  [ "$(move_count)" = 0 ] || fail "no move may be issued when the live order cannot be read"
  assert_contains "$RECONCILE_OUT" "cannot read the workspace list" "the abort must be logged clearly"
  pass "reconcile: an unreadable workspace list fails closed with no move issued"
}

test_reconcile_no_owned_edges_is_a_noop() {
  contig_case noop $'ws-fm\nws-m1'
  run_reconcile "$CASE_STATE" || fail "an edge-less reconcile must succeed"
  [ "$(move_count)" = 0 ] || fail "no owned workspaces must mean zero moves"
  [ ! -s "$CASE_CLI_LOG" ] || fail "no owned workspaces must mean zero CLI calls, got:"$'\n'"$(cat "$CASE_CLI_LOG")"
  pass "reconcile: a home with no owned child workspaces is a healthy no-op (zero CLI calls, zero moves)"
}

test_workspace_move_validates_arguments() {
  fm_backend_herdr_workspace_move "$SES" "" 1 >/dev/null 2>&1 && fail "an empty workspace id must be refused"
  fm_backend_herdr_workspace_move "$SES" ws-x "not-a-number" >/dev/null 2>&1 && fail "a non-numeric index must be refused"
  fm_backend_herdr_workspace_move "$SES" ws-x "-1" >/dev/null 2>&1 && fail "a negative index must be refused"
  pass "workspace_move: refuses an empty workspace id and a non-numeric or negative insert index"
}

test_target_canonical_render_direct_crews_before_secondmates
test_target_multi_supervisor_contiguous_subtrees_stable_order
test_target_preserves_unrelated_manual_workspace_order
test_target_skips_missing_child_and_missing_parent_groups
test_target_sibling_order_pinned_by_reference_snapshot
test_target_aborts_on_ambiguous_ownership
test_next_move_converged_is_empty
test_next_move_pulls_new_crew_leftward
test_next_move_pushes_stray_crew_rightward
test_next_move_refuses_moving_unmanaged_workspaces
test_edges_extracts_only_owned_metas_for_the_session
test_edges_fail_closed_on_missing_ownership_fields
test_reconcile_canonical_render_single_move
test_reconcile_idempotent_no_churn
test_reconcile_converges_under_concurrent_create
test_reconcile_recovered_anchor_multi_stray_converges_stably
test_reconcile_tolerates_concurrent_finish
test_reconcile_socket_error_fails_closed
test_reconcile_missing_ownership_fails_closed_before_any_move
test_reconcile_ambiguous_ownership_fails_closed_before_any_move
test_reconcile_unreadable_list_fails_closed
test_reconcile_no_owned_edges_is_a_noop
test_workspace_move_validates_arguments

echo "# all herdr workspace-contiguity unit tests passed"
