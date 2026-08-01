#!/usr/bin/env bash
# tests/fm-task-delivery.test.sh - per-task delivery resolution: the
# no-mistakes-prod-only project policy, the task-owned resolution record, and the
# guarantee that generated instructions and recorded task state always agree.
#
# Everything here runs through the public interfaces an operator uses:
# bin/fm-task-delivery.sh, bin/fm-brief.sh, bin/fm-spawn.sh, bin/fm-project-mode.sh,
# and bin/fm-home-seed.sh. The spawn cases drive a real fm-spawn against a fake
# tmux/treehouse and a real git worktree, so brief text and state/<id>.meta are
# compared as they are actually produced.
set -u

# shellcheck source=tests/secondmate-helpers.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/secondmate-helpers.sh"

TMP_ROOT=$(fm_test_tmproot fm-task-delivery)
export FM_BACKEND=tmux

TD="$ROOT/bin/fm-task-delivery.sh"
BRIEF_SH="$ROOT/bin/fm-brief.sh"
SPAWN="$ROOT/bin/fm-spawn.sh"
MODE_SH="$ROOT/bin/fm-project-mode.sh"

# A fake tmux that logs every call (so "no endpoint was created" is assertable)
# and reports the worktree as the pane's cwd, plus exit-0 stubs for the tools a
# spawn shells out to.
make_task_fakebin() {
  local dir=$1 fakebin
  fakebin=$(fm_fakebin "$dir")
  cat > "$fakebin/tmux" <<'SH'
#!/usr/bin/env bash
set -u
printf '%s\n' "$*" >> "${FM_FAKE_TMUX_LOG:-/dev/null}"
case "$*" in
  *"#{pane_current_path}"*) printf '%s\n' "${FM_FAKE_PANE_PATH:-}"; exit 0 ;;
esac
case "${1:-}" in
  display-message) printf 'firstmate\n'; exit 0 ;;
  list-windows) exit 0 ;;
  has-session|new-session|new-window|kill-window) exit 0 ;;
  send-keys) exit 0 ;;
esac
exit 0
SH
  chmod +x "$fakebin/tmux"
  fm_fake_exit0 "$fakebin" treehouse gh-axi gh
  printf '%s\n' "$fakebin"
}

# One firstmate home whose registry covers every mode plus the conditional policy,
# with and without the orthogonal +yolo posture. Echoes the home path.
make_home() {
  local home="$TMP_ROOT/$1"
  mkdir -p "$home/data" "$home/state" "$home/config" "$home/projects"
  cat > "$home/data/projects.md" <<'REG'
# Projects

- flat - plain project (added 2026-06-22)
- fast [direct-PR] - direct-PR project (added 2026-06-22)
- yolofast [direct-PR +yolo] - direct-PR project with standing authority (added 2026-06-22)
- keep [local-only] - local-only project (added 2026-06-22)
- split [no-mistakes-prod-only] - conditional policy project (added 2026-06-22)
- splityolo [no-mistakes-prod-only +yolo] - conditional policy with standing authority (added 2026-06-22)
REG
  printf 'codex\n' > "$home/config/crew-harness"
  printf '%s\n' "$home"
}

# A real git project under the home, named exactly as the registry entry, so the
# project name fm-spawn derives from the path resolves in the registry.
make_project() {
  local home=$1 project=$2
  [ -d "$home/projects/$project" ] || fm_git_init_commit "$home/projects/$project"
}

# A fresh isolated worktree of <project> for one task. Echoes its path.
make_task_worktree() {
  local home=$1 project=$2 id=$3 wt="$TMP_ROOT/wt-$3"
  git -C "$home/projects/$project" worktree add --quiet -b "wt-$id" "$wt"
  printf '%s\n' "$wt"
}

td() {  # <home> <args...>
  local home=$1
  shift
  FM_ROOT_OVERRIDE='' FM_HOME="$home" FM_STATE_OVERRIDE="$home/state" \
    FM_DATA_OVERRIDE="$home/data" "$TD" "$@" 2>&1
}

run_brief() {  # <home> <args...>
  local home=$1
  shift
  FM_ROOT_OVERRIDE='' FM_HOME="$home" FM_STATE_OVERRIDE="$home/state" \
    FM_DATA_OVERRIDE="$home/data" "$BRIEF_SH" "$@" 2>&1
}

