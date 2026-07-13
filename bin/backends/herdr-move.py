#!/usr/bin/env python3
"""Typed raw AF_UNIX writer for herdr's native workspace.move request.

This is the WRITE-side sibling of herdr-eventwait.py, reusing the exact same
proven transport (one AF_UNIX connection, newline-delimited JSON) that the
read-only event subscriber already verified against real herdr 0.7.3
(protocol 16). It sends exactly ONE typed request and reads exactly ONE
response for it:

  request : {"id","method":"workspace.move",
             "params":{"workspace_id":W,"insert_index":N}}\n
  response: {"id",...,"result":{...}}\n           (success - generic ok)
        or  {"id",...,"error":{"code",...}}\n     (business-logic refusal)

workspace.move is herdr's first-class NON-DESTRUCTIVE reorder primitive
(data/herdr-workspace-reorder-audit-o5/report.md): it repositions an existing
live workspace container to insert_index in the flat ordered workspace list,
leaving the workspace's id, tabs, panes, agents, and focus untouched. It is
not reachable through the shipped herdr CLI (no `herdr workspace move` verb,
no raw-request escape hatch), which is the only reason this local writer
exists. The server may interleave broadcast event lines on the wire; anything
carrying an "event" key (or any id other than ours) is skipped while waiting
for our response.

This writer deliberately knows nothing about firstmate's contiguity policy:
ownership checks, target-order computation, and fail-closed abort decisions
all live in bin/backends/herdr.sh (fm_backend_herdr_contiguity_reconcile).
The bash side must only ever pass workspace ids it has already proven it
owns.

Usage: herdr-move.py <socket_path> <workspace_id> <insert_index> [<timeout_seconds>]

Output: nothing on success; the error response body (or a diagnostic) on
stderr for failures.

Exit status:
  0  the server acknowledged the move with a result (no error).
  2  bad arguments (empty workspace_id, non-integer/negative insert_index,
     non-positive timeout), could not connect, or could not send the request.
  3  the server answered our request id with an error, or with a body that
     carries neither result nor error (an unrecognized response shape).
  4  the server closed the stream or the response timed out - the move's
     outcome is UNKNOWN; the caller must treat this as failure and abort.
A non-zero exit means the caller must fail closed: abort the reconcile pass,
leave the current workspace order as-is, and never retry blindly.
"""
import json
import socket
import sys
import time

CONNECT_TIMEOUT = 5.0
DEFAULT_RESPONSE_TIMEOUT = 10.0
RECV_CHUNK = 65536
REQUEST_ID = "fm-workspace-move"


def _read_line(sock, buf, deadline):
    """Read one newline-terminated chunk from sock, honoring an absolute
    monotonic deadline. Returns (line_bytes_or_None, buf, outcome), where
    outcome is line, timeout, closed, or error."""
    while b"\n" not in buf:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return None, buf, "timeout"
        sock.settimeout(remaining)
        try:
            chunk = sock.recv(RECV_CHUNK)
        except socket.timeout:
            return None, buf, "timeout"
        except OSError:
            return None, buf, "error"
        if not chunk:
            return None, buf, "closed"
        buf += chunk
    line, buf = buf.split(b"\n", 1)
    return line, buf, "line"


def main(argv):
    if len(argv) < 4 or len(argv) > 5:
        print("usage: herdr-move.py <socket_path> <workspace_id> <insert_index> [<timeout_seconds>]", file=sys.stderr)
        return 2
    sock_path = argv[1]
    workspace_id = argv[2]
    if not workspace_id:
        print("herdr-move: refusing an empty workspace_id", file=sys.stderr)
        return 2
    # Typed validation mirroring WorkspaceMoveParams (insert_index: uint).
    try:
        insert_index = int(argv[3], 10)
    except ValueError:
        print("herdr-move: insert_index must be an integer, got %r" % argv[3], file=sys.stderr)
        return 2
    if insert_index < 0:
        print("herdr-move: insert_index must be >= 0, got %d" % insert_index, file=sys.stderr)
        return 2
    timeout = DEFAULT_RESPONSE_TIMEOUT
    if len(argv) == 5:
        try:
            timeout = float(argv[4])
        except ValueError:
            return 2
    if timeout <= 0:
        return 2

    try:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(CONNECT_TIMEOUT)
        sock.connect(sock_path)
    except OSError as exc:
        print("herdr-move: cannot connect to %s: %s" % (sock_path, exc), file=sys.stderr)
        return 2

    request = {
        "id": REQUEST_ID,
        "method": "workspace.move",
        "params": {"workspace_id": workspace_id, "insert_index": insert_index},
    }
    try:
        sock.sendall((json.dumps(request) + "\n").encode("utf-8"))
    except OSError as exc:
        print("herdr-move: send failed: %s" % exc, file=sys.stderr)
        return 2

    deadline = time.monotonic() + timeout
    buf = b""
    while True:
        line, buf, outcome = _read_line(sock, buf, deadline)
        if line is None:
            print("herdr-move: no response for %s (%s)" % (REQUEST_ID, outcome), file=sys.stderr)
            return 4
        try:
            message = json.loads(line.decode("utf-8", "replace"))
        except ValueError:
            continue
        if not isinstance(message, dict):
            continue
        # Broadcast events (including our own move's workspace_moved echo)
        # carry an "event" key; responses carry our request id.
        if message.get("event") is not None:
            continue
        if message.get("id") != REQUEST_ID:
            continue
        if message.get("error") is not None:
            print("herdr-move: server refused: %s" % json.dumps(message.get("error")), file=sys.stderr)
            return 3
        if message.get("result") is not None:
            return 0
        print("herdr-move: unrecognized response shape: %s" % line.decode("utf-8", "replace"), file=sys.stderr)
        return 3


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv))
    except KeyboardInterrupt:
        sys.exit(4)
