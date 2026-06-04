# 455 — OIDC-identity-strategy decision spike (unblocks cosign keyless / slice 414)

**Cluster:** Security
**Estimate:** S (0.5d)
**Type:** JUDGMENT (decision-only)
**Status:** `ready`
**Parent / unblocks:** 414 (cosign-keyless OSCAL signing Phase 2 / 368b) · gated by [ADR-0010](../adr/0010-oscal-cosign-signing.md)

## Narrative

Slice 414 (cosign-keyless OSCAL-bundle signing, Phase 2 of the ADR-0010
ADOPT-DEFERRED plan) is `not-ready`, and it is gated on a single missing
decision. ADR-0010's load-bearing finding: slice 188's Authorization Server
mints a **per-deployment, non-public issuer** (`aud = <atlas-instance-issuer>`)
that is **not in public Fulcio's federated trust root**. atlas therefore cannot
mint a Fulcio cert for keyless signing without first resolving _how_ it
establishes a Fulcio-trusted identity. No decision spike exists for this yet —
unlike slice 400, which unblocked the Phase-1 work (slice 413) by settling the
mode framework. This slice is that missing spike for the keyless leg.

This is **decision-only: it ships NO production code.** It mirrors slice 400's
shape — a confidence-rated ADR that lays out the options, the cost ledger, and a
recommendation, then flips slice 414's status accordingly. The maintainer signs
off on the recommendation (it does not auto-merge).

### What ships

1. **A decision ADR** (`docs/adr/NNNN-oidc-identity-for-keyless-signing.md`,
   next free ADR slot) that lays out, in maintainer-readable form, the keyless
   OIDC-identity options for OSCAL-bundle signing:

   - **(a) GitHub-Actions-OIDC for release artifacts only.** Use the
     already-Fulcio-trusted `token.actions.githubusercontent.com` identity (the
     same one slice 084 / release.yml already use for the checksums signature).
     This works for CI-time _release artifacts_ but does **not** solve runtime,
     in-deployment OSCAL-bundle signing — name that boundary explicitly
     (cross-ref ADR-0010 "Scope precision": release-artifact row vs
     OSCAL-bundle row).
   - **(b) Per-tenant / per-deployment federated issuer.** atlas's own AS
     (slice 188) federates its issuer into a trust root a Fulcio instance
     accepts. Cost: who runs that Fulcio? Public Fulcio will not federate an
     arbitrary self-hosted issuer.
   - **(c) Private Sigstore stack.** Operator-run Fulcio + Rekor. Full keyless
     semantics, no public-infra dependency, but a heavy operational ask for a
     self-host solo persona.
   - **(d) Sigstore-root onboarding.** Onboard atlas's issuer to the public
     Sigstore root-trust process. Slow, governance-heavy, uncertain.
   - **(e) Defer keyless to air-gap-only / don't-adopt-keyless.** Keep
     `cosign-kms` (slice 413) as the connected-deployment default and
     `embedded-ed25519` for air-gap; document keyless as a v3+ item. (This is
     the path of least resistance and may be the right call.)

   For each: what it requires at runtime, its failure modes, and its fit for the
   self-host-solo vs SaaS deployment shapes.

2. **A recommendation with a confidence rating** — ADOPT one option,
   ADOPT-DEFERRED (commit direction, build later), or DON'T-ADOPT-KEYLESS (keep
   kms + embedded, record why canvas §9's "Full Sigstore transparency-log in v3"
   stays v3).