run_spawn() {  # <home> <project> <id> <worktree> [extra spawn args...]
  local home=$1 project=$2 id=$3 wt=$4
  shift 4
  FM_ROOT_OVERRIDE='' FM_HOME="$home" FM_STATE_OVERRIDE="$home/state" \
    FM_DATA_OVERRIDE="$home/data" FM_PROJECTS_OVERRIDE="$home/projects" \
    FM_CONFIG_OVERRIDE="$home/config" FM_SPAWN_NO_GUARD=1 TMUX="fake,1,0" \
    FM_FAKE_PANE_PATH="$wt" FM_FAKE_TMUX_LOG="$home/tmux.log" \
    PATH="$FAKEBIN:$PATH" \
    "$SPAWN" "$id" "$home/projects/$project" "$@" 2>&1
}

# Scaffold and dispatch one ship task end to end. Echoes "<brief> <meta>".
dispatch_ship() {  # <home> <project> <id>
  local home=$1 project=$2 id=$3 wt out
  make_project "$home" "$project"
  wt=$(make_task_worktree "$home" "$project" "$id")
  out=$(run_brief "$home" "$id" "$project") || fail "brief scaffold failed for $id: $out"
  out=$(run_spawn "$home" "$project" "$id" "$wt") || fail "spawn failed for $id: $out"
  printf '%s\n' "$out"
}

meta_field() {  # <home> <id> <key>
  grep "^$3=" "$1/state/$2.meta" | cut -d= -f2-
}

FAKEBIN=$(make_task_fakebin "$TMP_ROOT/fake")

# --- the record and its resolution ------------------------------------------

# Every accepted explicit selection round-trips: an operator names a concrete mode
# and yolo value, and resolve reports exactly that, for all three modes and both
# authority values, regardless of the project's own posture.
test_explicit_selection_round_trips() {
  local home label project mode yolo id out
  home=$(make_home explicit)
  while IFS='|' read -r label project mode yolo; do
    [ -n "$label" ] || continue
    id="explicit-$label"
    out=$(td "$home" set "$id" "$project" --mode "$mode" --yolo "$yolo") \
      || fail "$label: set was refused: $out"
    assert_contains "$out" "mode=$mode yolo=$yolo" "$label: set did not confirm the selection"
    out=$(td "$home" resolve "$id" ship "$project") || fail "$label: resolve failed: $out"
    [ "$out" = "$mode $yolo" ] || fail "$label: resolve returned '$out', expected '$mode $yolo'"
  done <<'ROWS'
pipeline-off|flat|no-mistakes|off
pipeline-on|flat|no-mistakes|on
direct-off|flat|direct-PR|off
direct-on|flat|direct-PR|on
local-off|flat|local-only|off
local-on|flat|local-only|on
over-fast|fast|no-mistakes|on
over-policy|split|local-only|off
ROWS
  pass "an explicit task selection records and resolves every mode and both authority values"
}

# The conditional policy resolves from the surface classification alone: only
# genuinely internal-only work takes the direct PR, and authority stays orthogonal
# - it comes from the project posture unless the operator states it.
test_conditional_policy_resolution() {
  local home label project surface expect_mode expect_yolo extra id out
  home=$(make_home conditional)
  while IFS='|' read -r label project surface extra expect_mode expect_yolo; do
    [ -n "$label" ] || continue
    id="cond-$label"
    # shellcheck disable=SC2086  # extra is an intentional optional flag pair
    out=$(td "$home" set "$id" "$project" --surface "$surface" $extra) \
      || fail "$label: set was refused: $out"
    assert_contains "$out" "surface=$surface mode=$expect_mode yolo=$expect_yolo" \
      "$label: policy resolved to the wrong concrete values"
    out=$(td "$home" resolve "$id" ship "$project") || fail "$label: resolve failed: $out"
    [ "$out" = "$expect_mode $expect_yolo" ] \
      || fail "$label: resolve returned '$out', expected '$expect_mode $expect_yolo'"
  done <<'ROWS'
internal|split|internal-only||direct-PR|off
product|split|product-facing||no-mistakes|off
uncertain|split|mixed-or-uncertain||no-mistakes|off
internal-standing|splityolo|internal-only||direct-PR|on
product-standing|splityolo|product-facing||no-mistakes|on
internal-stated-authority|split|internal-only|--yolo on|direct-PR|on
product-withheld-authority|splityolo|product-facing|--yolo off|no-mistakes|off
ROWS
  pass "the conditional policy resolves internal-only to direct-PR and everything else to the pipeline, with yolo orthogonal"
}

