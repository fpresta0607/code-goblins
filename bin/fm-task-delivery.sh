#!/usr/bin/env bash
# Own one task's concrete delivery resolution: which mode it ships through and
# which yolo authority applies, recorded once at intake and consumed unchanged by
# brief generation and spawn so the two can never diverge.
#
# Usage:
#   fm-task-delivery.sh set <task-id> <project> --surface <surface> [--yolo <on|off>]
#   fm-task-delivery.sh set <task-id> <project> --mode <mode> --yolo <on|off>
#   fm-task-delivery.sh check <task-id> <ship|scout|secondmate> [<project>]
#   fm-task-delivery.sh resolve <task-id> <ship|scout> <project>
#
#   <mode>    no-mistakes | direct-PR | local-only
#   <surface> internal-only | product-facing | mixed-or-uncertain
#
# The record lives at state/<task-id>.delivery under the active firstmate home
# and holds exactly these five lines, in this order:
#   task=<task-id>
#   policy=<project policy at intake: no-mistakes|direct-PR|local-only|no-mistakes-prod-only>
#   surface=<surface|none>
#   mode=<concrete mode>
#   yolo=<on|off>
# Only concrete values are recorded. `policy` is provenance, never re-resolved
# authority: a project whose registered policy changed after intake refuses
# instead of silently shipping the task under a different contract.
#
# `set` writes that record once, atomically, through an exclusive link, and
# refuses a second write. There is deliberately no edit or clear verb: correcting
# a selection means removing state/<task-id>.delivery before the brief exists.
# It refuses when the task already has a brief or task metadata, because those
# were shaped by the resolution the record would be replacing.
#
# Two input shapes, mutually exclusive:
#   --surface  resolves the conditional `no-mistakes-prod-only` project policy for
#              this task. internal-only resolves to direct-PR; product-facing and
#              mixed-or-uncertain resolve to no-mistakes. It is refused on a project
#              whose policy is not no-mistakes-prod-only, where no classification
#              applies. Firstmate classifies the change itself and never infers
#              internal-only from file location or project name. Without --yolo the
#              project's own registered posture is recorded, so no authority is
#              assumed for any surface; firstmate passes --yolo explicitly when a
#              standing authority applies.
#   --mode     records a concrete task-specific captain instruction, which is
#              stronger than any project policy. --yolo is required with it so the
#              record is never partial, and mode stays orthogonal to yolo.
#
# `check` is the pre-mutation gate. It validates any existing record and refuses
# before a caller creates a brief, allocates a task copy, provisions a home,
# creates an endpoint, or writes metadata. It refuses a record on a scout or
# secondmate, whose deliverables never ship through a project delivery mode, and
# with <project> supplied it refuses a ship task on a no-mistakes-prod-only
# project that has no recorded resolution. Success is silent.
#
# `resolve` prints one line, "<mode> <yolo>", for a ship or scout task. It runs
# the same gate first, so a caller that resolves is never inconsistent with one
# that only checked. A ship task with a record prints that record's concrete
# values; without a record it prints the project registry's own mode and yolo,
# exactly as bin/fm-project-mode.sh reports them, including that script's stderr
# warning and safe default for an unregistered project or unknown mode. A scout
# never carries a resolution and reports the strictest concrete mode its project
# policy allows, because its deliverable is a report rather than a merge.
#
# Exit status: 0 accepted, 1 refused with a concrete reason on stderr, 2 usage.
set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  sed -n '2,${/^#/!q;p;}' "$0" | sed 's/^# \{0,1\}//'
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
esac

FM_HOME="${FM_HOME:-${FM_ROOT_OVERRIDE:-$(cd "$SCRIPT_DIR/.." && pwd)}}"
STATE="${FM_STATE_OVERRIDE:-$FM_HOME/state}"
DATA="${FM_DATA_OVERRIDE:-$FM_HOME/data}"

# shellcheck source=bin/fm-pr-lib.sh
. "$SCRIPT_DIR/fm-pr-lib.sh"

RECORD_TASK=
RECORD_POLICY=
RECORD_SURFACE=
RECORD_MODE=
RECORD_YOLO=

mode_valid() {
  case "${1-}" in
    no-mistakes|direct-PR|local-only) return 0 ;;
  esac
  return 1
}

policy_valid() {
  case "${1-}" in
    no-mistakes|direct-PR|local-only|no-mistakes-prod-only) return 0 ;;
  esac
  return 1
}

