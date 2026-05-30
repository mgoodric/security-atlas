# Slice 366 — JWT signing key rotation end-to-end · Decisions log

**Type:** JUDGMENT. The subjective build-time calls below were made by the implementing agent using best-reasoned, pattern-matched judgment, recorded here, and shipped. No human sign-off gate. The maintainer iterates from the "Revisit once in use" list post-deployment.

**Closes:** slice-327 security audit finding M-1.

---

## Decisions made

### D1 — Overlap window default = 24h

- **Options:** 24h (ADR-0003 draft) · 7d (slice notes "could defensibly be 7d").
- **Chosen:** 24h, exposed as `fsstore.DefaultRotationOverlap` and overridable via `atlas-cli keys prune --overlap`.
- **Rationale:** matches ADR-0003 § Key rotation strategy exactly (24× the 1h access-token TTL). With a 1h JWKS cache, every verifier re-fetches ~24× during the window, so it always sees both keys before the old one sunsets. The prune cutoff is `now − AccessTokenLifetime − overlap`, which structurally guarantees no in-flight token is invalidated mid-validation (P0-366-3). 7d would be needlessly conservative for forward-security rotation and lengthens the window an attacker could exploit a stolen-but-rotated-out key — though that window is bounded by token expiry anyway. 24h is the documented default; operators lengthen it freely.
- **Confidence:** high.

### D2 — Rotation cadence default = annual (NOT the 90d in ADR-0003's draft)