# With no task record, resolution is exactly what the project registry says, for
# every legacy mode - including the warn-and-default for an unregistered project.
test_project_default_is_unchanged_without_a_record() {
  local home project expect out
  home=$(make_home defaults)
  while IFS='|' read -r project expect; do
    [ -n "$project" ] || continue
    out=$(FM_ROOT_OVERRIDE='' FM_HOME="$home" "$MODE_SH" "$project" 2>/dev/null)
    [ "$out" = "$expect" ] || fail "registry for $project returned '$out', expected '$expect'"
    out=$(td "$home" resolve "task-$project" ship "$project" 2>/dev/null) \
      || fail "resolve without a record failed for $project"
    [ "$out" = "$expect" ] \
      || fail "resolve without a record returned '$out' for $project, expected the registry's '$expect'"
  done <<'ROWS'
flat|no-mistakes off
fast|direct-PR off
yolofast|direct-PR on
keep|local-only off
ROWS
  out=$(td "$home" resolve task-unknown ship never-registered 2>&1) \
    || fail "resolve did not fall back for an unregistered project"
  assert_contains "$out" 'not in registry' "resolve dropped fm-project-mode's own warning"
  assert_contains "$out" 'no-mistakes off' "resolve dropped fm-project-mode's safe default"
  pass "without a task record, resolution is the project registry's own answer, warnings included"
}

# --- refusals before any mutation -------------------------------------------

# A conditional-policy ship task must be classified before it exists anywhere: no
# brief, no task directory, no worktree, no endpoint, no metadata.
test_conditional_policy_ship_refuses_before_mutation() {
  local home out wt
  home=$(make_home cond-refuse)
  make_project "$home" split
  : > "$home/tmux.log"
  out=$(run_brief "$home" needs-surface-z1 split) && fail "brief was scaffolded without a surface classification"
  assert_contains "$out" 'needs an explicit surface classification' "brief refusal did not name the missing classification"
  assert_absent "$home/data/needs-surface-z1" "refused brief still created the task directory"

  # Reach spawn with a brief in hand by classifying, briefing, then removing the
  # record: the dispatch path must refuse on its own, not trust the brief.
  td "$home" set needs-surface-z2 split --surface product-facing >/dev/null \
    || fail "classification was refused"
  run_brief "$home" needs-surface-z2 split >/dev/null || fail "brief scaffold failed after classification"
  rm -f "$home/state/needs-surface-z2.delivery"
  wt=$(make_task_worktree "$home" split needs-surface-z2)
  out=$(run_spawn "$home" split needs-surface-z2 "$wt") && fail "spawn dispatched an unclassified conditional-policy task"
  assert_contains "$out" 'needs an explicit surface classification' "spawn refusal did not name the missing classification"
  assert_absent "$home/state/needs-surface-z2.meta" "refused spawn still wrote task metadata"
  assert_no_grep 'new-window' "$home/tmux.log" "refused spawn still created an endpoint"
  pass "a conditional-policy ship task refuses in both brief and dispatch before anything is created"
}

