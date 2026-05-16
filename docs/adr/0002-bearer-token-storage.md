# ADR 0002 — Bearer-token storage: HMAC-SHA256 keyed with server secret

**Status:** Accepted · Honored (verified 2026-05-15 by slice 071 audit — slice 034 shipped both schemes per spec: argon2id for `local_credentials.password_hash` + HMAC-SHA256-with-server-secret for `api_keys.token_hash`; `BEARER_HASH_KEY` env-var refuse-to-start guard present)

**Date:** 2026-05-12

**Implements through:** [`docs/issues/034-oidc-rp-local-users.md`](../issues/034-oidc-rp-local-users.md)

---

## Context

Slice 034 owns the `api_keys` table that backs every connector push (003 / 013 / 044 / 045 / 046 / 047 / 048 / 049) and every admin call against `/v1/admin/*`. The slice ships two distinct credential families:

1. **Local user passwords** — low-entropy human input, threat model is "attacker dumps the DB and brute-forces."
2. **API bearer tokens** — 160-bit random secrets issued by the server, threat model is "attacker steals the DB or steals one plaintext token."

The slice's anti-criterion (P0 — block merge) is:

> Does NOT store passwords without bcrypt/argon2

Read literally that prohibits unhashed-or-MD5-style password storage; it does not constrain the algorithm used for high-entropy machine tokens. But because both column types ("`password_hash`" on `local_credentials`, "`token_hash`" on `api_keys`) share a generic "credential" framing, a reviewer who lands cold on the diff will reasonably ask: "Why is one argon2id and the other something else?"

This ADR records the answer once, in advance, so the question doesn't become a security-review blocker on every future credential-touching slice.

## Decision

**Local user passwords:** argon2id (RFC 9106 — `m=64MB, t=1, p=4`).

**API bearer tokens:** HMAC-SHA256 keyed with a 32-byte server secret loaded from `BEARER_HASH_KEY` env var at process boot. Server **refuses to start** if the env var is missing or shorter than 32 bytes.

`api_keys.token_hash` is the raw 32-byte HMAC output, stored as `bytea`. The lookup path is constant-time by construction (`SELECT … WHERE token_hash = $1` after computing the HMAC in Go).

## Rationale

| Property                                       | Local password (argon2id)                                             | Bearer token (HMAC-SHA256+key)                                                                 |
| ---------------------------------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Input entropy                                  | Low (~30 bits, typical human password)                                | High (160 bits — `crypto/rand`-generated)                                                      |
| Threat being defended                          | Offline brute-force after DB dump                                     | DB dump containing hashes alone                                                                |
| Required defense                               | Slow KDF — make each guess expensive                                  | Pre-image resistance against a known-format input                                              |
| Hot-path latency                               | Once per login (rare — sessions cover the rest)                       | Once per connector push (`/v1/evidence:push`) — can run thousands/sec on a busy tenant         |
| Effect of using argon2id on bearer tokens      | n/a                                                                   | ~50ms per request added — would make `/v1/evidence:push` unusable for high-volume connectors   |
| Effect of using HMAC-SHA256 on local passwords | Catastrophic — a stolen DB lets an attacker brute-force every account | n/a                                                                                            |
| Why a keyed hash, not plain SHA-256?           | n/a                                                                   | Plain SHA-256 of a 160-bit token is fine on entropy grounds, but a keyed hash forces attackers |
|                                                |                                                                       | to also steal `BEARER_HASH_KEY` to validate stolen plaintexts — defense-in-depth against       |
|                                                |                                                                       | a DB-only leak.                                                                                |

**Why not bcrypt for either?** argon2id is the modern recommendation (RFC 9106, OWASP top choice for new systems). bcrypt is acceptable for passwords but not preferred for green-field code.

**Why not argon2id for bearer tokens, just with cheap parameters?** Two reasons:

1. The hot-path latency budget on `/v1/evidence:push` is the dominant constraint — even argon2id's minimum-cost parameters cost ~5ms per call, multiplied across every connector push, dwarfing the actual ingest work.
2. Argon2id's parameter choice is its own ongoing ops question. HMAC-SHA256 has no tuning surface: it works the same forever.

**Why store the HMAC output instead of the plaintext encrypted?** Encryption is reversible; if an attacker steals both the DB and the key, they recover every plaintext token. HMAC is one-way: even with the key, an attacker cannot recover the plaintext from the hash. They can only validate a guess — and 160-bit guesses are computationally infeasible.

## Operational requirement

`BEARER_HASH_KEY` is a **boot-blocking** server secret.

- **Generation:** `openssl rand -base64 32` (or equivalent; 32 bytes of `crypto/rand`).
- **Storage:** environment variable. For Kubernetes, mount from a Secret. For docker-compose, source from `deploy/docker/.env` (slice 037 will document this). For bare-metal self-host, set in the systemd unit.
- **Rotation:** changing `BEARER_HASH_KEY` invalidates every issued bearer token — all connectors must rotate. There is no graceful-rotation path in v1. Operators rotating the key must follow up by issuing fresh credentials and revoking the old ones via `/v1/admin/credentials/:id/rotate`.
- **Refuse-to-boot:** `cmd/atlas` validates the env var at startup. Empty or shorter than 32 bytes → fatal log + exit(1). The platform never silently downgrades to a plaintext or weak-key path.
- **Integration tests:** `internal/db/integration_test.go` sets `BEARER_HASH_KEY` to a fixed 32-byte test value in `TestMain` so every test runs against the same hashing keyspace.

## Consequences

- One additional bootstrap step for v1 self-host: generate and persist `BEARER_HASH_KEY`. Documented in slice 037's deploy bundle.
- Anti-criterion language in `docs/issues/034-oidc-rp-local-users.md` should be read as: human passwords use bcrypt or argon2id; high-entropy machine tokens use a constant-time, keyed pre-image-resistant function. Future credential slices should reference this ADR rather than relitigate.
- Schema-level enforcement: `api_keys.token_hash` is `bytea NOT NULL` of fixed length 32. `local_credentials.password_hash` is `text NOT NULL` of variable length (argon2id encoded form).
- Slices that introduce additional credential types (e.g., webhook signing secrets in v2) inherit the rule: high-entropy machine secret → keyed HMAC; low-entropy human input → argon2id.

## Alternatives considered

1. **Argon2id for both** — rejected on hot-path latency grounds. See rationale table.
2. **bcrypt for both** — rejected because bcrypt is not preferred for new systems and shares the same latency objection on bearer tokens.
3. **Plain SHA-256 (no key) for bearer tokens** — sufficient on entropy grounds, rejected because a keyed hash is strictly better against DB-only leak.
4. **Encrypt-at-rest with AES-GCM (reversible)** — rejected because reversibility is a misfeature: keys are write-once at issue, never retrieved.
5. **Store plaintext token alongside hash** — rejected on first principles (this is what we are explicitly NOT doing — token leaves the platform exactly once in the issue/rotate response and is never persisted in plaintext form).

---

[← ADR 0001 — FrameworkScope workflow](./0001-framework-scope-workflow.md)
