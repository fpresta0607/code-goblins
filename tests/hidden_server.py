"""hidden_server.py - runs a Python server with what it prints in two files.

Start-HiddenServer in hidden_server.ps1 starts a job's servers through this, in
a hidden window that shows nothing they print. Start-Process could send their
output to files itself, but then it hands a server the handles of the step
that started it, the step's output among them, and the runner ends a step's
processes once its shell has exited and its output is still open, so a server
a later step needs would be gone.

    python hidden_server.py <log> -m <module> [arguments...]
    python hidden_server.py <log> <script> [arguments...]

It writes <log>.out and <log>.err, a line at a time.
"""

import runpy
import sys

log, *command = sys.argv[1:]
sys.stdout = open(f"{log}.out", "w", buffering=1, encoding="utf-8")
sys.stderr = open(f"{log}.err", "w", buffering=1, encoding="utf-8")
if command[0] == "-m":
    sys.argv = command[1:]
    runpy.run_module(command[1], run_name="__main__", alter_sys=True)
else:
    sys.argv = command
    runpy.run_path(command[0], run_name="__main__")