# Tampered, partial, or hostile records are refused by both consumers, and the
# refusal happens before a brief or an endpoint exists.
test_unusable_records_refuse_before_mutation() {
  local home label body id out wt
  home=$(make_home corrupt)
  make_project "$home" flat
  while IFS='|' read -r label body; do
    [ -n "$label" ] || continue
    id="corrupt-$label"
    printf '%b' "$body" > "$home/state/$id.delivery"
    : > "$home/tmux.log"
    out=$(run_brief "$home" "$id" flat) && fail "$label: brief accepted an unusable record"
    assert_contains "$out" 'error:' "$label: brief refusal printed no reason"
    assert_absent "$home/data/$id" "$label: refused brief created the task directory"
  done <<'ROWS'
short|task=corrupt-short\npolicy=no-mistakes\nsurface=none\nmode=direct-PR\n
empty|
unknown-mode|task=corrupt-unknown-mode\npolicy=no-mistakes\nsurface=none\nmode=turbo\nyolo=on\n
unknown-yolo|task=corrupt-unknown-yolo\npolicy=no-mistakes\nsurface=none\nmode=direct-PR\nyolo=maybe\n
unknown-policy|task=corrupt-unknown-policy\npolicy=whatever\nsurface=none\nmode=direct-PR\nyolo=on\n
wrong-identity|task=some-other-task\npolicy=no-mistakes\nsurface=none\nmode=direct-PR\nyolo=on\n
duplicate-key|task=corrupt-duplicate-key\npolicy=no-mistakes\nsurface=none\nmode=direct-PR\nyolo=on\nmode=local-only\n
reordered|policy=no-mistakes\ntask=corrupt-reordered\nsurface=none\nmode=direct-PR\nyolo=on\n
surface-without-policy|task=corrupt-surface-without-policy\npolicy=no-mistakes\nsurface=internal-only\nmode=direct-PR\nyolo=on\n
surface-mode-conflict|task=corrupt-surface-mode-conflict\npolicy=no-mistakes-prod-only\nsurface=internal-only\nmode=no-mistakes\nyolo=on\n
ROWS

  # A record that is not a plain file is never authority, whatever it points at.
  printf 'task=corrupt-target\npolicy=no-mistakes\nsurface=none\nmode=direct-PR\nyolo=on\n' > "$TMP_ROOT/planted.delivery"
  ln -s "$TMP_ROOT/planted.delivery" "$home/state/corrupt-symlink.delivery"
  out=$(run_brief "$home" corrupt-symlink flat) && fail "brief followed a symlinked record"
  assert_contains "$out" 'symlink' "symlink refusal did not name the symlink"
  mkdir -p "$home/state/corrupt-dir.delivery"
  out=$(run_brief "$home" corrupt-dir flat) && fail "brief accepted a directory as a record"
  assert_contains "$out" 'not a regular file' "directory refusal did not name the file type"

  # The same refusal holds at dispatch, before any endpoint or metadata exists.
  td "$home" set spawn-corrupt-z1 flat --mode direct-PR --yolo on >/dev/null || fail "set failed"
  run_brief "$home" spawn-corrupt-z1 flat >/dev/null || fail "brief scaffold failed"
  printf 'task=spawn-corrupt-z1\npolicy=no-mistakes\nsurface=none\nmode=turbo\nyolo=on\n' \
    > "$home/state/spawn-corrupt-z1.delivery"
  wt=$(make_task_worktree "$home" flat spawn-corrupt-z1)
  : > "$home/tmux.log"
  out=$(run_spawn "$home" flat spawn-corrupt-z1 "$wt") && fail "spawn accepted a tampered record"
  assert_contains "$out" 'unknown mode' "spawn refusal did not name the bad value"
  assert_absent "$home/state/spawn-corrupt-z1.meta" "refused spawn wrote task metadata"
  assert_no_grep 'new-window' "$home/tmux.log" "refused spawn created an endpoint"
  pass "malformed, partial, conflicting, and non-regular records refuse before any brief or endpoint"
}

