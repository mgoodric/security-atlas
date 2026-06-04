# 368 — OSCAL export bundle signing: ed25519 → cosign

**Cluster:** Oscal
**Estimate:** 5d (re-scoped into 368a ~3d + 368b ~2d — see "Re-scoped by ADR-0010")
**Type:** JUDGMENT
**Status:** `in-progress` (tracking parent — ADR-0010 approved; split into 413 Phase 1 in-progress + 414 Phase 2 gated)
**Gated by:** [ADR-0010](../adr/0010-oscal-cosign-signing.md) (slice 400 decision spike)

## Re-scoped by ADR-0010 (slice 400 decision spike)

[ADR-0010](../adr/0010-oscal-cosign-signing.md) is the maintainer's go/no-go
gate for this slice. Its recommendation — **ADOPT-DEFERRED, confidence HIGH** —
re-scopes the single 5d "build all three modes, keyless-default-for-SaaS" plan
below into **two phased sub-slices**, because:

1. **The `cosign-keyless` OIDC-identity premise in this doc does not hold as
   written.** Slice 188's AS mints a per-deployment, non-public issuer
   (`aud = <atlas-instance-issuer>`), which is **not** in public Fulcio's
   federated trust root. atlas's own machine identity therefore cannot mint a
   Fulcio cert without either a public-IdP coupling, a private Sigstore stack,
   or a Sigstore-root onboarding process. Keyless is gated on resolving that.
2. **`cosign-kms` is reachable now** with no Fulcio/Rekor/OIDC dependency and
   closes the bulk of the named auditor friction (ad-hoc key + bespoke
   verifier) with stock `cosign verify-blob`.
3. **Air-gap self-host can only use `embedded-ed25519`** — retained, default
   there, non-negotiable.

**Status: this slice is `blocked` pending maintainer approval of ADR-0010.** On
approval, file the two sub-slices below; this slice becomes the tracking parent.

### 368a — Phase 1 (cosign-kms + retained embedded), ~3d — `ready` on ADR approval

`internal/oscal/cosign` wrapper + `cosign-kms` mode + retained
`embedded-ed25519` + `Mode` discriminator + manifest mode + verification
dispatch + backward-compat + CLI `sign|verify|config-check` + runbook +
integration tests for Modes A & B. Maps to the day-by-day plan below:
Day 1 (wrapper + kms) · Day 3 (dispatch + manifest + backward-compat) ·
Day 4 (CLI + runbook) · most of Day 5 (Mode-A/B integration tests + decisions
log). **No Fulcio/Rekor/OIDC.** Covers AC-1, AC-2 (kms + embedded values),
AC-3, AC-4, AC-5, AC-7, AC-10, and AC-6 for Modes A & B.

### 368b — Phase 2 (cosign-keyless + Fulcio + Rekor), ~2d — `not-ready` (gated)

`cosign-keyless` mode plus Fulcio cert issuance plus Rekor transparency-log
handling, keyless verification dispatch, keyless integration tests, and the SaaS
keyless-default flip. Maps to Day 2 (keyless + Fulcio) plus the keyless slice of
Day 5. **Blocked-by:** a separate, short OIDC-identity-strategy decision
(public IdP vs private Sigstore vs Sigstore-root onboarding) — see ADR-0010
"the single decision … secondary decision." This is canvas §9's "Full Sigstore
transparency-log in v3" line. Covers the `cosign-keyless` portion of AC-2, AC-3,
AC-6, and the revised AC-9 SaaS default.

### AC-9 revision (per ADR-0010 default-mode table)

The original AC-9 below set the Helm/SaaS default to `cosign-keyless`. **ADR-0010
revises this:** at GA the SaaS (Helm) default is **`cosign-kms`** (reachable,
operator-controlled identity, no Sigstore public-infra dependency);
`cosign-keyless` becomes the SaaS default only after 368b + the OIDC-identity
decision land. The `docker-compose.yml` self-host default stays
`embedded-ed25519` (unchanged). See ADR-0010 "Recommended default mode per
deployment shape."

---

## Narrative

Slice 327's security audit (`docs/audits/327-security-audit-security-auditor-report.md` finding **M-3**, severity **Medium**) surfaced that `internal/oscal/sign.go` signs OSCAL export bundles with **in-process ed25519 detached signatures** rather than the canvas-mandated **cosign** flow.

Canvas §9 commits to "cosign signing of audit-export bundles." The slice-030 decisions log §D1 documents the in-process choice as a deliberate trade-off (avoiding a fragile external-binary dependency) and flags "swap for cosign keyless + Fulcio transparency log" as a v3 revisit item.

