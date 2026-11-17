"""hidden_server.py - runs a Python server with what it prints in files.

Start-HiddenServer in hidden_server.ps1 starts a job's servers through this, in
a hidden window that shows nothing they print. Start-Process could send their
output to files itself, but then it hands a server the handles of the step
that started it, the step's output among them, and the runner ends a step's
processes once its shell has exited and its output is still open, so a server
a later step needs would be gone.

    python hidden_server.py <log> <seconds> -m <module> [arguments...]
    python hidden_server.py <log> <seconds> <script> [arguments...]

It writes <log>.out and <log>.err, a line at a time. A server stuck before it
listens prints nothing, so until it listens it also writes where Python is,
every <seconds>, to <log>.stack.
"""

import faulthandler
import runpy
import socketserver
import sys

log, seconds, *command = sys.argv[1:]
sys.stdout = open(f"{log}.out", "w", buffering=1, encoding="utf-8")
sys.stderr = open(f"{log}.err", "w", buffering=1, encoding="utf-8")
stacks = open(f"{log}.stack", "w", encoding="utf-8")
faulthandler.dump_traceback_later(float(seconds), repeat=True, file=stacks)

# Each server the jobs start is a socketserver.TCPServer, which listens here.
listen = socketserver.TCPServer.server_activate


def listen_and_stop_stacks(server: socketserver.TCPServer) -> None:
    listen(server)
    faulthandler.cancel_dump_traceback_later()


socketserver.TCPServer.server_activate = listen_and_stop_stacks
if command[0] == "-m":
    sys.argv = command[1:]
    runpy.run_module(command[1], run_name="__main__", alter_sys=True)
else:
    sys.argv = command
    runpy.run_path(command[0], run_name="__main__")
