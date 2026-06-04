# 400 — OSCAL signing: cosign/Sigstore decision spike + ADR (no code)

**Cluster:** Oscal
**Estimate:** 0.5-1d
**Type:** JUDGMENT (decision-only)
**Status:** `merged` (`ae14ea4d`, #956 — ADR-0010; maintainer-approved ADOPT-DEFERRED 2026-06-03)
**Parent:** 368 (OSCAL export bundle signing ed25519 → cosign)

## Narrative

Slice 368 (5d) proposes swapping OSCAL export-bundle signing from the
in-process ed25519 detached signature to a multi-mode cosign/Sigstore
signer. That is a multi-week build that commits the project to an external
`cosign` binary dependency + Fulcio/Rekor as live dependencies + an OIDC
machine-identity for keyless signing. Before any of that code is written,
the maintainer wants the full picture, tradeoffs, and value laid out so the
direction (and whether to build it at all, or which modes) is an explicit
decision — not something an implementing engineer commits to unilaterally.

This slice is **decision-only: no production code ships.** It de-risks the
368 implementation slices by settling the load-bearing choices first.

## What ships

1. **A decision doc / ADR** (`docs/adr/NNNN-oscal-cosign-signing.md`, next ADR
   number) that lays out, in maintainer-readable form:

   - **The value, concretely.** Why stock-`cosign verify-blob`-able exports +
     a public Rekor transparency log matter for the v1 binary success
     criterion ("survive third-party security review"), vs. the status quo
     (auditors need bespoke handling of an ad-hoc embedded ed25519 public
     key). Name the actual auditor friction the current scheme creates.
   - **The three modes** 368 proposes (`cosign-keyless`, `cosign-kms`,
     `embedded-ed25519`) — what each requires at runtime, and the failure
     modes (Fulcio/Rekor outage, no OIDC identity, air-gap).
   - **The cost ledger.** External cosign binary (bundling vs operator
     install; cosign is Apache-2.0 — confirm license-bundling is clean per
     P0-368-4), Fulcio/Rekor live deps, the OIDC-identity question (does
     atlas's own AS federate with Sigstore, or use a public IdP?), CI needing
     the cosign binary, and the ~5d build.
   - **A recommendation** with a confidence level: ADOPT (all three modes /
     subset), ADOPT-DEFERRED (commit direction, build in v2), or
     DON'T-ADOPT (keep ed25519, document why the canvas §9 cosign commitment
     is deferred/revised). Include the recommended default mode per
     deployment shape (self-host air-gap vs SaaS).

2. **Re-scope slice 368** based on the recommendation: if ADOPT, update 368's
   doc to reference this ADR and (optionally) the maintainer files the
   implementation sub-slices; if DON'T-ADOPT, mark 368 `wont-do` with the ADR
   as rationale and update canvas §9 / the slice-030 revisit note.

## Acceptance criteria

- [ ] **AC-1.** ADR authored covering value, the 3 modes, cost ledger, OIDC-
      identity question, cosign license-bundling check, and a confidence-rated
      recommendation + per-deployment default.
- [ ] **AC-2.** No production code changed (decision-only). Docs/ADR only.
- [ ] **AC-3.** Slice 368 doc updated to reflect the decision (re-scoped,
      deferred, or marked wont-do with rationale).
- [ ] **AC-4.** If ADOPT: the implementation sub-slice breakdown is recorded
      (the 368 doc's day-1..day-5 plan becomes discrete ready/not-ready slices
      gated on this ADR).

## Dependencies

- **#368** (parent tracking slice) — this spike feeds its go/no-go.
- **#188** (OAuth AS client_credentials machine identity) — `merged`;
  relevant to the keyless OIDC-identity option.

## Anti-criteria (P0 — block merge)

- **P0-400-1.** Ships NO production code — if the spike tempts a "quick
  prototype", that belongs in a 368 implementation sub-slice, not here.
- **P0-400-2.** Does NOT auto-merge — the recommendation is for maintainer
  sign-off (this is the human-in-the-loop decision point for 368).

## Notes

This is the maintainer's requested "spike/ADR first, then decide" gate for 368. The implementation slices (cosign package, keyless mode, dispatch, CLI,
integration tests — 368's day-by-day plan) are deliberately NOT filed until
this ADR lands and the maintainer approves the direction.
