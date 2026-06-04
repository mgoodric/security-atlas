# 413 — OSCAL bundle signing Phase 1: cosign-kms + retained embedded-ed25519 (368a)

**Cluster:** Oscal
**Estimate:** ~3d
**Type:** JUDGMENT
**Status:** `in-progress` (batch 186, 2026-06-03 — maintainer approved ADR-0010 ADOPT-DEFERRED)
**Parent:** 368 (OSCAL export bundle signing ed25519 → cosign) · gated by [ADR-0010](../adr/0010-oscal-cosign-signing.md)

## Narrative

Phase 1 of the ADR-0010 ADOPT-DEFERRED plan (slice 400 decision spike, maintainer-approved 2026-06-03). Implements the **reachable** cosign value with **no Fulcio/Rekor/OIDC dependency**, and keeps the in-process ed25519 path as the explicit air-gap fallback. The fancy `cosign-keyless` mode is deferred to slice 414 (368b), gated on a separate OIDC-identity decision.

This closes the bulk of the slice-327 M-3 / canvas-§9 auditor friction (ad-hoc embedded public key + bespoke verifier) by making exports verifiable with **stock `cosign verify-blob`** in the KMS mode, while never regressing air-gap self-host.

See [ADR-0010](../adr/0010-oscal-cosign-signing.md) for the full value/cost/decision analysis and `docs/issues/368-cosign-signing-migration.md` "Re-scoped by ADR-0010 → 368a" for the day-by-day mapping.

## What ships (Phase 1 only — no Fulcio/Rekor/OIDC)

1. **New package `internal/oscal/cosign`** — conservative wrapper around `cosign sign-blob` / `cosign verify-blob` (timeouts, curated env allowlist, error mapping; no caller-env inheritance).
2. **`cosign-kms` mode** — sign via a KMS-held key reference (operator-controlled identity; no Sigstore public infra).
3. **Retained `embedded-ed25519` mode** — the current `internal/oscal/sign.go` path, kept and made an explicit selectable mode (the air-gap default).
4. **`Mode` discriminator** recorded in the export manifest; **verification dispatch** picks the verifier by manifest mode; **backward-compat** for already-signed (ed25519) bundles.
5. **CLI `sign | verify | config-check`** surfaces.
6. **Operator runbook** + **integration tests for Modes A (kms) & B (embedded)**.

## Default modes (per ADR-0010 table — Phase 1 reachable subset)

- `docker-compose.yml` self-host → **`embedded-ed25519`** (unchanged; air-gap-safe).
- Connected self-host with a KMS → **`cosign-kms`** (opt-in).
- SaaS (Helm) at GA → **`cosign-kms`** (ADR-0010 revised AC-9; keyless only after 414).

## Acceptance criteria (368 AC subset covered by Phase 1)

- [x] **AC-1.** `internal/oscal/cosign` wrapper lands (timeouts, env allowlist, error mapping).
- [x] **AC-2.** `cosign-kms` + `embedded-ed25519` modes both selectable + functional.
- [x] **AC-3.** Export manifest records the signing `Mode`; verification dispatches on it.
- [x] **AC-4.** Backward-compat: existing ed25519-signed bundles still verify.
- [x] **AC-5.** CLI `sign | verify | config-check`.
- [x] **AC-6 (Modes A & B).** Integration tests for kms + embedded sign→verify round-trips.
- [x] **AC-7.** Operator runbook (mode selection, KMS setup, air-gap guidance).
- [x] **AC-10.** cosign binary dependency handled per ADR-0010 (Apache-2.0 bundle-clean; pin version; provenance in asset-inventory §2.3).
- [x] **AC-8 / AC-11.** `pre-commit run --all-files` + CI green; no air-gap regression.

## Dependencies

- **#368** (tracking parent).
- **ADR-0010** — `merged` (maintainer-approved 2026-06-03). The design authority.
- **#188** (AS client_credentials machine identity) — `merged`; relevant context only (NOT used by Phase 1 — keyless is 414).

## Anti-criteria (P0)

- **P0-413-1.** NO Fulcio / Rekor / OIDC / keyless code — that is slice 414. Phase 1 is kms + embedded only.
- **P0-413-2.** Does NOT change the air-gap `docker-compose` default away from `embedded-ed25519`.
- **P0-413-3.** Does NOT widen the OSCAL export public API beyond what the mode discriminator + dispatch require.
- **P0-413-4.** Backward-compat: an existing ed25519-signed bundle MUST still verify post-change (no silent break of prior exports).
