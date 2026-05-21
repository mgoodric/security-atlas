# pyatlas

The security-atlas Python SDK.

## What ships in slice 191

The `pyatlas.OAuthClient` helper for OAuth 2.0 `client_credentials`:

```python
from pyatlas import OAuthClient

oc = OAuthClient(
    client_id="...",
    client_secret="...",
    issuer_url="https://atlas.example.com",
)

token = oc.token()  # cached, refreshes 60s before expiry
```

`OAuthClient.token()` is thread-safe (guarded by `threading.Lock`),
caches the access token until 60 seconds before expiry, and
refreshes synchronously. No external dependencies — only the
Python standard library.

## What does NOT ship in slice 191

- The high-level evidence push surface (slice 003's eventual SDK
  graduates to this OAuth flow in a follow-on slice).
- Refresh-token grant, DPoP, mTLS (all v3 deferred).
- An async (`asyncio`) variant — synchronous is sufficient for
  the CLI / scripting use case slice 191 targets.

## Testing

```
cd sdk/python
python -m unittest discover -s tests
```
