# 414 — OSCAL bundle signing Phase 2: cosign-keyless + Fulcio + Rekor (368b)

**Cluster:** Oscal
**Estimate:** ~2d
**Type:** JUDGMENT
**Status:** `not-ready` (gated on an OIDC-identity-strategy decision — see Dependencies)
**Parent:** 368 (OSCAL export bundle signing ed25519 → cosign) · gated by [ADR-0010](../adr/0010-oscal-cosign-signing.md)

## Narrative

Phase 2 of the ADR-0010 ADOPT-DEFERRED plan. Adds the **`cosign-keyless`** mode — Fulcio-issued short-lived certs + Rekor transparency-log entries — the "Full Sigstore transparency-log" line in canvas §9.

**This is deliberately deferred and gated.** ADR-0010's load-bearing finding: slice 188's AS mints a per-deployment, non-public issuer that is **not in public Fulcio's federated trust root**, so atlas cannot mint a Fulcio cert without first resolving an identity-strategy question. Air-gap self-host can never use this mode (no Sigstore reachability), so `embedded-ed25519` remains its default regardless.

## What ships (when unblocked)

1. **`cosign-keyless` mode** — Fulcio cert issuance + Rekor upload during sign; Rekor inclusion-proof handling.
2. **Keyless verification dispatch** (identity + Rekor inclusion checks).
3. **Keyless integration tests** (against a test Sigstore stack or mocked Fulcio/Rekor).
4. **SaaS keyless-default flip** — revise the Helm default from `cosign-kms` (set by 413) to `cosign-keyless` once the identity strategy is live.

## Acceptance criteria

- [ ] **AC-1.** `cosign-keyless` mode signs (Fulcio cert) + logs to Rekor.
- [ ] **AC-2.** Keyless verification dispatch (identity + transparency-log inclusion).
- [ ] **AC-3.** Drift/round-trip integration tests for the keyless path.
- [ ] **AC-4.** SaaS (Helm) default flipped to `cosign-keyless` (revised AC-9).

## Dependencies

- **#368** (tracking parent) · **#413** (Phase 1 — must land first; establishes the mode framework + dispatch this extends).
- **ADR-0010** — `merged`.
- **OIDC-identity-strategy decision (BLOCKER).** A separate, short maintainer decision: how atlas establishes a Fulcio-trusted identity — (a) couple to a public IdP, (b) stand up a private Sigstore stack, or (c) onboard atlas's issuer to the Sigstore root. ADR-0010 "the single decision … secondary decision." Until this is decided, Phase 2 stays `not-ready`.

## Anti-criteria (P0)

- **P0-414-1.** Does NOT ship until the OIDC-identity-strategy decision lands (no half-built keyless path on `main`).
- **P0-414-2.** Does NOT change the air-gap `docker-compose` default (stays `embedded-ed25519` — keyless is unreachable there).
