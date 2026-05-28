# 366 — JWT signing key rotation end-to-end

**Cluster:** Auth
**Estimate:** 3d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Slice 327's security audit (`docs/audits/327-security-audit-security-auditor-report.md` finding **M-1**, severity **Medium**) surfaced that `internal/auth/keystore/fsstore/fsstore.go:108-111` ships a `Rotate` stub returning `keystore.ErrRotateUnsupported`. The interface declares the method so callers can adopt the rotation API today; ADR-0003 §Key rotation strategy explicitly defers the end-to-end implementation.

NIST SP 800-57 Part 1 §5.3.6 recommends max cryptoperiod of 1-3 years for ES256 / ECDSA signing keys. A production deployment running >12 months operates with a single long-lived static key. The compromise blast radius is the entire signing-key lifetime; rotation cannot be triggered without operator file-system intervention (delete `.key` file, restart binary so `Open` regenerates a fresh keypair — which also invalidates every currently-issued JWT mid-flight, since no overlap window exists).

The filesystem store has the _shape_ for rotation already: `load()` reads every `.key` file in the directory, sorted by filename (KeyID format `YYYYMMDDTHHMMSSZ` is chronological-lexicographic). The alphabetically-last KeyID becomes the active signing key; the rest are retained as verification keys. What's missing is the orchestration: generate-new + retain-old-for-overlap-window + eventual prune.

### What ships

1. **`Rotate(ctx)` implementation in `fsstore.Store`.** Generate fresh ES256 keypair → write to `<dir>/<new-kid>.key` at mode 0600 (atomic temp+rename, same discipline as `generate()`) → call `s.load()` so the in-memory `signing` becomes the new key and `verify[]` carries both new + old.

2. **Configurable overlap window.** A `RotationOverlap time.Duration` field on the Store (or in ADR-config) with default 24h. KeyIDs older than `now() - access_token_lifetime - overlap` are pruned from disk by a separate `Prune(ctx)` method called from a scheduled job.

3. **CLI surface.**

   - `atlas keys list` — print every KeyID + age + role (signing | verifying | pending-prune).
   - `atlas keys rotate` — invoke `Rotate(ctx)` manually.
   - `atlas keys prune` — invoke `Prune(ctx)` manually with `--dry-run` default.

4. **Scheduled rotation job.** A `cmd/atlas` background task (already-present scheduler surface for slice 028 freeze-stamp, etc.) that calls Rotate annually by default (configurable via `ATLAS_KEY_ROTATION_INTERVAL`).

5. **Operational runbook.** `docs/runbooks/jwt-key-rotation.md` covering: routine annual rotation, emergency rotation (suspected compromise), key recovery, JWKS publication impact.

6. **JWKS endpoint update.** The OIDC discovery JWKS already publishes the full verification key set. Test that after rotation, the JWKS continues to advertise both keys for the overlap window.

7. **Integration tests.** Rotation under load (token signed with old key during overlap → still verifies); pruned key past overlap → JWT signed with it is rejected; rotation while a JWT is mid-validation → no race condition.

### Why this matters

A signing key is the platform's single point of trust for every JWT it issues. Long-lived static keys violate NIST guidance and are the kind of finding a third-party security review flags on first pass. Operators of self-hosted instances especially benefit — they cannot rely on a SaaS operator's KMS-backed rotation.

### Why now

ADR-0003 defers rotation explicitly. The slice-327 audit promotes M-1 from "documented deferral" to "tracked spillover" so the deferral has a slot number and lands in a planned batch rather than the indefinite future.

**Trigger:** filed 2026-05-28 from slice 327 audit.

## Threat model

STRIDE pass:

- **S (Spoofing):** A compromised long-lived signing key allows the attacker to mint arbitrary JWTs indefinitely. Rotation bounds the compromise window.
- **T (Tampering):** N/A.
- **R (Repudiation):** Each rotation event must be logged (audit log: who rotated, when, KeyID transition).
- **I (Information disclosure):** Private key material must never appear in logs (P0-187-6 invariant carries through; this slice does NOT change the logging discipline).
- **D (Denial of service):** Pruning a key still in use mid-validation could cause spurious 401s. The overlap window must exceed `AccessTokenLifetime` (currently 1h) by a comfortable margin.
- **E (Elevation of privilege):** A successful key-compromise attack is total platform compromise. Rotation bounds the window; it doesn't prevent the attack.