surface_valid() {
  case "${1-}" in
    internal-only|product-facing|mixed-or-uncertain) return 0 ;;
  esac
  return 1
}

yolo_valid() {
  case "${1-}" in
    on|off) return 0 ;;
  esac
  return 1
}

# The concrete mode a classified surface resolves to under the conditional policy.
mode_for_surface() {
  case "$1" in
    internal-only) printf '%s\n' direct-PR ;;
    *) printf '%s\n' no-mistakes ;;
  esac
}

require_task_id() {
  fm_task_id_creation_valid "${1-}" || {
    echo "error: invalid task id \"${1-}\"" >&2
    exit 2
  }
}

# Prints "<policy> <yolo>" for a project, passing bin/fm-project-mode.sh's own
# stderr warnings and safe defaults through unchanged. The registry parser is
# invoked as this script's own sibling so a caller's FM_ROOT_OVERRIDE, which may
# name another installation's path, never decides which parser runs.
project_posture() {
  local project=$1 parsed
  parsed=$("$SCRIPT_DIR/fm-project-mode.sh" "$project") || {
    echo "error: project posture for \"$project\" could not be resolved" >&2
    return 1
  }
  printf '%s\n' "$parsed"
}

field_value() {  # <line> <key>
  local line=$1 key=$2
  case "$line" in
    "$key="*) printf '%s\n' "${line#"$key"=}" ;;
    *) return 1 ;;
  esac
}

# read_record <task-id>: 0 loaded, 1 absent, 2 refused (reason printed).
read_record() {
  local id=$1 path line n=0 expected
  local l1='' l2='' l3='' l4='' l5=''
  path="$STATE/$id.delivery"
  if [ -L "$path" ]; then
    echo "error: delivery resolution for task $id at $path is a symlink; refusing to read it as authority" >&2
    return 2
  fi
  [ -e "$path" ] || return 1
  if [ ! -f "$path" ]; then
    echo "error: delivery resolution for task $id at $path is not a regular file" >&2
    return 2
  fi
  if [ ! -r "$path" ]; then
    echo "error: delivery resolution for task $id at $path is not readable" >&2
    return 2
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    case "$n" in
      1) l1=$line ;;
      2) l2=$line ;;
      3) l3=$line ;;
      4) l4=$line ;;
      5) l5=$line ;;
    esac
  done < "$path"
  if [ "$n" -ne 5 ]; then
    echo "error: delivery resolution for task $id at $path has $n lines; expected exactly 5 (task, policy, surface, mode, yolo)" >&2
    return 2
  fi
  RECORD_TASK=$(field_value "$l1" task) || {
    echo "error: delivery resolution for task $id at $path does not start with task=" >&2
    return 2
  }
  RECORD_POLICY=$(field_value "$l2" policy) || {
    echo "error: delivery resolution for task $id at $path has no policy= on line 2" >&2
    return 2
  }
  RECORD_SURFACE=$(field_value "$l3" surface) || {
    echo "error: delivery resolution for task $id at $path has no surface= on line 3" >&2
    return 2
  }
  RECORD_MODE=$(field_value "$l4" mode) || {
    echo "error: delivery resolution for task $id at $path has no mode= on line 4" >&2
    return 2
  }
  RECORD_YOLO=$(field_value "$l5" yolo) || {
    echo "error: delivery resolution for task $id at $path has no yolo= on line 5" >&2
    return 2
  }
  if [ "$RECORD_TASK" != "$id" ]; then
    echo "error: delivery resolution at $path records task \"$RECORD_TASK\", not $id; refusing a mismatched identity" >&2
    return 2
  fi
  if ! policy_valid "$RECORD_POLICY"; then
    echo "error: delivery resolution for task $id records unknown project policy \"$RECORD_POLICY\"" >&2
    return 2
  fi
  if ! mode_valid "$RECORD_MODE"; then
    echo "error: delivery resolution for task $id records unknown mode \"$RECORD_MODE\"" >&2
    return 2
  fi
  if ! yolo_valid "$RECORD_YOLO"; then
    echo "error: delivery resolution for task $id records unknown yolo \"$RECORD_YOLO\"" >&2
    return 2
  fi
  if [ "$RECORD_SURFACE" != none ]; then
    if ! surface_valid "$RECORD_SURFACE"; then
      echo "error: delivery resolution for task $id records unknown surface \"$RECORD_SURFACE\"" >&2
      return 2
    fi
    if [ "$RECORD_POLICY" != no-mistakes-prod-only ]; then
      echo "error: delivery resolution for task $id records surface \"$RECORD_SURFACE\" under project policy \"$RECORD_POLICY\", which has no surface classification" >&2
      return 2
    fi
    expected=$(mode_for_surface "$RECORD_SURFACE")
    if [ "$RECORD_MODE" != "$expected" ]; then
      echo "error: delivery resolution for task $id records surface \"$RECORD_SURFACE\" with mode \"$RECORD_MODE\"; that surface resolves to $expected" >&2
      return 2
    fi
  fi
  return 0
}