# The write interface refuses everything that would leave two disagreeing answers.
test_set_refuses_conflicting_and_duplicate_input() {
  local home out
  home=$(make_home setrefuse)
  while IFS='|' read -r label expect args; do
    [ -n "$label" ] || continue
    # shellcheck disable=SC2086  # args is an intentional argument list
    out=$(td "$home" set $args) && fail "$label: set accepted it"
    assert_contains "$out" "$expect" "$label: refusal did not explain itself"
    assert_absent "$home/state/${args%% *}.delivery" "$label: refused set left a record"
  done <<'ROWS'
both-inputs|pass exactly one|set-both-z1 split --surface internal-only --mode direct-PR
neither-input|pass --surface|set-neither-z2 flat
partial-mode|--mode requires --yolo|set-partial-z3 flat --mode direct-PR
bad-mode|--mode must be|set-badmode-z4 flat --mode turbo --yolo on
bad-yolo|--yolo must be|set-badyolo-z5 flat --mode direct-PR --yolo maybe
bad-surface|--surface must be|set-badsurface-z6 split --surface sideways
surface-on-flat|applies only to no-mistakes-prod-only|set-flatsurface-z7 flat --surface internal-only
surface-on-local|applies only to no-mistakes-prod-only|set-localsurface-z8 keep --surface internal-only
ROWS

  td "$home" set dup-z9 flat --mode direct-PR --yolo on >/dev/null || fail "first set was refused"
  out=$(td "$home" set dup-z9 flat --mode local-only --yolo off) && fail "a second set overwrote the resolution"
  assert_contains "$out" 'already has a delivery resolution' "duplicate refusal did not explain itself"
  [ "$(td "$home" resolve dup-z9 ship flat)" = "direct-PR on" ] \
    || fail "the duplicate attempt changed the recorded resolution"

  td "$home" set late-z10 flat --mode direct-PR --yolo on >/dev/null || fail "set failed"
  run_brief "$home" late-z10 flat >/dev/null || fail "brief scaffold failed"
  rm -f "$home/state/late-z10.delivery"
  out=$(td "$home" set late-z10 flat --mode local-only --yolo off) && fail "set accepted a task that already has instructions"
  assert_contains "$out" 'already has instructions' "late-record refusal did not name the existing brief"
  pass "the write interface refuses conflicting, partial, duplicate, and out-of-order input"
}

# A resolution belongs to one task on one project. If the project's registered
# policy moved underneath it, shipping it anyway would apply the wrong contract.
test_policy_drift_refuses() {
  local home out
  home=$(make_home drift)
  td "$home" set drift-z1 flat --mode direct-PR --yolo on >/dev/null || fail "set failed"
  printf '%s\n' '- flat [no-mistakes-prod-only] - reclassified (added 2026-06-23)' >> "$home/data/projects.md"
  python3 - "$home/data/projects.md" <<'PY'
import sys
path = sys.argv[1]
lines = [l for l in open(path).read().splitlines() if not l.startswith('- flat - ')]
open(path, 'w').write("\n".join(lines) + "\n")
PY
  out=$(td "$home" resolve drift-z1 ship flat) && fail "resolve ignored the project's new policy"
  assert_contains "$out" 'is now registered' "drift refusal did not name the change"
  pass "a resolution recorded under one project policy refuses when that policy changed"
}

# Scouts and secondmates never ship through a project delivery mode, so a record
# on either is refused before its brief, its scratch copy, or its home exists.
test_scout_and_secondmate_refuse_a_record() {
  local home out wt
  home=$(make_home kinds)
  make_project "$home" flat
  td "$home" set scout-z1 flat --mode direct-PR --yolo on >/dev/null || fail "set failed"
  out=$(run_brief "$home" scout-z1 flat --scout) && fail "a scout brief accepted a delivery resolution"
  assert_contains "$out" 'scout' "scout refusal did not name the kind"
  assert_absent "$home/data/scout-z1" "refused scout brief created the task directory"

  run_brief "$home" scout-z2 flat --scout >/dev/null || fail "unrecorded scout brief failed"
  printf 'task=scout-z2\npolicy=no-mistakes\nsurface=none\nmode=direct-PR\nyolo=on\n' \
    > "$home/state/scout-z2.delivery"
  wt=$(make_task_worktree "$home" flat scout-z2)
  : > "$home/tmux.log"
  out=$(run_spawn "$home" flat scout-z2 "$wt" --scout) && fail "a scout spawn accepted a delivery resolution"
  assert_absent "$home/state/scout-z2.meta" "refused scout spawn wrote task metadata"
  assert_no_grep 'new-window' "$home/tmux.log" "refused scout spawn created an endpoint"

  td "$home" set mate-z3 flat --mode direct-PR --yolo on >/dev/null || fail "set failed"
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$home" FM_STATE_OVERRIDE="$home/state" \
    FM_DATA_OVERRIDE="$home/data" FM_SECONDMATE_CHARTER='ops domain' \
    "$BRIEF_SH" mate-z3 --secondmate flat 2>&1) && fail "a secondmate charter accepted a delivery resolution"
  assert_contains "$out" 'secondmate' "secondmate refusal did not name the kind"
  assert_absent "$home/data/mate-z3" "refused charter created the task directory"

  : > "$home/tmux.log"
  mkdir -p "$TMP_ROOT/kinds-subhome"
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$home" FM_STATE_OVERRIDE="$home/state" \
    FM_DATA_OVERRIDE="$home/data" FM_PROJECTS_OVERRIDE="$home/projects" \
    FM_CONFIG_OVERRIDE="$home/config" FM_SPAWN_NO_GUARD=1 TMUX="fake,1,0" \
    FM_FAKE_TMUX_LOG="$home/tmux.log" PATH="$FAKEBIN:$PATH" \
    "$SPAWN" mate-z3 "$TMP_ROOT/kinds-subhome" --secondmate 2>&1) \
    && fail "a secondmate spawn accepted a delivery resolution"
  assert_contains "$out" 'before provisioning or launching its home' "secondmate spawn refusal was not the delivery gate"
  assert_absent "$home/state/mate-z3.meta" "refused secondmate spawn wrote task metadata"
  assert_absent "$TMP_ROOT/kinds-subhome/state" "refused secondmate spawn provisioned the home"
  pass "a delivery resolution on a scout or secondmate refuses before its brief, copy, home, or endpoint"
}