## Acceptance criteria

- [ ] **AC-1.** `fsstore.Store.Rotate(ctx)` generates a fresh ES256 keypair, persists it to `<dir>/<new-kid>.key` at mode 0600 atomically, and updates the in-memory `signing` + `verify[]` such that the new key is active and the old key remains in `verify[]`.
- [ ] **AC-2.** `fsstore.Store.Prune(ctx)` removes keypair files older than `(now - AccessTokenLifetime - RotationOverlap)`; the signing key is NEVER pruned.
- [ ] **AC-3.** Integration test (`fsstore_rotate_integration_test.go` with `//go:build integration`): a JWT signed with the pre-rotation key still verifies during the overlap window.
- [ ] **AC-4.** Integration test: a JWT signed with a key past the overlap window is rejected (`tokensign.Verify` returns "no verification key for kid").
- [ ] **AC-5.** `cmd/atlas-cli` exposes `atlas keys list` / `atlas keys rotate` / `atlas keys prune` subcommands.
- [ ] **AC-6.** The JWKS endpoint (`/.well-known/jwks.json` or wherever published) returns the full `verify[]` set, including the post-rotation pair.
- [ ] **AC-7.** Audit log row written on every rotation event (table TBD by engineer — likely `me_audit_log` or `unified_audit_log`).
- [ ] **AC-8.** `docs/runbooks/jwt-key-rotation.md` covers routine + emergency + recovery procedures.
- [ ] **AC-9.** ADR-0003 updated to mark "Key rotation strategy" as implemented (resolved status); add a follow-on revisit note for KMS-backed mode (deferred to v3).
- [ ] **AC-10.** `pre-commit run --all-files` passes; CI green.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** Closes M-1.
- **AI-assist boundary (CLAUDE.md).** Untouched; rotation is mechanical infra.
- **Audit log first-class.** Rotation events written to the audit log.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — OAuth AS + ES256 signing
- ADR-0003 §Key rotation strategy
- Audit report `docs/audits/327-security-audit-security-auditor-report.md` finding M-1

## Dependencies

- **#187** (keystore + ES256 signing) — `merged`.
- **#190** (JWT middleware) — `merged`. JWT validation surface unchanged.

## Anti-criteria (P0 — block merge)

- **P0-366-1.** Does NOT log private key material in any code path. The slice 187 P0-187-6 invariant carries through; this slice MUST honor it.
- **P0-366-2.** Does NOT prune the active signing key. The signing key is the alphabetically-last KeyID; prune skips it explicitly.
- **P0-366-3.** Does NOT shrink the overlap window below `AccessTokenLifetime`. The minimum is `AccessTokenLifetime + RotationOverlap` so no JWT currently in flight is invalidated mid-validation.
- **P0-366-4.** Does NOT change the on-disk file format (PKCS#8 PEM). Existing keypair files must continue to load.
- **P0-366-5.** Does NOT remove or change the manual-regenerate fallback (`Open` on empty dir creates a fresh keypair). Rotation is an additive capability, not a replacement.
- **P0-366-6.** Does NOT auto-merge.

## Skill mix

- `tdd` — RED-first integration tests
- `database-designer` if a rotation audit log table is added
- `simplify` — pre-PR quality pass

## Notes for the implementing agent

ADR-0003 §Key rotation strategy is the canonical spec — read it first. The fsstore is already half-built for rotation: `load()` does multi-key load sorted by KeyID; the existing `generate()` is most of the write path.

The KeyID format (`YYYYMMDDTHHMMSSZ`) is chronological-lexicographic; the "alphabetically-last is active" rule means a fresh `Rotate` call automatically becomes the new active signer just by writing a newer KeyID.

The slice-327 audit notes this as a foundational gap; the fix is largely orchestration on top of existing primitives. Don't over-engineer the rotation cadence — annual is the right default for v1; KMS-backed rotation is a separate v3 ADR.

JUDGMENT calls the implementing agent will make:

- Overlap window default (recommend 24h; could defensibly be 7d)
- Rotation cadence default (recommend annual; could be 6 months for high-security deployments)
- Whether to surface rotation as a tenant-level vs platform-level operation (recommend platform-level — JWKS is shared)

Document these in `docs/audit-log/366-...-decisions.md`.