3. **Flip slice 414's status** based on the recommendation: `not-ready` →
   `ready` (if a buildable option is chosen) OR `not-ready` → `wontfix` (if
   DON'T-ADOPT-KEYLESS). Update 414's "Dependencies / BLOCKER" line + the 368
   parent doc to reference this ADR.

## Threat model

STRIDE pass — this is a _decision_ artifact (ships no code), but the decision is
about a **signing-identity trust root**, so the threat model reasons about the
options' security properties, not about an implementation surface.

**S — Spoofing (the central axis)**

- _Threat:_ The whole point of keyless is binding a signature to a verifiable
  identity. Option (b)/(c)/(d) each define _which_ identity Fulcio will vouch
  for — get the trust-root scoping wrong and a signature could be mintable by an
  identity that isn't atlas, defeating the "who signed this export?" assurance.
- _Mitigation:_ The ADR must, for each option, name the exact identity Fulcio
  certifies and who can obtain a token for it. The recommendation must prefer an
  option whose identity-issuance is auditable and not broadly mintable.
- _Anti-criterion:_ P0-455-3.

**R — Repudiation**

- _Threat:_ Keyless's value is the Rekor transparency-log record. An option that
  yields no public/queryable log (e.g. a private Rekor only the operator sees)
  weakens the third-party-verifiability that motivates the feature.
- _Mitigation:_ The ADR weighs each option's transparency-log reachability for
  an _external auditor_ (the v1 binary criterion).

**I — Information disclosure**

- _Threat:_ A federated-issuer or private-Sigstore option could expose
  deployment metadata (issuer URLs, tenant identifiers) in Fulcio certs / Rekor
  entries that are public by design.
- _Mitigation:_ The ADR notes, per option, what becomes publicly visible in the
  cert/log and whether that leaks tenant-identifying data.

**E — Elevation of privilege**

- _Threat:_ A misconfigured federation (option b) could let one tenant's
  identity sign as another, or let any holder of an atlas machine token sign
  audit-binding bundles.
- _Mitigation:_ The recommendation must keep signing-identity issuance scoped
  and not broadly delegable; it cross-references slice 188's machine-token model.

**T / D**

- Not applicable to a decision-only artifact (no input surface, no availability
  surface). Noted for completeness per the mandatory STRIDE pass.

## Acceptance criteria

- [ ] **AC-1.** ADR authored at `docs/adr/NNNN-oidc-identity-for-keyless-signing.md`
      (next free ADR slot — note ADR-0003 double-occupancy when computing) laying
      out options (a)-(e), each with runtime requirements, failure modes, and
      deployment-shape fit.
- [ ] **AC-2.** Each option's signing-identity trust properties are stated:
      which identity Fulcio certifies, who can obtain a token, what becomes
      publicly visible in cert/log.
- [ ] **AC-3.** A confidence-rated recommendation (ADOPT / ADOPT-DEFERRED /
      DON'T-ADOPT-KEYLESS) with a per-deployment-shape default.
- [ ] **AC-4.** Slice 414's status is flipped accordingly (`ready` or
      `wontfix`), and its BLOCKER line + the 368 parent doc updated to cite this
      ADR. (Status-table registration of the flip is the orchestrator's job —
      this slice updates the slice DOCS only; it does NOT edit `_STATUS.md`.)
- [ ] **AC-5.** NO production code changed (decision-only) — docs/ADR only.
- [ ] **AC-6.** Cross-references ADR-0010 (the "single decision … secondary
      decision" this spike resolves) and the release-artifact-vs-OSCAL-bundle
      scope distinction so the reader doesn't conflate the two signing surfaces.
- [ ] **AC-7.** `pre-commit run --all-files` passes.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6, v1 binary criterion).** The
  decision is in service of stock-tooling-verifiable exports.
- **OSCAL is the wire format (invariant #8).** Untouched — the decision concerns
  signing identity, not the export format.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — "Evidence integrity — … Full Sigstore
  transparency-log in v3".
- [ADR-0010](../adr/0010-oscal-cosign-signing.md) — defines the deferred keyless
  leg + the OIDC-identity question this spike answers.
- Slice 188 (OAuth AS machine identity / per-deployment non-public issuer).

## Dependencies

- **#414** (cosign-keyless Phase 2) — this spike's output unblocks or closes it.
- **#400 / ADR-0010** (parent decision spike) — `merged`; this spike resolves
  the secondary OIDC-identity decision ADR-0010 explicitly deferred.
- **#188** (AS machine identity) — `merged`; its non-public issuer is the root
  cause of the gating question.
- **#413** (cosign-kms Phase 1) — the fallback this spike weighs keyless against.

## Anti-criteria (P0 — block merge)

- **P0-455-1.** Ships NO production code — if the spike tempts a "quick
  prototype", that belongs in slice 414's implementation, not here.
- **P0-455-2.** Does NOT auto-merge — the recommendation is a maintainer
  sign-off decision (the human-in-the-loop gate for slice 414).
- **P0-455-3.** Does NOT recommend an option whose signing-identity issuance is
  broadly mintable / unauditable — the trust-root scoping is the whole point.
- **P0-455-4.** Does NOT edit `_STATUS.md` (orchestrator batch-registers); this
  slice updates slice DOCS (414 + 368) only.
- **P0-455-5.** Does NOT conflate the CI-time release-artifact signing surface
  (slice 451) with the runtime OSCAL-bundle signing surface (414) — keep the
  ADR-0010 scope distinction explicit.

## Skill mix (3-5)

- `security-review` — the trust-root + identity-issuance reasoning.
- `dependency-auditor` (light) — Fulcio federation / Sigstore-root onboarding
  facts.
- `runbook-generator` — the ADR is a maintainer-readable decision doc.

## Notes for the implementing agent

- This mirrors slice 400 exactly: decision-only ADR + flip the gated slice's
  status. Read slice 400 + ADR-0010 first — ADR-0010 already names this as "the
  single decision … secondary decision" it deferred, so much of the framing is
  pre-written there. Your job is to develop the options into a confidence-rated
  recommendation and flip 414.
- Option (e) — DON'T-ADOPT-KEYLESS, keep kms + embedded — is a legitimate and
  possibly correct outcome for a self-host-solo persona. cosign-kms (slice 413)
  already closes the bulk of the named auditor friction with stock
  `cosign verify-blob` and no Fulcio/Rekor dependency. Do not treat "ship
  keyless" as the foregone conclusion; the spike exists precisely to make that
  an explicit, recorded call.
- Cross-ref slice 451 (SLSA/SBOM for release artifacts): that slice's
  GitHub-Actions-OIDC keyless flow is option (a) here for the _release-artifact_
  surface — but it does NOT solve runtime OSCAL-bundle signing (different
  identity). Keep the two surfaces distinct (ADR-0010 "Scope precision").