# --- instructions and recorded state agree ----------------------------------

# The load-bearing guarantee: whatever the task resolves to, the worker's
# instructions and the durable task record say the same thing.
test_brief_and_metadata_agree() {
  local home label project setup id proof mode yolo out
  home=$(make_home agree)
  while IFS='|' read -r label project setup proof mode yolo; do
    [ -n "$label" ] || continue
    id="agree-$label"
    if [ -n "$setup" ]; then
      # shellcheck disable=SC2086  # setup is an intentional argument list
      td "$home" set "$id" "$project" $setup >/dev/null || fail "$label: set failed"
    fi
    dispatch_ship "$home" "$project" "$id" >/dev/null
    assert_grep "$proof" "$home/data/$id/brief.md" "$label: instructions do not match the resolved mode"
    out=$(meta_field "$home" "$id" mode)
    [ "$out" = "$mode" ] || fail "$label: recorded mode is '$out', expected '$mode'"
    out=$(meta_field "$home" "$id" yolo)
    [ "$out" = "$yolo" ] || fail "$label: recorded yolo is '$out', expected '$yolo'"
  done <<'ROWS'
override-direct|flat|--mode direct-PR --yolo on|ships **direct-PR**|direct-PR|on
override-local|flat|--mode local-only --yolo off|ships **local-only**|local-only|off
override-pipeline|fast|--mode no-mistakes --yolo off|/no-mistakes to validate|no-mistakes|off
policy-internal|split|--surface internal-only|ships **direct-PR**|direct-PR|off
policy-product|split|--surface product-facing|/no-mistakes to validate|no-mistakes|off
policy-internal-standing|splityolo|--surface internal-only|ships **direct-PR**|direct-PR|on
default-pipeline|flat||/no-mistakes to validate|no-mistakes|off
default-direct|fast||ships **direct-PR**|direct-PR|off
default-yolo|yolofast||ships **direct-PR**|direct-PR|on
default-local|keep||ships **local-only**|local-only|off
ROWS
  pass "generated instructions and recorded task state agree for overrides, policy resolutions, and project defaults"
}

# A local-only project stays local-only: it is never treated as a remote-backed
# conditional policy, and no classification can turn it into a PR path.
test_local_only_stays_local_only() {
  local home out
  home=$(make_home localonly)
  out=$(td "$home" resolve local-z1 ship keep) || fail "resolve failed for a local-only project"
  [ "$out" = "local-only off" ] || fail "a local-only project resolved to '$out'"
  out=$(td "$home" set local-z1 keep --surface internal-only) && fail "a local-only project accepted a surface classification"
  assert_contains "$out" 'applies only to no-mistakes-prod-only' "local-only refusal did not explain itself"
  dispatch_ship "$home" keep local-z2 >/dev/null
  assert_grep 'ships **local-only**' "$home/data/local-z2/brief.md" "local-only instructions changed"
  assert_grep 'no remote, no PR, no pipeline' "$home/data/local-z2/brief.md" "local-only instructions lost their no-remote contract"
  [ "$(meta_field "$home" local-z2 mode)" = local-only ] || fail "local-only task did not record mode=local-only"
  pass "a local-only project stays local-only and refuses a conditional-policy classification"
}

