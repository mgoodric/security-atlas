"""OAuth 2.0 client_credentials helper for the security-atlas SDK.

Slice 191 ships this as the migration target for slice 003's
api-key-based authentication. SDK consumers move from constructing
a bearer-string directly to constructing an ``OAuthClient`` that
handles token acquisition, caching, and refresh.

Usage::

    from pyatlas import OAuthClient

    oc = OAuthClient(
        client_id="...",
        client_secret="...",
        issuer_url="https://atlas.example.com",
    )
    token = oc.token()
    # Use token as the bearer for any /v1/* call:
    #   Authorization: Bearer <token>

Thread safety
-------------

``OAuthClient.token()`` is safe for concurrent callers. The internal
cache + refresh state is guarded by a ``threading.Lock``. The first
caller in a refresh window blocks all subsequent callers until the
refresh completes — there is no thundering-herd to the issuer.

Refresh policy
--------------

``token()`` returns the cached JWT until 60 seconds before expiry,
then refreshes synchronously. Tokens have a 1-hour lifetime per
slice 188; the 60-second early refresh handles clock skew + slow
requests without ever returning an about-to-expire token.

Scope discipline
----------------

This module does NOT implement:

* Refresh-token grant (v3 deferred per slice 188).
* DPoP (v3 deferred per slice 191 P0-191-7).
* Token introspection — the SDK is the resource client, not the
  resource server.
"""

from __future__ import annotations

import json
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass, field

# DEFAULT_REFRESH_LEEWAY_SECONDS is the window before expiry inside
# which token() refreshes proactively. 60 seconds chosen to absorb
# clock skew + slow upstream calls.
DEFAULT_REFRESH_LEEWAY_SECONDS: float = 60.0

# DEFAULT_HTTP_TIMEOUT_SECONDS bounds the issuer request. Token
# acquisition is a single short round-trip; 30 seconds is generous.
DEFAULT_HTTP_TIMEOUT_SECONDS: float = 30.0


class OAuthError(Exception):
    """Base class for OAuthClient errors."""


class InvalidConfigError(OAuthError):
    """Raised by ``OAuthClient.__init__`` on missing / malformed config."""


@dataclass
class OAuthClient:
    """A thread-safe OAuth client_credentials bearer-token acquirer.

    All required parameters must be provided as keyword arguments;
    positional construction is discouraged because slice 191's
    contract is documented through the named fields.
    """

    client_id: str
    client_secret: str
    issuer_url: str

    # Optional: RFC 8693 audience form param.
    audience: str | None = None

    # Optional: window-before-expiry inside which token() refreshes.
    refresh_leeway_seconds: float = DEFAULT_REFRESH_LEEWAY_SECONDS

    # Optional: per-request issuer-call timeout (seconds).
    http_timeout_seconds: float = DEFAULT_HTTP_TIMEOUT_SECONDS

    # Optional: clock injection point for tests. Returns a Unix
    # timestamp (float seconds since epoch).
    now: Callable[[], float] = field(default=time.time)

    # Internal cache state — initialized in __post_init__.
    _cached_token: str | None = field(default=None, init=False, repr=False)
    _expires_at: float = field(default=0.0, init=False, repr=False)
    _lock: threading.Lock = field(default_factory=threading.Lock, init=False, repr=False)
    _token_url: str = field(default="", init=False, repr=False)

    def __post_init__(self) -> None:
        if not self.client_id:
            raise InvalidConfigError("client_id is required")
        if not self.client_secret:
            raise InvalidConfigError("client_secret is required")
        if not self.issuer_url:
            raise InvalidConfigError("issuer_url is required")
        if self.refresh_leeway_seconds <= 0:
            self.refresh_leeway_seconds = DEFAULT_REFRESH_LEEWAY_SECONDS
        if self.http_timeout_seconds <= 0:
            self.http_timeout_seconds = DEFAULT_HTTP_TIMEOUT_SECONDS
        self._token_url = self.issuer_url.rstrip("/") + "/oauth/token"

    def token(self) -> str:
        """Return a valid bearer token, refreshing if near expiry.

        Concurrent callers see a single synchronous refresh under
        the internal lock.
        """
        with self._lock:
            now = self.now()
            if self._cached_token and (now + self.refresh_leeway_seconds) < self._expires_at:
                return self._cached_token
            tok, expires_at = self._acquire(now)
            self._cached_token = tok
            self._expires_at = expires_at
            return tok

    def _acquire(self, now: float) -> tuple[str, float]:
        """POST grant_type=client_credentials to /oauth/token.

        Caller holds ``self._lock``. The HTTP call is synchronous;
        racing callers wait on the lock rather than firing
        concurrent requests at the issuer.
        """
        form = {
            "grant_type": "client_credentials",
            "client_id": self.client_id,
            "client_secret": self.client_secret,
        }
        if self.audience:
            form["audience"] = self.audience
        body = urllib.parse.urlencode(form).encode("utf-8")
        req = urllib.request.Request(
            self._token_url,
            data=body,
            method="POST",
            headers={
                "Content-Type": "application/x-www-form-urlencoded",
                "Accept": "application/json",
            },
        )
        try:
            with urllib.request.urlopen(req, timeout=self.http_timeout_seconds) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as exc:  # 4xx / 5xx from issuer
            detail = exc.read().decode("utf-8", errors="replace").strip()
            raise OAuthError(f"token endpoint returned {exc.code}: {detail}") from exc
        except urllib.error.URLError as exc:
            raise OAuthError(f"token request failed: {exc.reason}") from exc

        try:
            payload = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise OAuthError(f"parse token response: {exc}") from exc

        access_token = payload.get("access_token")
        if not access_token:
            raise OAuthError("token response missing access_token")
        expires_in = int(payload.get("expires_in") or 3600)
        if expires_in <= 0:
            expires_in = 3600
        return access_token, now + float(expires_in)