# gate <task-id> <kind> [<project>]: the shared pre-mutation contract behind both
# `check` and `resolve`. On success, RECORD_* is loaded when a record exists and
# GATE_HAS_RECORD says whether it did.
GATE_HAS_RECORD=0
GATE_POLICY=
GATE_YOLO=
gate() {
  local id=$1 kind=$2 project=${3-} status posture
  GATE_HAS_RECORD=0
  GATE_POLICY=
  GATE_YOLO=
  set +e
  read_record "$id"
  status=$?
  set -e
  [ "$status" -ne 2 ] || return 1
  if [ -n "$project" ]; then
    posture=$(project_posture "$project") || return 1
    GATE_POLICY=${posture%% *}
    GATE_YOLO=${posture##* }
  fi
  if [ "$status" -eq 0 ]; then
    GATE_HAS_RECORD=1
    case "$kind" in
      scout)
        echo "error: task $id is a scout and its deliverable is a report, not a merge; remove $STATE/$id.delivery or dispatch it as a ship task" >&2
        return 1
        ;;
      secondmate)
        echo "error: task $id is a persistent secondmate, which ships no project change of its own; remove $STATE/$id.delivery before provisioning or launching its home" >&2
        return 1
        ;;
    esac
    if [ -n "$project" ] && [ "$GATE_POLICY" != "$RECORD_POLICY" ]; then
      echo "error: task $id was resolved under project policy \"$RECORD_POLICY\" but $project is now registered \"$GATE_POLICY\"; remove $STATE/$id.delivery and resolve the task again" >&2
      return 1
    fi
    return 0
  fi
  if [ "$kind" = ship ] && [ -n "$project" ] && [ "$GATE_POLICY" = no-mistakes-prod-only ]; then
    echo "error: project $project is registered no-mistakes-prod-only, so ship task $id needs an explicit surface classification before it is briefed or spawned: bin/fm-task-delivery.sh set $id $project --surface <internal-only|product-facing|mixed-or-uncertain>" >&2
    return 1
  fi
  return 0
}