# Each pair of a batch resolves only its own task.
test_batch_preserves_each_task_resolution() {
  local home out wt_a wt_b
  home=$(make_home batch)
  make_project "$home" flat
  make_project "$home" split
  td "$home" set batch-a-z1 flat --mode direct-PR --yolo on >/dev/null || fail "set failed for batch-a-z1"
  td "$home" set batch-b-z2 split --surface product-facing >/dev/null || fail "set failed for batch-b-z2"
  run_brief "$home" batch-a-z1 flat >/dev/null || fail "brief failed for batch-a-z1"
  run_brief "$home" batch-b-z2 split >/dev/null || fail "brief failed for batch-b-z2"
  wt_a=$(make_task_worktree "$home" flat batch-a-z1)
  wt_b=$(make_task_worktree "$home" split batch-b-z2)
  # One fake pane cwd per spawn is impossible in a single batch call, so each pair
  # is dispatched through the same batch entry point with its own pane path.
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$home" FM_STATE_OVERRIDE="$home/state" \
    FM_DATA_OVERRIDE="$home/data" FM_PROJECTS_OVERRIDE="$home/projects" \
    FM_CONFIG_OVERRIDE="$home/config" FM_SPAWN_NO_GUARD=1 TMUX="fake,1,0" \
    FM_FAKE_PANE_PATH="$wt_a" FM_FAKE_TMUX_LOG="$home/tmux.log" PATH="$FAKEBIN:$PATH" \
    "$SPAWN" "batch-a-z1=$home/projects/flat" 2>&1) || fail "batch dispatch failed: $out"
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$home" FM_STATE_OVERRIDE="$home/state" \
    FM_DATA_OVERRIDE="$home/data" FM_PROJECTS_OVERRIDE="$home/projects" \
    FM_CONFIG_OVERRIDE="$home/config" FM_SPAWN_NO_GUARD=1 TMUX="fake,1,0" \
    FM_FAKE_PANE_PATH="$wt_b" FM_FAKE_TMUX_LOG="$home/tmux.log" PATH="$FAKEBIN:$PATH" \
    "$SPAWN" "batch-b-z2=$home/projects/split" 2>&1) || fail "batch dispatch failed: $out"
  [ "$(meta_field "$home" batch-a-z1 mode)" = direct-PR ] || fail "batch task a did not keep its own mode"
  [ "$(meta_field "$home" batch-a-z1 yolo)" = on ] || fail "batch task a did not keep its own authority"
  [ "$(meta_field "$home" batch-b-z2 mode)" = no-mistakes ] || fail "batch task b took another task's mode"
  [ "$(meta_field "$home" batch-b-z2 yolo)" = off ] || fail "batch task b took another task's authority"
  pass "batch dispatch resolves each task's own delivery selection"
}

# --- project registration and secondmate seeding ----------------------------

# What a newly registered remote-backed project resolves to, and the guarantee
# that no existing entry changes meaning: the new default lives in the registry
# line intake writes, never in a parser that reinterprets old records.
test_new_project_default_registration() {
  local home label line expect out
  home="$TMP_ROOT/newdefault"
  mkdir -p "$home/data" "$home/state"
  : > "$home/data/projects.md"
  while IFS='|' read -r label line expect; do
    [ -n "$label" ] || continue
    printf '%s\n' "$line" >> "$home/data/projects.md"
    out=$(FM_ROOT_OVERRIDE='' FM_HOME="$home" "$MODE_SH" "$label" 2>/dev/null)
    [ "$out" = "$expect" ] || fail "$label resolved to '$out', expected '$expect'"
  done <<'ROWS'
newremote|- newremote [no-mistakes-prod-only] - newly added remote-backed project (added 2026-06-22)|no-mistakes-prod-only off
legacy|- legacy - registered before the policy existed (added 2024-01-02)|no-mistakes off
legacyyolo|- legacyyolo [+yolo] - legacy entry with standing authority (added 2024-01-02)|no-mistakes on
pinnedfast|- pinnedfast [direct-PR] - explicitly chosen alternative (added 2026-06-22)|direct-PR off
pinnedlocal|- pinnedlocal [local-only] - explicitly chosen alternative (added 2026-06-22)|local-only off
newremoteyolo|- newremoteyolo [no-mistakes-prod-only +yolo] - policy with explicit standing authority (added 2026-06-22)|no-mistakes-prod-only on
ROWS
  pass "a default-registered remote-backed project resolves to the conditional policy while legacy and explicit entries keep their meaning"
}