The cryptographic shape is equivalent — ed25519 detached over content digest is what `cosign sign-blob` produces under the hood. What's _missing_ is the **Sigstore ecosystem integration**: transparency log entries, Fulcio-issued OIDC identities, ecosystem tooling compatibility. An auditor verifying an atlas export cannot use stock `cosign verify-blob` without bespoke handling of the embedded ad-hoc public key.

For a GRC product whose central claim is _auditor-friendly export_, shipping with a non-standard signature ecosystem is a notable friction point in the v1 binary success criterion.

### What ships

Multi-mode signer with cosign as the default for connected deployments and the in-process ed25519 path retained as an explicit air-gap fallback.

1. **New package `internal/oscal/cosign`.** Thin wrapper around `cosign sign-blob` (and `cosign verify-blob`) shelling out to the cosign binary. The wrapper is conservative: timeouts, error mapping, no inheritance of caller env beyond a curated allowlist.

2. **Signing-mode discriminator.** Existing `oscal.Signature` struct gains a `Mode` field with three values:

   - `"cosign-keyless"` — Sigstore Fulcio + transparency log. Default when atlas has an OIDC identity (the platform's own machine identity).
   - `"cosign-kms"` — cosign with a KMS-backed signer (AWS KMS / GCP KMS / Azure Key Vault). Selected when `ATLAS_COSIGN_KMS_REF` is set.
   - `"embedded-ed25519"` — the existing in-process path. Selected when `ATLAS_OSCAL_SIGNING_MODE=embedded-ed25519` is explicitly set OR when cosign is unavailable AND `ATLAS_OSCAL_ALLOW_EMBEDDED=true` is set.

3. **Manifest carries mode.** The bundle manifest's signature block records which mode produced it so verifiers know which validation path to run.

4. **Verification dispatch.** `oscal.VerifyBundle` dispatches on `Mode`: keyless mode calls `cosign verify-blob` (which checks the Rekor transparency log entry too); KMS calls cosign with the KMS ref; embedded-ed25519 retains the existing in-process verify.

5. **CLI surface.**

   - `atlas oscal sign <bundle>` — uses the configured signing mode.
   - `atlas oscal verify <bundle>` — auto-detects mode from manifest.
   - `atlas oscal config-check` — reports which signing mode the current config produces and whether prerequisites are met (cosign installed, OIDC reachable, KMS accessible).

6. **Operational runbook.** `docs/runbooks/oscal-signing.md` covering the three modes, when to choose each, prerequisites, and migration from existing ed25519-signed bundles (verification continues to work; new bundles use the configured default mode).

### JUDGMENT calls

The engineer makes the following design calls and records them in `docs/audit-log/368-...-decisions.md`:

- **Default mode for self-hosted deployments.** Recommend `embedded-ed25519` until the operator opts into cosign (self-host is air-gap-friendly).
- **Default mode for SaaS deployments.** Recommend `cosign-keyless` (Sigstore is the obvious default for a connected platform).
- **Cosign binary dependency strategy.** Bundle in container OR require operator install? Recommend bundle (consistent across deployments; one Dockerfile line).
- **OIDC identity for keyless.** Which OIDC issuer should mint the Fulcio cert for atlas's signing operations? Recommend the platform's own AS issuing a dedicated service identity (`oauth_client:oscal-signer`) — slice 188's machine-token infrastructure already supports this.
- **Existing-bundle compatibility.** Verify the migration path: old ed25519 bundles must continue to verify under the new dispatch; manifest schema additions are backward-compatible.

### Why this matters

Canvas §9 commits to cosign. The v1 binary success criterion ("survive third-party security review") rewards spec-aligned tool choices. Auditors and downstream verifiers using stock Sigstore tooling can verify atlas exports without bespoke handling.

### Why now

M-3 from the slice-327 audit. Multi-week effort; appropriate for a v2 milestone batch rather than a single quarterly hardening sprint.

**Trigger:** filed 2026-05-28 from slice 327 audit.

## Threat model

STRIDE pass:

- **S (Spoofing):** Keyless mode with Fulcio binds signatures to a verifiable OIDC identity, closing the "who signed?" gap that ad-hoc ed25519 leaves open.
- **T (Tampering):** Transparency log entries (Rekor) make tampering with the signing record itself detectable.
- **R (Repudiation):** Improved — Sigstore's transparency log is exactly the repudiation defense for signing operations.
- **I (Information disclosure):** Bundle content is unaffected; signature metadata is what changes.
- **D (Denial of service):** Cosign sign-blob has Fulcio + Rekor as live dependencies; an outage degrades to embedded-ed25519 fallback if configured. Embedded-only deployments are unaffected.
- **E (Elevation of privilege):** Compromised signing identity remains a total-platform risk; this slice does NOT reduce that surface — it makes detection / attribution easier.

## Acceptance criteria

- [ ] **AC-1.** `internal/oscal/cosign` package wraps `cosign sign-blob` + `cosign verify-blob` with timeouts, error mapping, and conservative env handling.
- [ ] **AC-2.** `oscal.Signature` gains a `Mode` field with three valid values: `cosign-keyless`, `cosign-kms`, `embedded-ed25519`.
- [ ] **AC-3.** `oscal.SignBundle` dispatches on configured mode; `oscal.VerifyBundle` dispatches on the bundle's manifest mode.
- [ ] **AC-4.** `cmd/atlas-cli` exposes `atlas oscal sign|verify|config-check` subcommands.
- [ ] **AC-5.** Existing ed25519-signed bundles continue to verify under the new dispatch (backward compatibility).
- [ ] **AC-6.** Integration test (`oscal_cosign_integration_test.go` with `//go:build integration`): sign + verify round-trip in each mode succeeds; a tampered bundle fails verification in each mode.
- [ ] **AC-7.** `docs/runbooks/oscal-signing.md` covers the three modes, when to use each, prerequisites, and migration.
- [ ] **AC-8.** ~~ADR-0003 (or a new ADR) records the multi-mode decision + the mode-selection logic.~~ **DONE — recorded in [ADR-0010](../adr/0010-oscal-cosign-signing.md)** (slice 400). The mode-selection logic + per-deployment defaults live there.
- [ ] **AC-9.** **REVISED by ADR-0010.** Default mode for `docker-compose.yml` self-host deployment is `embedded-ed25519` (no cosign prerequisite) — unchanged. Default for `deploy/helm` SaaS deployment is **`cosign-kms` at GA** (was `cosign-keyless`); `cosign-keyless` becomes the SaaS default only after 368b + the OIDC-identity decision. See ADR-0010 "Recommended default mode per deployment shape."
- [ ] **AC-10.** `pre-commit run --all-files` passes; CI green.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** Closes M-3 by aligning with canvas §9.
- **OSCAL is the wire format (invariant #8).** Untouched; this slice changes signing of the export, not the export format itself.
- **Evidence integrity (invariant #2).** Strengthens chain-of-trust around export bundles.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — "cosign signing of audit-export bundles"
- ADR-0003 (audit-export hash + signing)
- Slice 030 decisions log §D1 (revisit list — cosign migration)
- Audit report `docs/audits/327-security-audit-security-auditor-report.md` finding M-3

## Dependencies

- **#030** (OSCAL SSP/POAM export) — `merged`. Existing signing surface.
- **#188** (OAuth AS client_credentials) — `merged`. Provides the machine identity for cosign-keyless mode.

## Anti-criteria (P0 — block merge)

- **P0-368-1.** Does NOT remove the embedded-ed25519 path. Air-gapped deployments depend on it.
- **P0-368-2.** Does NOT break verification of existing ed25519-signed bundles. The `Mode` discriminator is additive; absent-field defaults to `embedded-ed25519`.
- **P0-368-3.** Does NOT shell out to cosign with attacker-controlled args. The wrapper's input is the bundle digest (hex string) and the signing-mode config — both server-controlled.
- **P0-368-4.** Does NOT bundle the cosign binary into the release artifact without an explicit license review (cosign is Apache 2.0; verify before bundling).
- **P0-368-5.** Does NOT auto-merge.

## Skill mix

- `tdd` — RED-first integration tests across modes
- `database-designer` (light) — if signing-event audit log is added
- `simplify` — pre-PR quality pass

## Notes for the implementing agent

This is a 5d slice — substantial scope. Suggested phased approach within the slice:

1. **Day 1:** `internal/oscal/cosign` package + cosign-kms mode (simplest of the three; no Fulcio integration).
2. **Day 2:** cosign-keyless mode + Fulcio integration + transparency log handling.
3. **Day 3:** dispatch logic + manifest schema additions + backward-compat tests.
4. **Day 4:** CLI surface + runbook.
5. **Day 5:** integration tests across modes + decisions log + PR.

The slice-030 decisions log already documented the cosign migration as a v3 revisit; this slice promotes it to actually-tracked work. ADR-0003 may need an update or a fresh ADR depending on the scope of the signing-mode decision.

The cosign binary version pinning is its own concern: pin a known-good version (currently 2.x stable) in the bundling step; update via a separate slice when cosign releases a new major.

For Fulcio identity: atlas's own AS already issues machine tokens via slice 188. The cosign-keyless path needs an OIDC identity acceptable to Fulcio's federation set — the simplest is having atlas's AS pre-register with the Sigstore root chain, OR using a public OIDC IdP (Google / GitHub) for the signing identity. The decisions log should document the choice.
