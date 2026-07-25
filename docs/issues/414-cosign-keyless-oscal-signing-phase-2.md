# 414 — OSCAL bundle signing Phase 2: cosign-keyless + Fulcio + Rekor (368b)

**Cluster:** Oscal
**Estimate:** ~2d
**Type:** JUDGMENT
**Status:** `ready` (OIDC-identity strategy decided — [ADR-0016](../adr/0016-oidc-identity-for-keyless-signing.md); scope tightened to opt-in private-Sigstore keyless, NOT a default flip)
**Parent:** 368 (OSCAL export bundle signing ed25519 → cosign) · gated by [ADR-0010](../adr/0010-oscal-cosign-signing.md) · OIDC-identity resolved by [ADR-0016](../adr/0016-oidc-identity-for-keyless-signing.md)

## Narrative

Phase 2 of the ADR-0010 ADOPT-DEFERRED plan. Adds the **`cosign-keyless`** mode — Fulcio-issued short-lived certs + Rekor transparency-log entries — the "Full Sigstore transparency-log" line in canvas §9.

**The gating OIDC-identity question is now resolved ([ADR-0016](../adr/0016-oidc-identity-for-keyless-signing.md)).** ADR-0010's load-bearing finding stands — slice 188's AS mints a per-deployment, non-public issuer that is **not in public Fulcio's federated trust root**, so atlas cannot mint a _public_-Fulcio cert. ADR-0016 resolves this: keyless ships as an **opt-in mode** using atlas's scoped `client:oscal-signer` identity (slice 188) federated into an **operator-run private Sigstore** (Fulcio + Rekor, operator-controlled trust root) — NOT public Fulcio (options a/d rejected). Air-gap self-host can never use this mode (no Sigstore reachability), so `embedded-ed25519` remains its default regardless, and the connected SaaS GA default stays `cosign-kms` (ADR-0010 default table preserved — keyless is opt-in, not a default flip).

## What ships (when unblocked)

1. **`cosign-keyless` mode (opt-in)** — Fulcio cert issuance + Rekor upload during sign against an **operator-run private Sigstore** (per ADR-0016), using atlas's scoped `client:oscal-signer` identity (slice 188); Rekor inclusion-proof handling.
2. **Keyless verification dispatch** (identity + Rekor inclusion checks against the operator's trust root).
3. **Keyless integration tests** (against a test Sigstore stack or mocked Fulcio/Rekor).
4. **Opt-in keyless availability** — make `cosign-keyless` selectable for the private-Sigstore deployment shape. **REVISED by ADR-0016:** this is NOT a SaaS default flip — the Helm GA default stays `cosign-kms` (ADR-0010 default table preserved); keyless is opt-in for operators who run their own Sigstore.

## Acceptance criteria

- [x] **AC-1.** `cosign-keyless` mode signs (Fulcio cert) + logs to Rekor. (`internal/oscal/cosign/signblob_keyless.go` `SignBlobKeyless`; `internal/oscal/sign_keyless.go` `KeylessSigner.SignBundle` records cert + Rekor log index in the manifest.)
- [x] **AC-2.** Keyless verification dispatch (identity + transparency-log inclusion). (`verifyCosignKeyless` + `VerifyBundleWithCosign` keyless case; `VerifyBlobKeyless` runs `cosign verify-blob --certificate-identity --certificate-oidc-issuer --rekor-url`.)
- [x] **AC-3.** Drift/round-trip integration tests for the keyless path. (`internal/oscal/sign_keyless_integration_test.go` — sign → WriteBundle → ReadBundle → verify + tamper-rejects, faking Fulcio/Rekor at the client boundary; unit round-trips in `sign_keyless_test.go`.)
- [x] **AC-4.** **REVISED by ADR-0016:** `cosign-keyless` is made selectable (opt-in) for the private-Sigstore deployment shape. (`ResolveSigningConfig` selects keyless only on explicit mode + private Fulcio/Rekor; never inferred — `TestResolveSigningConfig_KeylessIsNeverInferred`.) The SaaS (Helm) GA default is NOT flipped — it stays `cosign-kms` (ADR-0010 default table preserved). Keyless is opt-in, not a default.

**Delivered.** See `docs/audit-log/414-cosign-keyless-signing-decisions.md` for the JUDGMENT calls (library choice, OIDC plumbing, manifest shape, opt-in config).

## Dependencies

- **#368** (tracking parent) · **#413** (Phase 1 — must land first; establishes the mode framework + dispatch this extends).
- **ADR-0010** — `merged`.
- **OIDC-identity-strategy decision — RESOLVED.** [ADR-0016](../adr/0016-oidc-identity-for-keyless-signing.md) (slice 455) decided the strategy: keyless ships as an **opt-in mode** using atlas's scoped `client:oscal-signer` identity (slice 188) federated into an **operator-run private Sigstore** (options b-on-c), NOT public Fulcio (options a/d rejected) and NOT a default flip (option e — kms/embedded — stays the default for all primary shapes). This BLOCKER is cleared; status flipped `not-ready` → `ready`.

## Anti-criteria (P0)

- **P0-414-1.** Does NOT ship before slice 413 (Phase 1 mode framework) lands. The OIDC-identity-strategy BLOCKER is cleared by ADR-0016; the remaining gate is 413's dispatch foundation.
- **P0-414-2.** Does NOT change the air-gap `docker-compose` default (stays `embedded-ed25519` — keyless is unreachable there).
- **P0-414-3.** Does NOT flip the SaaS/Helm GA default to keyless (per ADR-0016 — keyless is opt-in; the GA default stays `cosign-kms`). Does NOT target public Fulcio for the runtime OSCAL surface (options a/d rejected — operator-run private Sigstore only).
