# @security-atlas/sdk

The security-atlas TypeScript SDK — the one the frontend / Node ecosystem
reaches for first.

## What ships today (slice 191)

The `OAuthClient` helper for OAuth 2.0 `client_credentials`. It acquires,
caches, and refreshes the bearer token your code presents to the platform's
canonical inbound wire surface (`Push` → `Receipt`, exposed over gRPC and
`POST /v1/evidence:push`). The SDK is the resource **client**: it obtains the
token; you attach it as `Authorization: Bearer <token>` on each `Push` call.

```ts
import { OAuthClient } from "@security-atlas/sdk";

const oc = new OAuthClient({
  clientId: "<CLIENT_ID>",
  clientSecret: "<CLIENT_SECRET>",
  issuerUrl: "https://atlas.example.com",
});

// Cached, thread-safe (single in-flight refresh), refreshes 60s before expiry.
const token = await oc.getToken();

// Use the token as the bearer on the canonical push surface:
await fetch("https://atlas.example.com/v1/evidence:push", {
  method: "POST",
  headers: {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    record: {
      idempotency_key: "example-key-2026-06-03T00:00:00Z",
      evidence_kind: "aws.s3.bucket_encryption_state.v1",
      schema_version: "1.0.0",
      control_id: "scf:CRY-04",
      scope: [
        { key: "cloud_account", values: ["aws:<ACCOUNT_ID>"] },
        { key: "environment", values: ["prod"] },
      ],
      observed_at: new Date().toISOString(),
      result: "pass", // one of: pass | fail | na | inconclusive
      payload: { bucket_name: "example-bucket", algorithm: "aws:kms" },
      source_attribution: {
        actor_type: "connector",
        actor_id: "connector:example:demo@dev",
      },
    },
  }),
});
// The platform returns a Receipt:
//   { record_id, hash, ingested_at, credential_id, deduplicated }.
```

`getToken()` returns the cached JWT until 60 seconds before expiry, then
refreshes synchronously; concurrent callers await a single in-flight refresh
Promise (no thundering herd to the issuer). No runtime dependencies — the SDK
uses the platform `fetch`.

## API surface

| Symbol                                      | Purpose                                                                                                 |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `new OAuthClient(opts: OAuthClientOptions)` | Construct the client. Throws `InvalidConfigError` on a missing required field.                          |
| `OAuthClient.getToken(): Promise<string>`   | Return a valid bearer token (cached, refresh-before-expiry).                                            |
| `OAuthClientOptions`                        | `{ clientId, clientSecret, issuerUrl, audience?, refreshLeewayMs?, httpTimeoutMs?, now?, fetchImpl? }`. |
| `OAuthError`, `InvalidConfigError`          | Error classes (`InvalidConfigError extends OAuthError`).                                                |
| `DEFAULT_REFRESH_LEEWAY_MS` (`60_000`)      | The before-expiry window inside which `getToken` refreshes proactively.                                 |
| `DEFAULT_HTTP_TIMEOUT_MS` (`30_000`)        | The per-request issuer-call timeout.                                                                    |

## Does NOT ship yet

- A typed high-level push method on this SDK. Slice 003's wire surface
  (`Push` → `Receipt`) is the canonical inbound API; the typed TypeScript
  client that wraps it graduates to this entry point in a follow-on slice.
  Until then, present the `getToken()` bearer to `POST /v1/evidence:push`
  directly, as shown above. (The Go SDK — `pkg/sdk-go` — already exposes a
  typed `Push`; TS catches up later.)
- Refresh-token grant, DPoP, mTLS (all v3 deferred per slice 191 P0-191-7).

## Security notes

- Use placeholder credentials and issuer URLs in any committed sample — never
  a real `clientSecret`, bearer token, platform endpoint, or AWS identifier.
- The `clientSecret` is presented only to the issuer's `/oauth/token`
  endpoint over TLS; it is never logged by the SDK.

## Build & test

```sh
cd sdk/typescript
npm run build        # tsc -p tsconfig.json
npm run typecheck    # tsc -p tsconfig.json --noEmit

# Run the unit tests (Node's built-in runner — no external deps):
node --test --experimental-strip-types tests/oauth.test.ts
```

The test suite (`tests/oauth.test.ts`) stands up an in-process
`node:http` issuer and injects a clock (`now`) to exercise caching,
refresh-before-expiry, single-flight refresh, and the error paths — no
external issuer required.
