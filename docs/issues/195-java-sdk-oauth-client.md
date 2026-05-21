# 195 — Java SDK OAuth client_credentials helper

**Cluster:** SDK
**Estimate:** 1-2d
**Type:** AFK (no judgment calls — translate slice 191's Go/Python/TS pattern to Java)
**Status:** `not-ready` (gate: slice 191 merged · `sdk/java/` package scaffold needs slice-001-style bootstrap)

## Provenance

Surfaced during slice 191 (D1 in `docs/audit-log/191-sdk-migration-decisions.md`). The slice 191 spec
permits Java SDK as spillover if `sdk/java/` doesn't exist; `ls sdk/java/` at the slice-191
worktree returned "No such file or directory", so the Java client is filed here.

## Narrative

Slice 191 shipped OAuth `client_credentials` helpers for Go (`pkg/sdk-go/oauth/`), Python
(`sdk/python/pyatlas/oauth.py`), and TypeScript (`sdk/typescript/src/oauth.ts`). The Java SDK
is the fourth language listed in the canvas tech-stack table (`Plans/canvas/09-tech-stack.md`)
but the `sdk/java/` directory has never existed.

This slice:

1. Scaffolds `sdk/java/` with Maven (`pom.xml`) — matches the canvas's "Java" entry
   without forcing a Gradle adoption decision until SDK consumers ask for it.
2. Ships the `OAuthClient` class with the same contract as the Go / Python / TS surfaces:
   `OAuthClient(clientId, clientSecret, issuerUrl)` + `getToken()` returning a cached JWT
   with 60-second refresh-before-expiry.
3. Uses `java.net.http.HttpClient` (JDK 11+) — no external HTTP library, matching the
   Python SDK's stdlib-only discipline.
4. Adds JUnit 5 unit tests covering caching, refresh-near-expiry, and concurrent access.

## Acceptance criteria

- **AC-1.** NEW `sdk/java/pom.xml` declares `groupId=com.security-atlas`, `artifactId=atlas-sdk`,
  Java 17 baseline (matches the wider monorepo's JDK floor — confirm in this slice).
- **AC-2.** NEW `sdk/java/src/main/java/com/security_atlas/sdk/OAuthClient.java` exposes:
  - Constructor: `OAuthClient(String clientId, String clientSecret, String issuerUrl)`
  - Builder option: `audience`, `refreshLeewayMs`, `httpTimeoutMs`, `clock` (for tests)
  - `String getToken() throws OAuthException` — synchronous; returns cached or freshly-acquired token
- **AC-3.** Thread-safe: concurrent `getToken()` callers serialize through a `ReentrantLock`.
- **AC-4.** Refresh-before-expiry at 60-second leeway.
- **AC-5.** Uses `java.net.http.HttpClient`; no Maven dependency beyond JUnit 5 test-scoped.
- **AC-6.** JUnit 5 unit tests cover: requires-config, cache-hit, refresh-near-expiry,
  concurrent-callers, issuer-error.
- **AC-7.** README at `sdk/java/README.md` mirrors `sdk/python/README.md`.

## Anti-criteria (P0)

- **P0-195-1.** Does NOT introduce a Gradle build (Maven only for v1).
- **P0-195-2.** Does NOT introduce an external HTTP library (e.g., OkHttp, Apache HttpClient).
- **P0-195-3.** Does NOT implement refresh-token grant, DPoP, or mTLS — same v3 deferrals as slice 191.
- **P0-195-4.** Does NOT modify any of the existing Go / Python / TS SDK behavior.

## Dependencies

- **#191** — slice 191 must merge first (delivers the `/oauth/token` `client_credentials` path
  the Java SDK calls).

## Skill mix (2-3)

- `tdd`
- `simplify`
- (optional) `ship-gate` — light surface; mostly a translation of existing code.

## Notes for the implementing agent

The Go / Python / TS source files are the authoritative specification. Read those three before
writing the Java surface so the contract stays identical across languages.

The canvas `Plans/canvas/09-tech-stack.md` lists Java as a v2 SDK; this slice graduates that
commitment into shipped code.

### Java baseline

If the wider monorepo has a documented Java baseline, match it. Otherwise, JDK 17 (LTS,
released Sep 2021, widely available) is the natural floor — it includes the `HttpClient`
fluent builder, sealed classes, and pattern-matching for switch which simplify the SDK.
Document the choice in this slice's decisions log (if any non-trivial calls surface).