cmd_set() {
  local id=${1-} project=${2-} arg policy posture record tmp
  local surface='' mode='' yolo='' want=''
  [ -n "$id" ] && [ -n "$project" ] || {
    echo "usage: fm-task-delivery.sh set <task-id> <project> --surface <surface> [--yolo <on|off>]" >&2
    echo "       fm-task-delivery.sh set <task-id> <project> --mode <mode> --yolo <on|off>" >&2
    exit 2
  }
  shift 2
  for arg in "$@"; do
    if [ -n "$want" ]; then
      case "$arg" in
        --*) echo "error: $want requires a value" >&2; exit 2 ;;
      esac
      case "$want" in
        --surface) surface=$arg ;;
        --mode) mode=$arg ;;
        --yolo) yolo=$arg ;;
      esac
      want=
      continue
    fi
    case "$arg" in
      --surface|--mode|--yolo) want=$arg ;;
      --surface=*) surface=${arg#--surface=} ;;
      --mode=*) mode=${arg#--mode=} ;;
      --yolo=*) yolo=${arg#--yolo=} ;;
      *) echo "error: unexpected argument \"$arg\"" >&2; exit 2 ;;
    esac
  done
  [ -z "$want" ] || { echo "error: $want requires a value" >&2; exit 2; }
  require_task_id "$id"

  if [ -n "$surface" ] && [ -n "$mode" ]; then
    echo "error: --surface resolves the project policy and --mode overrides it; pass exactly one" >&2
    exit 1
  fi
  if [ -z "$surface" ] && [ -z "$mode" ]; then
    echo "error: pass --surface to resolve a conditional project policy, or --mode with --yolo to record a task-specific captain instruction" >&2
    exit 1
  fi
  if [ -n "$yolo" ] && ! yolo_valid "$yolo"; then
    echo "error: --yolo must be on or off, not \"$yolo\"" >&2
    exit 1
  fi

  posture=$(project_posture "$project") || exit 1
  policy=${posture%% *}

  if [ -n "$surface" ]; then
    surface_valid "$surface" || {
      echo "error: --surface must be internal-only, product-facing, or mixed-or-uncertain, not \"$surface\"" >&2
      exit 1
    }
    if [ "$policy" != no-mistakes-prod-only ]; then
      echo "error: project $project is registered \"$policy\", which ships every change the same way; a surface classification applies only to no-mistakes-prod-only" >&2
      exit 1
    fi
    mode=$(mode_for_surface "$surface")
    [ -n "$yolo" ] || yolo=${posture##* }
  else
    mode_valid "$mode" || {
      echo "error: --mode must be no-mistakes, direct-PR, or local-only, not \"$mode\"" >&2
      exit 1
    }
    [ -n "$yolo" ] || {
      echo "error: --mode requires --yolo so the recorded resolution is never partial" >&2
      exit 1
    }
    surface=none
  fi

  record="$STATE/$id.delivery"
  if [ -e "$record" ] || [ -L "$record" ]; then
    echo "error: task $id already has a delivery resolution at $record; remove it before recording another" >&2
    exit 1
  fi
  if [ -e "$DATA/$id/brief.md" ]; then
    echo "error: task $id already has instructions at $DATA/$id/brief.md, which were shaped by its current resolution; record the resolution before the brief" >&2
    exit 1
  fi
  if [ -e "$STATE/$id.meta" ]; then
    echo "error: task $id is already dispatched and its recorded delivery state is authoritative; a later resolution would contradict it" >&2
    exit 1
  fi

  mkdir -p "$STATE" || {
    echo "error: could not create $STATE" >&2
    exit 1
  }
  tmp=$(mktemp "$STATE/.delivery.XXXXXXXX") || {
    echo "error: could not stage a delivery resolution for task $id" >&2
    exit 1
  }
  {
    printf 'task=%s\n' "$id"
    printf 'policy=%s\n' "$policy"
    printf 'surface=%s\n' "$surface"
    printf 'mode=%s\n' "$mode"
    printf 'yolo=%s\n' "$yolo"
  } > "$tmp"
  # An exclusive link publishes the complete record in one step: a concurrent
  # writer loses the link rather than merging with a half-written file.
  if ! ln "$tmp" "$record" 2>/dev/null; then
    rm -f "$tmp"
    echo "error: task $id already has a delivery resolution at $record; remove it before recording another" >&2
    exit 1
  fi
  rm -f "$tmp"
  echo "resolved $id: policy=$policy surface=$surface mode=$mode yolo=$yolo"
}

cmd_check() {
  local id=${1-} kind=${2-} project=${3-}
  [ -n "$id" ] && [ -n "$kind" ] || {
    echo "usage: fm-task-delivery.sh check <task-id> <ship|scout|secondmate> [<project>]" >&2
    exit 2
  }
  require_task_id "$id"
  case "$kind" in
    ship|scout|secondmate) ;;
    *) echo "error: kind must be ship, scout, or secondmate, not \"$kind\"" >&2; exit 2 ;;
  esac
  gate "$id" "$kind" "$project" || exit 1
}

cmd_resolve() {
  local id=${1-} kind=${2-} project=${3-}
  [ -n "$id" ] && [ -n "$kind" ] && [ -n "$project" ] || {
    echo "usage: fm-task-delivery.sh resolve <task-id> <ship|scout> <project>" >&2
    exit 2
  }
  require_task_id "$id"
  case "$kind" in
    ship|scout) ;;
    *) echo "error: resolve applies to ship and scout tasks, not \"$kind\"" >&2; exit 2 ;;
  esac
  gate "$id" "$kind" "$project" || exit 1
  if [ "$GATE_HAS_RECORD" -eq 1 ]; then
    printf '%s %s\n' "$RECORD_MODE" "$RECORD_YOLO"
    return 0
  fi
  if [ "$GATE_POLICY" = no-mistakes-prod-only ]; then
    # Reached only by a scout: a ship without a resolution is refused above.
    printf '%s %s\n' no-mistakes "$GATE_YOLO"
    return 0
  fi
  printf '%s %s\n' "$GATE_POLICY" "$GATE_YOLO"
}

VERB=${1-}
[ "$#" -eq 0 ] || shift
case "$VERB" in
  set) cmd_set "$@" ;;
  check) cmd_check "$@" ;;
  resolve) cmd_resolve "$@" ;;
  *) usage >&2; exit 2 ;;
esac
