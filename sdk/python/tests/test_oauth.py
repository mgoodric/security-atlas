"""Unit tests for pyatlas.OAuthClient.

The tests run a tiny in-process HTTP server to back the /oauth/token
endpoint — no external dependencies, just http.server.
"""

from __future__ import annotations

import json
import threading
import time
import unittest
import urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer

from pyatlas import InvalidConfigError, OAuthClient, OAuthError


class _IssuerHandler(BaseHTTPRequestHandler):
    """A test-only /oauth/token handler.

    The class attribute ``tokens`` is consumed left-to-right on
    each POST; ``expires_in`` is the lifetime returned. ``calls``
    counts successful issuances.
    """

    tokens: list[str] = []
    expires_in: int = 3600
    calls: int = 0
    status: int = 200

    def log_message(self, format, *args):  # silence test output
        return

    def do_POST(self):  # noqa: N802 — http.server naming
        if self.path != "/oauth/token":
            self.send_response(404)
            self.end_headers()
            return
        length = int(self.headers.get("Content-Length", "0") or "0")
        body = self.rfile.read(length).decode("utf-8")
        form = urllib.parse.parse_qs(body)
        if form.get("grant_type") != ["client_credentials"]:
            self.send_response(400)
            self.end_headers()
            return
        cls = type(self)
        if cls.status != 200:
            self.send_response(cls.status)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"invalid_client"}')
            return
        token = cls.tokens[cls.calls % len(cls.tokens)]
        cls.calls += 1
        payload = json.dumps(
            {
                "access_token": token,
                "token_type": "Bearer",
                "expires_in": cls.expires_in,
            }
        ).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


class _IssuerServer:
    """Run ``_IssuerHandler`` in a background thread for the test."""

    def __init__(self):
        # Fresh handler subclass per server so test cases don't share
        # ``tokens`` / ``calls`` state.
        self.handler = type(
            "_TestIssuerHandler",
            (_IssuerHandler,),
            {"tokens": [], "expires_in": 3600, "calls": 0, "status": 200},
        )
        self.server = HTTPServer(("127.0.0.1", 0), self.handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    @property
    def url(self) -> str:
        host, port = self.server.server_address
        return f"http://{host}:{port}"

    def close(self) -> None:
        self.server.shutdown()
        self.thread.join(timeout=2.0)
        self.server.server_close()


class OAuthClientTests(unittest.TestCase):
    def setUp(self) -> None:
        self.issuer = _IssuerServer()
        self.addCleanup(self.issuer.close)

    def test_requires_client_id(self) -> None:
        with self.assertRaises(InvalidConfigError):
            OAuthClient(client_id="", client_secret="s", issuer_url="https://x")

    def test_requires_client_secret(self) -> None:
        with self.assertRaises(InvalidConfigError):
            OAuthClient(client_id="i", client_secret="", issuer_url="https://x")

    def test_requires_issuer_url(self) -> None:
        with self.assertRaises(InvalidConfigError):
            OAuthClient(client_id="i", client_secret="s", issuer_url="")

    def test_token_caches_until_expiry(self) -> None:
        self.issuer.handler.tokens = ["tok-1"]
        oc = OAuthClient(
            client_id="i",
            client_secret="s",
            issuer_url=self.issuer.url,
        )
        tok1 = oc.token()
        tok2 = oc.token()
        self.assertEqual(tok1, "tok-1")
        self.assertEqual(tok1, tok2)
        self.assertEqual(self.issuer.handler.calls, 1)

    def test_token_refreshes_near_expiry(self) -> None:
        self.issuer.handler.tokens = ["tok-1", "tok-2"]
        self.issuer.handler.expires_in = 60
        base = time.time()
        clock = [base]

        def fake_now() -> float:
            return clock[0]

        oc = OAuthClient(
            client_id="i",
            client_secret="s",
            issuer_url=self.issuer.url,
            now=fake_now,
        )
        self.assertEqual(oc.token(), "tok-1")
        # Inside the 60s leeway: 60s lifetime, refresh threshold at +0s.
        # Advance to +30s, which is well inside the leeway window.
        clock[0] = base + 30
        self.assertEqual(oc.token(), "tok-2")
        self.assertEqual(self.issuer.handler.calls, 2)

    def test_token_serializes_concurrent_callers(self) -> None:
        self.issuer.handler.tokens = ["tok-1"]
        oc = OAuthClient(
            client_id="i",
            client_secret="s",
            issuer_url=self.issuer.url,
        )
        results: list[str] = []
        results_lock = threading.Lock()

        def worker() -> None:
            tok = oc.token()
            with results_lock:
                results.append(tok)

        threads = [threading.Thread(target=worker) for _ in range(10)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(len(results), 10)
        # All callers see the same token; only one issuer call.
        self.assertEqual(set(results), {"tok-1"})
        self.assertEqual(self.issuer.handler.calls, 1)

    def test_token_surfaces_issuer_error(self) -> None:
        self.issuer.handler.tokens = ["unused"]
        self.issuer.handler.status = 401
        oc = OAuthClient(
            client_id="i",
            client_secret="s",
            issuer_url=self.issuer.url,
        )
        with self.assertRaises(OAuthError):
            oc.token()


if __name__ == "__main__":
    unittest.main()
