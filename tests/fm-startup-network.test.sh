#!/usr/bin/env bash
# tests/fm-startup-network.test.sh - behavior tests for bin/fm-startup-network.sh,
# the deferred network stage a session start launches instead of running its
# network work on the blocking path.
#
# The session-start suite proves the digest no longer waits and that the deferred
# sweeps still land. This suite pins the stage's own contract, whose whole job is
# to make deferral safe:
#   - `start` returns immediately and does not hold the caller's stdout open,
#     which is what would strand a session-open hook behind the worker
#   - a live inline-print claim suppresses the wake, and no claim (or a dead one)
#     produces it, so a result surfaces exactly once
#   - mutating sweeps are refused when the fleet lock no longer names the session
#     that requested them, and the refusal is reported rather than silent
#   - the aggregate bound turns a wedged sweep into an actionable line
#   - an abandoned `running` record is reported as needing a rerun rather than
#     staying "in progress" forever
#   - single-flight: a second `start` never launches a competing worker
set -u

# shellcheck source=tests/lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

TMP_ROOT=$(fm_test_tmproot fm-startup-network-tests)
FM_TEST_CLEANUP_DIRS+=("$TMP_ROOT")
trap fm_test_cleanup EXIT

# new_world <name>: an FM_HOME plus a fake code root whose bin/ is a real
# firstmate bin/ except for fm-bootstrap.sh, which is replaced by a scriptable
# stand-in. The stage's contract is about WHEN and WHETHER the network half runs
# and how its result is published; bin/fm-bootstrap.sh's own behavior is owned by
# tests/fm-bootstrap.test.sh, so pinning it here would duplicate that owner and
# make these assertions depend on unrelated tool detection.
new_world() {
  local name=$1 w home root
  w="$TMP_ROOT/$name"
  home="$w/home"
  root="$w/root"
  mkdir -p "$home/state" "$root/bin"
  for f in "$ROOT"/bin/*.sh; do
    ln -s "$f" "$root/bin/$(basename "$f")"
  done
  rm -f "$root/bin/fm-bootstrap.sh"
  cat > "$root/bin/fm-bootstrap.sh" <<'SH'
#!/usr/bin/env bash
# Scriptable stand-in: records how it was invoked, then behaves as the test asks.
set -u
printf 'network=%s detect_only=%s\n' \
  "${FM_BOOTSTRAP_NETWORK:-all}" "${FM_BOOTSTRAP_DETECT_ONLY:-0}" \
  >> "${FM_FAKE_BOOTSTRAP_LOG:?}"
[ -z "${FM_FAKE_BOOTSTRAP_SLEEP:-}" ] || sleep "$FM_FAKE_BOOTSTRAP_SLEEP"
[ -z "${FM_FAKE_BOOTSTRAP_OUT:-}" ] || printf '%s\n' "$FM_FAKE_BOOTSTRAP_OUT"
exit "${FM_FAKE_BOOTSTRAP_RC:-0}"
SH
  chmod +x "$root/bin/fm-bootstrap.sh"
  printf '%s|%s|%s\n' "$home" "$root" "$w/bootstrap.log"
}

# The detached worker records itself a moment after `start` returns - that gap is
# the whole point of not blocking - so a test that wants to observe the worker
# waits for its record rather than assuming instant publication.
await_worker_record() {  # <home>
  local home=$1 waited=0
  while [ ! -s "$home/state/.startup-network.status" ] && [ "$waited" -lt 100 ]; do
    sleep 0.1
    waited=$((waited + 1))
  done
  [ -s "$home/state/.startup-network.status" ] || fail "the detached worker never recorded itself"
}

run_stage() {  # <home> <root> <args...>
  local home=$1 root=$2
  shift 2
  FM_HOME="$home" FM_ROOT_OVERRIDE="$root" "$root/bin/fm-startup-network.sh" "$@"
}

# --- tests -------------------------------------------------------------------

# `start` is called from inside a session-open hook whose stdout the harness
# reads to EOF. A worker that inherited that pipe would hold the session open for
# exactly as long as the network work it was supposed to get off the critical
# path, so this asserts both halves: start returns fast, AND the pipe closes
# while the worker is still running.
test_start_returns_without_holding_the_callers_stdout() {
  local rec home root log started elapsed
  rec=$(new_world start-nonblocking)
  IFS='|' read -r home root log <<EOF
$rec
EOF
  printf '111111\n' > "$home/state/.lock"

  started=$(date +%s)
  # Command substitution reads to EOF, exactly like a hook harvesting hook output.
  FM_FAKE_BOOTSTRAP_LOG="$log" FM_FAKE_BOOTSTRAP_SLEEP=10 \
    run_stage "$home" "$root" start --locked 1 --harvest-pid $$ >/dev/null
  elapsed=$(( $(date +%s) - started ))

  [ "$elapsed" -lt 4 ] || fail "start blocked for ${elapsed}s behind a 10s worker"
  await_worker_record "$home"
  [ "$(run_stage "$home" "$root" report | head -1)" = "IN PROGRESS - the deferred network checks have not finished yet." ] \
    || fail "the worker was not actually still running: $(run_stage "$home" "$root" report)"
  run_stage "$home" "$root" wait 30 >/dev/null || fail "the worker never published"
  assert_grep 'network=only' "$log" "the worker did not run bootstrap's network-only phase"
  pass "fm-startup-network: start returns immediately and never holds the caller's stdout open"
}

# The claim handshake is what stops a result being both printed and queued. Both
# directions are decided here, without racing a digest.
test_a_live_claim_suppresses_the_wake_and_no_claim_produces_it() {
  local rec home root log
  rec=$(new_world claim-handshake)
  IFS='|' read -r home root log <<EOF
$rec
EOF
  printf '111111\n' > "$home/state/.lock"

  # A live claim: some session start is still composing and will print this.
  printf '%s\n' $$ > "$home/state/.startup-network.claim"
  FM_FAKE_BOOTSTRAP_LOG="$log" run_stage "$home" "$root" run --locked 1 --lock-pid 111111
  [ ! -s "$home/state/.wake-queue" ] \
    || fail "a result a live session start had claimed also queued a wake: $(cat "$home/state/.wake-queue")"

  # Harvest releases that claim, so the NEXT publication has nobody to print it.
  run_stage "$home" "$root" harvest --pid $$ >/dev/null
  assert_absent "$home/state/.startup-network.claim" "harvest did not release its own claim"
  FM_FAKE_BOOTSTRAP_LOG="$log" run_stage "$home" "$root" run --locked 1 --lock-pid 111111
  assert_grep 'check	startup-network' "$home/state/.wake-queue" \
    "an unclaimed result never reached the wake queue"

  # A claim whose session died is not a claim.
  : > "$home/state/.wake-queue"
  printf '999999999\n' > "$home/state/.startup-network.claim"
  FM_FAKE_BOOTSTRAP_LOG="$log" run_stage "$home" "$root" run --locked 1 --lock-pid 111111
  assert_grep 'check	startup-network' "$home/state/.wake-queue" \
    "a dead session's stale claim swallowed the result"
  assert_absent "$home/state/.startup-network.claim" "a dead claim was not reaped"
  pass "fm-startup-network: exactly one of the digest and the wake reports each result"
}

# The worker outlives the command that launched it. If another session took the
# lock meanwhile, running the mutating sweeps would sweep underneath that
# session, so they are refused - and the refusal is reported, not silent.
test_mutating_sweeps_are_refused_when_the_lock_changed_hands() {
  local rec home root log report
  rec=$(new_world lock-changed)
  IFS='|' read -r home root log <<EOF
$rec
EOF
  printf '222222\n' > "$home/state/.lock"

  FM_FAKE_BOOTSTRAP_LOG="$log" run_stage "$home" "$root" run --locked 1 --lock-pid 111111
  assert_grep 'network=only detect_only=1' "$log" \
    "the worker ran mutating sweeps for a lock it no longer held"
  report=$(run_stage "$home" "$root" report)
  assert_contains "$report" "NETWORK_CHECKS: the fleet lock was no longer held" \
    "the downgrade to a read-only probe was not reported"

  # The same worker with the lock intact does run them.
  : > "$log"
  FM_FAKE_BOOTSTRAP_LOG="$log" run_stage "$home" "$root" run --locked 1 --lock-pid 222222
  assert_grep 'network=only detect_only=0' "$log" \
    "the worker refused sweeps for the very session that still holds the lock"
  pass "fm-startup-network: mutating sweeps need the lock still to name the session that asked"
}

# The unbounded per-call network work is exactly what could wedge a startup. The
# stage carries one aggregate bound, and hitting it is an actionable line.
test_the_stage_bound_is_reported_not_swallowed() {
  local rec home root log report
  rec=$(new_world stage-bound)
  IFS='|' read -r home root log <<EOF
$rec
EOF
  printf '111111\n' > "$home/state/.lock"

  FM_FAKE_BOOTSTRAP_LOG="$log" FM_FAKE_BOOTSTRAP_SLEEP=20 FM_STARTUP_NETWORK_TIMEOUT=2 \
    run_stage "$home" "$root" run --locked 1 --lock-pid 111111
  report=$(run_stage "$home" "$root" report)
  assert_contains "$report" "NETWORK_CHECKS: hit the 2s bound before finishing" \
    "a wedged deferred stage was not reported: $report"
  assert_contains "$report" "fm-startup-network.sh run --locked 1" \
    "the timeout line did not say how to rerun the stage"
  assert_grep 'check	startup-network' "$home/state/.wake-queue" \
    "a timed-out stage did not surface to the agent"
  pass "fm-startup-network: an aggregate bound turns a wedged sweep into an actionable line"
}

# A digest that hits its own runtime bound kills its process group, taking the
# worker with it. The leftover `running` record must read as work to redo, not as
# work still in flight.
test_an_abandoned_run_reads_as_needing_a_rerun() {
  local rec home root log report
  rec=$(new_world abandoned)
  IFS='|' read -r home root log <<EOF
$rec
EOF
  cat > "$home/state/.startup-network.status" <<EOF
state=running
pid=999999999
started=$(date +%s)
locked=1
phases=probe,sweeps
EOF

  report=$(run_stage "$home" "$root" report)
  assert_contains "$report" "NETWORK_CHECKS: the deferred check worker stopped before publishing" \
    "an abandoned run still read as in progress: $report"
  assert_contains "$report" "dead-secondmate relaunch" \
    "the abandoned run did not name the checks that never completed"

  # A record older than the whole aggregate bound is abandoned even when its pid
  # happens to be alive again, so "in progress" can never become permanent.
  cat > "$home/state/.startup-network.status" <<EOF
state=running
pid=$$
started=$(( $(date +%s) - 400 ))
locked=1
phases=probe,sweeps
EOF
  assert_contains "$(FM_STARTUP_NETWORK_TIMEOUT=10 run_stage "$home" "$root" report)" \
    "NETWORK_CHECKS: the deferred check worker stopped before publishing" \
    "a record that outlived the stage bound still read as in progress"
  pass "fm-startup-network: an abandoned run reports as needing a rerun, never as in progress forever"
}

# Two session opens in quick succession must not run the same mutating sweeps
# concurrently against each other.
test_start_is_single_flight() {
  local rec home root log runs
  rec=$(new_world single-flight)
  IFS='|' read -r home root log <<EOF
$rec
EOF
  printf '111111\n' > "$home/state/.lock"

  FM_FAKE_BOOTSTRAP_LOG="$log" FM_FAKE_BOOTSTRAP_SLEEP=6 \
    run_stage "$home" "$root" start --locked 1 --harvest-pid $$
  await_worker_record "$home"
  FM_FAKE_BOOTSTRAP_LOG="$log" FM_FAKE_BOOTSTRAP_SLEEP=6 \
    run_stage "$home" "$root" start --locked 1 --harvest-pid $$
  run_stage "$home" "$root" wait 40 >/dev/null || fail "the worker never published"

  runs=$(grep -c 'network=only' "$log" || true)
  [ "$runs" -eq 1 ] || fail "a second start launched a competing worker ($runs runs): $(cat "$log")"
  pass "fm-startup-network: a second start never launches a competing worker"
}

test_start_returns_without_holding_the_callers_stdout
test_a_live_claim_suppresses_the_wake_and_no_claim_produces_it
test_mutating_sweeps_are_refused_when_the_lock_changed_hands
test_the_stage_bound_is_reported_not_swallowed
test_an_abandoned_run_reads_as_needing_a_rerun
test_start_is_single_flight

echo "# fm-startup-network.test.sh: all assertions passed"