- **Options:** 90 days (ADR-0003 draft, citing AWS KMS 365d / GCP KMS 30/90/365d) · annual (slice-366 spec "what ships" #4 + "Notes for the implementing agent": _"annual is the right default for v1"_) · 6 months (slice JUDGMENT note "could be 6 months for high-security deployments").
- **Chosen:** annual (`defaultKeyRotationInterval = 365 * 24h`), overridable via `ATLAS_KEY_ROTATION_INTERVAL`.
- **Rationale:** the slice spec is the more recent authoritative document and explicitly directs annual for v1, twice. NIST SP 800-57 Part 1 §5.3.6 recommends a 1-3 year cryptoperiod for ES256 signing keys, so annual is comfortably inside guidance. The ADR's 90d was a draft "designed-shape" figure from slice 187, written before the slice spec existed. I resolved the discrepancy in favor of the slice spec and **updated ADR-0003 to record the supersession** (so the two documents no longer contradict). High-security deployments shorten via the env var (e.g. `2160h` = 90d), so the conservative operator is one config line away.
- **Confidence:** high (the direction is explicit in the spec; the only judgment was reconciling the doc conflict, which I did by updating the older doc rather than papering over it).

### D3 — Rotation audit trail = structured platform-level slog line, NOT a per-tenant audit-log table

- **Options:** (a) write to one of the nine per-tenant audit-log tables (`me_audit_log` / `unified_audit_log` aggregator) · (b) create a new `key_rotation_audit_log` table · (c) emit a structured `slog` audit line.
- **Chosen:** (c) — a structured INFO log line `audit_event=key_rotation actor=scheduler previous_signing_kid=... new_signing_kid=... occurred_at=...`.
- **Rationale:** JWT key rotation is a **platform-level** event — the keystore and JWKS are shared across all tenants (this slice's own JUDGMENT note recommends platform-level, not tenant-level). The nine per-domain audit-log tables are all **tenant-scoped with RLS** and the `unifiedlog` aggregator explicitly refuses a tenant-less write (slice-124 P0-A5). Forcing a tenant-less crypto event into a tenant-scoped table would be a category error — there is no honest `tenant_id` to assign. Option (b) (a new table) is over-engineering for v1: a single structured log line is forensically sufficient (it carries the KeyID transition, actor, and timestamp), composes with the existing OTEL/Loki log pipeline (CLAUDE.md observability stack), and matches the existing precedent in `cmd/atlas/main.go` where the active `signing_kid` is already surfaced via `slog` at startup (and key bytes never are — P0-187-6). If a future audit requires the rotation history in the queryable unified log, a dedicated platform-audit surface can be added then (revisit item below).
- **Confidence:** medium — the call is sound for v1, but a security auditor diligencing the _product_ might prefer a queryable rotation history over grepping logs. Flagged for revisit.

### D4 — Same-second rotation collision handling

- **Decision:** `Rotate` bumps the new KeyID to the next whole second if a rotation lands in the same wall-clock second as the current signer (the KeyID format has 1-second granularity, and "alphabetically-last is active" requires the new KeyID to sort strictly after the old).
- **Rationale:** without this, two rotations within one second would produce identical KeyIDs (file overwrite + ambiguous active key). The bump is deterministic and preserves the chronological-lexicographic invariant. In production the annual cadence makes this near-impossible, but the manual CLI + the test suite can trigger it; defending against it is cheap and correct.
- **Confidence:** high.

### D5 — CLI `keys prune` is dry-run by default; `--confirm` required to delete

- **Decision:** `atlas-cli keys prune` prints what _would_ be removed and removes nothing unless `--confirm` is passed.
- **Rationale:** deleting signing-key material is irreversible and security-sensitive. A dry-run default is the standard safety posture for destructive ops (matches the slice spec's "`atlas keys prune` … with `--dry-run` default"). The active signer is additionally protected at the `Store.Prune` layer (P0-366-2), so even `--confirm` cannot delete it.
- **Confidence:** high.

### D6 — KeyInfo surface carries only KeyID + age + role (no key material)

- **Decision:** the operator-facing `fsstore.KeyInfo` struct exposes `KeyID string`, `Age time.Duration`, `Signing bool` — and nothing else.
- **Rationale:** P0-366-1 forbids logging private key material anywhere. Making the operator-facing struct _structurally incapable_ of carrying key bytes is defense-in-depth: a future `keys list --json` cannot accidentally serialize a private key because the type has no field for one. A unit test asserts no PEM markers leak through the rendered surface.
- **Confidence:** high.

---

## Revisit once in use

1. **D3 — queryable rotation history.** If a real third-party auditor wants the rotation event log in the queryable unified audit surface (rather than the structured log pipeline), add a platform-scoped `key_rotation_audit_log` table + wire it into the `unifiedlog` aggregator as a tenant-NULL branch. Re-evaluate the first time an auditor asks "show me your key rotation history" and grepping Loki is judged insufficient.
2. **CLI-vs-running-server keystore staleness.** The CLI `keys rotate`/`prune` mutate the on-disk directory, but a _running_ server caches the keystore in memory and only reloads on its own rotate/prune calls. The runbook documents this and recommends restart-after-CLI-rotate for an immediate cutover. Revisit by adding a hot-reload signal (SIGHUP handler or an admin endpoint) so a CLI rotation propagates into a running process without a restart. Re-evaluate once an operator hits the staleness gap in practice.
3. **D2 — cadence default.** Re-confirm annual is right once real operators run the platform >12 months. If security-conscious customers consistently shorten it, consider lowering the default (or surfacing a config prompt at install).
4. **KMS/HSM backend (v3).** The `keystore.KeyStore` interface supports a KMS-backed implementation without caller changes. Build it when a customer needs the private key to never leave a security module (FIPS/FedRAMP path). Tracked as a separate v3 ADR.
5. **Multi-key prune ordering under heavy rotation.** `Prune` iterates the verification set and removes everything past the cutoff except the active signer. If a deployment rotates very frequently (sub-hour) there could be many overlapping keys; the current logic is O(n) per prune and correct, but the JWKS payload grows with key count. Re-evaluate a max-retained-keys cap if any deployment rotates aggressively.

---

## Confidence summary

| Decision                           | Confidence |
| ---------------------------------- | ---------- |
| D1 overlap window 24h              | high       |
| D2 cadence annual                  | high       |
| D3 audit via slog (platform-level) | medium     |
| D4 same-second collision bump      | high       |
| D5 prune dry-run default           | high       |
| D6 KeyInfo minimal surface         | high       |

The single `medium` (D3) is the top of the revisit list.
