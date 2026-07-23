#!/usr/bin/env python3
"""Tiny OpenAI-compatible mock upstream for local Conductor dev testing.

Not part of the product; used only to exercise the gateway pipeline without
real provider credentials. Serves /v1/models and /v1/chat/completions.
"""
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = 9099


class Handler(BaseHTTPRequestHandler):
    def _send(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.rstrip("/").endswith("/models"):
            self._send(200, {
                "object": "list",
                "data": [
                    {"id": "gpt-4o", "object": "model", "owned_by": "mock"},
                    {"id": "gpt-4o-mini", "object": "model", "owned_by": "mock"},
                ],
            })
        else:
            self._send(404, {"error": "not found"})

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length) if length else b"{}"
        try:
            req = json.loads(raw)
        except Exception:
            req = {}
        if self.path.rstrip("/").endswith("/chat/completions"):
            model = req.get("model", "gpt-4o")
            user_msg = ""
            for m in req.get("messages", []):
                if m.get("role") == "user":
                    user_msg = m.get("content", "")
            self._send(200, {
                "id": "chatcmpl-mock-1",
                "object": "chat.completion",
                "created": int(time.time()),
                "model": model,
                "choices": [{
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": f"Hello from the mock provider! You said: {user_msg}",
                    },
                    "finish_reason": "stop",
                }],
                "usage": {"prompt_tokens": 12, "completion_tokens": 10, "total_tokens": 22},
            })
        else:
            self._send(404, {"error": "not found"})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    print(f"mock provider listening on :{PORT}")
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
