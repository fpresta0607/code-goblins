#!/usr/bin/env bash
# tests/fm-backend-herdr-move.test.sh - protocol and transport unit tests for
# the typed raw-socket workspace.move writer (bin/backends/herdr-move.py;
# docs/herdr-backend.md "Workspace contiguity"). Drives the real writer
# against a real AF_UNIX fake server (python3) so the exact request WIRE
# SHAPE (newline-delimited JSON, method, typed params) and every transport
# outcome (ok, interleaved events, server refusal, silence, early close,
# connect failure, argument validation) are asserted byte-for-byte, without
# any herdr binary.
set -u

# shellcheck source=tests/lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not found (required by the move writer)"; exit 0; }
command -v jq >/dev/null 2>&1 || { echo "skip: jq not found"; exit 0; }

TMP_ROOT=$(fm_test_tmproot fm-backend-herdr-move)
WRITER="$ROOT/bin/backends/herdr-move.py"

# fake-server.py <socket_path> <capture_file> <mode>: accepts ONE connection,
# captures the first request line verbatim, then behaves per <mode>:
#   ok            reply with the generic ok result for the writer's id
#   noise-then-ok reply with a broadcast event line, an unrelated-id response,
#                 and only then the ok - the writer must skip the noise
#   error         reply with an error response for the writer's id
#   silent        never reply (the writer must time out)
#   close         close the connection without replying
cat > "$TMP_ROOT/fake-server.py" <<'PY'
import socket
import sys

sock_path, capture, mode = sys.argv[1], sys.argv[2], sys.argv[3]
srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
srv.bind(sock_path)
srv.listen(1)
conn, _ = srv.accept()
buf = b""
while b"\n" not in buf:
    chunk = conn.recv(65536)
    if not chunk:
        break
    buf += chunk
line = buf.split(b"\n", 1)[0]
with open(capture, "wb") as fh:
    fh.write(line + b"\n")
if mode == "ok":
    conn.sendall(b'{"id":"fm-workspace-move","result":{"type":"ok"}}\n')
elif mode == "noise-then-ok":
    conn.sendall(b'{"event":"workspace_moved","data":{"workspace_id":"w42","insert_index":3,"workspaces":[]}}\n')
    conn.sendall(b'{"id":"someone-else","result":{"type":"ok"}}\n')
    conn.sendall(b'{"id":"fm-workspace-move","result":{"type":"ok"}}\n')
elif mode == "error":
    conn.sendall(b'{"id":"fm-workspace-move","error":{"code":"workspace_not_found","message":"no such workspace"}}\n')
elif mode == "silent":
    import time
    time.sleep(30)
elif mode == "close":
    pass
conn.close()
srv.close()
PY

start_server() {  # <name> <mode> -> sets SRV_SOCK, SRV_CAPTURE, SRV_PID
  local name=$1 mode=$2
  SRV_SOCK="$TMP_ROOT/$name.sock"
  SRV_CAPTURE="$TMP_ROOT/$name.capture"
  rm -f "$SRV_SOCK" "$SRV_CAPTURE"
  python3 "$TMP_ROOT/fake-server.py" "$SRV_SOCK" "$SRV_CAPTURE" "$mode" &
  SRV_PID=$!
  for _ in $(seq 1 50); do
    [ -S "$SRV_SOCK" ] && return 0
    sleep 0.1
  done
  fail "fake server did not bind $SRV_SOCK"
}

stop_server() {
  kill "$SRV_PID" 2>/dev/null
  wait "$SRV_PID" 2>/dev/null
  return 0
}

