# atlas-sdk (Java)

The security-atlas Java SDK.

## What ships in slice 195

The `OAuthClient` helper for OAuth 2.0 `client_credentials`:

```java
import com.security_atlas.sdk.OAuthClient;

OAuthClient oc = OAuthClient.builder()
    .clientId("...")
    .clientSecret("...")
    .issuerUrl("https://atlas.example.com")
    .build();

String token = oc.getToken();  // cached, refreshes 60s before expiry
```

`OAuthClient.getToken()` is thread-safe (guarded by `ReentrantLock`),
caches the access token until 60 seconds before expiry, and
refreshes synchronously. **No runtime dependencies** — only the
JDK's `java.net.http.HttpClient` plus a tiny hand-rolled JSON
reader scoped to the three fields the SDK consumes.

## Engineer decisions

- **D1 — Maven, not Gradle.** Maven is the more conventional choice for
  public SDK distribution; matches the slice 195 spec (`P0-195-1`
  forbids a Gradle build).
- **D2 — `java.net.http.HttpClient` (JDK 11+).** Zero external HTTP
  dependency. Matches the Python SDK's stdlib-only discipline.
- **D3 — Hand-rolled JSON reader, not Jackson.** The OAuth token
  response is a flat object with three known fields
  (`access_token`, `token_type`, `expires_in`). A 100-line reader
  is the right trade-off vs adding a 1-MB+ runtime dependency.
  Reconsider if the SDK grows to call richer endpoints
  (slice 196+).
- **D4 — Java 17 LTS baseline.** Matches the slice spec AC-1 and
  the wider monorepo's JDK floor.

## What does NOT ship in slice 195

- The high-level evidence push surface (slice 003's eventual SDK
  graduates to this OAuth flow in a follow-on slice).
- Refresh-token grant, DPoP, mTLS (all v3 deferred per slice 191
  P0-191-7).
- An async / reactive variant — synchronous matches the Go SDK's
  blocking `Token(ctx)` and Python's blocking `token()`.

## Testing

```
cd sdk/java
mvn test
```

The test suite uses an in-process `com.sun.net.httpserver.HttpServer`
as a fake atlas issuer; no external services required.
