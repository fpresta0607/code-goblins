"""stand_in_model.py - a stand-in for a model, for pi on a runner with no account.

It answers OpenAI's chat completions API on 127.0.0.1 with a short reply,
streamed slowly enough that a goblin's terminal shows pi at work on its
brief, so a spawn on a clean runner starts a goblin as it does on a person's
machine. It reaches nothing else and keeps nothing.

    python stand_in_model.py <port>
"""

import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REPLY = ["I am ", "a stand-in ", "model, ", "so I ", "change ", "nothing."]


class StandIn(BaseHTTPRequestHandler):
    def log_message(self, *args: object) -> None:
        pass

    def do_GET(self) -> None:
        body = json.dumps({"object": "list", "data": [{"id": "stand-in", "object": "model"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        self.rfile.read(length)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for index, text in enumerate(REPLY):
            delta: dict[str, str] = {"content": text}
            if index == 0:
                delta["role"] = "assistant"
            self.event({"choices": [{"index": 0, "delta": delta, "finish_reason": None}]})
            time.sleep(2)
        self.event({"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 1, "completion_tokens": len(REPLY), "total_tokens": 1 + len(REPLY)}})
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def event(self, chunk: dict[str, object]) -> None:
        chunk.update({"id": "stand-in", "object": "chat.completion.chunk", "created": int(time.time()), "model": "stand-in"})
        self.wfile.write(b"data: " + json.dumps(chunk).encode() + b"\n\n")
        self.wfile.flush()


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), StandIn).serve_forever()