test_request_wire_shape_and_ok() {
  local out rc
  start_server shape ok
  out=$(python3 "$WRITER" "$SRV_SOCK" w42 3 5 2>"$TMP_ROOT/shape.err")
  rc=$?
  stop_server
  expect_code 0 "$rc" "the writer must exit 0 on an ok response (stderr: $(cat "$TMP_ROOT/shape.err"))"
  [ -z "$out" ] || fail "the writer must print nothing on success, got '$out'"
  [ -s "$SRV_CAPTURE" ] || fail "the server captured no request"
  jq -e '.method == "workspace.move"' "$SRV_CAPTURE" >/dev/null || fail "request method must be workspace.move: $(cat "$SRV_CAPTURE")"
  jq -e '.params.workspace_id == "w42"' "$SRV_CAPTURE" >/dev/null || fail "params.workspace_id must round-trip: $(cat "$SRV_CAPTURE")"
  jq -e '.params.insert_index == 3 and (.params.insert_index | type) == "number"' "$SRV_CAPTURE" >/dev/null \
    || fail "params.insert_index must be the TYPED integer 3 (WorkspaceMoveParams uint), not a string: $(cat "$SRV_CAPTURE")"
  jq -e '(.id | type) == "string" and (.id | length) > 0' "$SRV_CAPTURE" >/dev/null || fail "the request must carry a correlation id"
  jq -e '.params | keys == ["insert_index","workspace_id"]' "$SRV_CAPTURE" >/dev/null \
    || fail "params must carry exactly workspace_id and insert_index: $(cat "$SRV_CAPTURE")"
  pass "writer: sends exactly one typed workspace.move request ({workspace_id, insert_index: uint}) and exits 0 on the ok result"
}

test_skips_interleaved_events_and_foreign_responses() {
  local rc
  start_server noise noise-then-ok
  python3 "$WRITER" "$SRV_SOCK" w42 3 5 2>/dev/null
  rc=$?
  stop_server
  expect_code 0 "$rc" "the writer must skip broadcast events and foreign-id responses and still find its ok"
  pass "writer: skips interleaved broadcast events (including its own workspace_moved echo) and foreign-id responses"
}

test_error_response_exits_3() {
  local err rc
  start_server refuse error
  err=$(python3 "$WRITER" "$SRV_SOCK" w-gone 0 5 2>&1 >/dev/null)
  rc=$?
  stop_server
  expect_code 3 "$rc" "a server error response must exit 3"
  assert_contains "$err" "workspace_not_found" "the server's refusal must be surfaced on stderr"
  pass "writer: a server error response exits 3 and surfaces the refusal"
}

test_silent_server_times_out_exit_4() {
  local rc
  start_server quiet silent
  python3 "$WRITER" "$SRV_SOCK" w42 1 1 2>/dev/null
  rc=$?
  stop_server
  expect_code 4 "$rc" "a response timeout must exit 4 (outcome unknown - the caller fails closed)"
  pass "writer: a silent server times out with exit 4 (unknown outcome, caller must fail closed)"
}

test_early_close_exits_4() {
  local rc
  start_server drop close
  python3 "$WRITER" "$SRV_SOCK" w42 1 5 2>/dev/null
  rc=$?
  stop_server
  expect_code 4 "$rc" "a stream closed before the response must exit 4"
  pass "writer: a connection closed before the response exits 4"
}

test_connect_failure_exits_2() {
  local rc
  python3 "$WRITER" "$TMP_ROOT/no-such.sock" w42 1 2>/dev/null
  rc=$?
  expect_code 2 "$rc" "an unconnectable socket must exit 2"
  pass "writer: an unconnectable socket path exits 2"
}

test_typed_argument_validation_exits_2() {
  local rc
  python3 "$WRITER" "$TMP_ROOT/unused.sock" "" 1 2>/dev/null; rc=$?
  expect_code 2 "$rc" "an empty workspace_id must exit 2 before any connect"
  python3 "$WRITER" "$TMP_ROOT/unused.sock" w42 not-a-number 2>/dev/null; rc=$?
  expect_code 2 "$rc" "a non-integer insert_index must exit 2 before any connect"
  python3 "$WRITER" "$TMP_ROOT/unused.sock" w42 -1 2>/dev/null; rc=$?
  expect_code 2 "$rc" "a negative insert_index must exit 2 (WorkspaceMoveParams uint, minimum 0)"
  python3 "$WRITER" 2>/dev/null; rc=$?
  expect_code 2 "$rc" "missing arguments must exit 2"
  pass "writer: typed argument validation (empty id, non-integer or negative index, missing args) refuses before touching the socket"
}

test_request_wire_shape_and_ok
test_skips_interleaved_events_and_foreign_responses
test_error_response_exits_3
test_silent_server_times_out_exit_4
test_early_close_exits_4
test_connect_failure_exits_2
test_typed_argument_validation_exits_2

echo "# all herdr workspace.move writer protocol tests passed"