# Registering the policy is a project-level act that never classifies a change,
# and a seeded secondmate home keeps the policy and still gets the pipeline setup.
test_seeding_preserves_the_policy() {
  local home subhome fakebin out
  home="$TMP_ROOT/seed-home"
  subhome="$TMP_ROOT/seed-subhome"
  mkdir -p "$home/projects" "$home/data" "$home/state"
  fm_git_init_commit "$home/projects/split"
  fm_git_add_origin "$home/projects/split" "$TMP_ROOT/remotes/split.git"
  fm_git_init_commit "$home/projects/unlisted"
  fm_git_add_origin "$home/projects/unlisted" "$TMP_ROOT/remotes/unlisted.git"
  printf '%s\n' '- split [no-mistakes-prod-only +yolo] - conditional policy project (added 2026-06-22)' \
    > "$home/data/projects.md"
  git clone --quiet "$ROOT" "$subhome"
  fakebin=$(make_fake_no_mistakes "$TMP_ROOT/seed-fake")
  scaffold_secondmate_charter "$home" design 'delivery domain' split unlisted || fail "charter scaffold failed"

  out=$(PATH="$fakebin:$PATH" FM_HOME="$home" "$ROOT/bin/fm-home-seed.sh" design "$subhome" split unlisted 2>&1) \
    || fail "seeding refused a conditional-policy project: $out"
  assert_present "$subhome/projects/split/.git" "the conditional-policy project was not cloned"
  assert_present "$subhome/projects/split/.no-mistakes-init" "the conditional-policy clone was not initialized for the pipeline"
  assert_present "$subhome/projects/split/.no-mistakes-doctor" "the conditional-policy clone was not doctored"
  assert_grep '[no-mistakes-prod-only +yolo]' "$subhome/data/projects.md" "the seeded registry lost the policy"
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$subhome" "$ROOT/bin/fm-project-mode.sh" split)
  [ "$out" = "no-mistakes-prod-only on" ] || fail "the seeded home resolved the policy as '$out'"
  assert_absent "$subhome/state/split.delivery" "seeding invented a task resolution at registration time"

  # Seeding an already-registered project is not new-project intake: an entry the
  # source home never bracketed keeps its legacy meaning in the secondmate home.
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$subhome" "$ROOT/bin/fm-project-mode.sh" unlisted 2>/dev/null)
  [ "$out" = "no-mistakes off" ] || fail "seeding migrated an unbracketed project to '$out'"
  out=$(grep -- '- unlisted' "$subhome/data/projects.md")
  case "$out" in
    *'['*) fail "seeding gave an unbracketed legacy project a policy it never had: $out" ;;
  esac

  # The seeded home keeps the direct internal-only path the policy promises.
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$subhome" "$TD" set seeded-internal-z1 split --surface internal-only 2>&1) \
    || fail "the seeded home refused an internal-only classification: $out"
  out=$(FM_ROOT_OVERRIDE='' FM_HOME="$subhome" "$TD" resolve seeded-internal-z1 ship split 2>&1) \
    || fail "the seeded home could not resolve a classified task: $out"
  [ "$out" = "direct-PR on" ] || fail "the seeded home resolved internal-only work to '$out'"
  pass "seeding clones, initializes, and preserves a conditional-policy project and its internal path without classifying anything"
}

test_explicit_selection_round_trips
test_conditional_policy_resolution
test_project_default_is_unchanged_without_a_record
test_conditional_policy_ship_refuses_before_mutation
test_unusable_records_refuse_before_mutation
test_set_refuses_conflicting_and_duplicate_input
test_policy_drift_refuses
test_scout_and_secondmate_refuse_a_record
test_brief_and_metadata_agree
test_local_only_stays_local_only
test_batch_preserves_each_task_resolution
test_new_project_default_registration
test_seeding_preserves_the_policy

echo "# all fm-task-delivery tests passed"
