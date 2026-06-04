# 423 — End-to-end test for the OSCAL signed-export download chain

**Cluster:** Quality
**Estimate:** 0.5d (S)
**Type:** AFK
**Status:** `merged` (`7298f546`, #981 — OSCAL export e2e; download-surface→457)
**Priority:** P1

## Narrative

**WHY.** OSCAL export is a v1-binary success criterion — the auditor
hand-off is "does the operator run their next SOC 2 out of
security-atlas" (CLAUDE.md). The Go side
(`internal/api/oscalexport/handler.go`) has unit/integration tests and a
98 coverage floor, but the **browser-driven** generate → stream →
download chain is never exercised end-to-end. The only OSCAL e2e
(`web/e2e/audits-list.spec.ts`, the "slice 217 / AC-A4" test ~line 165)
asserts a _disclosure placeholder_ that replaced a disabled button — it
verifies the capability is honestly disclosed, not that the real export
works. So the highest-stakes v1 artifact (the bundle the auditor
receives) has no full-stack e2e behind it. Slice 413 added cosign-kms +
embedded-ed25519 signing modes; the signed bundle is now the artifact,
which raises the stakes further.

**WHAT.** A Playwright spec that drives `/audits` → triggers an OSCAL
bundle export → asserts the download fires with the correct
`Content-Type` (and, where the signed-export path is reachable in the
e2e harness, that the bundle carries the signing manifest / `.sig`
sidecar). Mirror `web/e2e/board-pack-export-e2e.spec.ts` (slice 388),
which already establishes the download-assertion pattern for the
board-pack export.

**SCOPE DISCIPLINE.** This is a download-chain e2e — it asserts the
download fires + the content type + (best-effort) the signed-bundle
shape. It does NOT re-verify cosign signatures inside the browser
(that's the Go integration tier's job — see slice 425) and it does NOT
add a new export feature. If the e2e harness cannot reach the signed
path (KMS unavailable in CI), the spec asserts the embedded-ed25519
default path and notes the signed-path coverage as deferred to the Go
integration tier.

## Threat model

**S — Spoofing.** The export endpoint must require an authenticated
operator session.

- Mitigation: the spec authenticates via the standard e2e JWT bearer
  fixture (slice 201 pattern); an unauthenticated download attempt is
  out of scope for _this_ spec but the authenticated path is the only
  one driven.

**T — Tampering.** A tampered bundle should be detectable via the
signature.

- Mitigation: the signed-bundle assertion confirms the `.sig` /
  manifest mode is present; cryptographic verify is the Go tier (slice
  425). The e2e proves the signed artifact is _produced_, closing the
  "does the chain actually emit a signed bundle" gap.

**R — Repudiation.** Export is an audit-relevant action.

- Mitigation: no new audit surface; if the handler already writes an
  export audit row, the spec need not assert it (Go integration covers
  it). The e2e proves the operator-visible chain.

**I — Information disclosure (HEADLINE).** The export must respect tenant
scope and the audit-period freeze — a bundle must not contain another
tenant's evidence or post-freeze records.

- Mitigation: the spec runs as a single tenant's operator and asserts
  the download succeeds for that tenant's audit period; it does NOT
  attempt cross-tenant export (that negative case belongs to the Go
  integration tier, which can assert RLS denial directly). The e2e
  confirms the positive in-scope path; the audit-period-freeze invariant
  is asserted at the data tier, not re-driven here.

**D — Denial of service.** A large bundle could stream unbounded.

- Mitigation: out of scope — the spec drives a seed-data-sized period;
  streaming caps are a handler concern already covered by Go tests.

**E — Elevation of privilege.** Export must be gated to operators, not
read-only viewers.

- Mitigation: the spec uses an operator-role bearer; the role gate is
  enforced by the handler (Go-tested). The e2e confirms the operator
  path works.

**Verdict:** `has-mitigations`. Information-disclosure (tenant scope +
freeze) is the headline; the e2e covers the in-scope positive path and
defers the negative/crypto cases to the Go tier by design.

## Acceptance criteria

- [ ] **AC-1 (test).** A new Playwright spec
      (e.g. `web/e2e/oscal-export-e2e.spec.ts`) navigates from `/audits`
      to the per-period detail and triggers the OSCAL bundle export.
- [ ] **AC-2 (test).** The spec asserts the browser download event fires
      (Playwright `page.waitForEvent("download")` or equivalent),
      mirroring `web/e2e/board-pack-export-e2e.spec.ts`.
- [ ] **AC-3 (test).** The spec asserts the downloaded artifact's
      `Content-Type` (and suggested filename, if surfaced) matches the
      OSCAL bundle content type the handler sets.
- [ ] **AC-4 (test).** Where the signed-export path is reachable in the
      e2e harness, the spec asserts the bundle carries the signing-mode
      manifest and/or a `.sig` sidecar (slice 413 modes). If unreachable,
      the spec asserts the embedded-ed25519 default and documents the
      signed-path deferral in a code comment.
- [ ] **AC-5 (test).** The spec runs against the docker-compose bring-up
      seed data — no preconditions the bootstrap cannot provide
      (`web/e2e/README.md` rule); a seed gap is filed as a spillover, not
      worked around.
- [ ] **AC-6.** The spec is enrolled in the `Frontend · Playwright e2e`
      CI job and passes there; a failed run uploads the HTML report +
      traces (existing job behavior).
- [ ] **AC-7.** The slice-217 disclosure spec in
      `web/e2e/audits-list.spec.ts` is left intact (it asserts an
      orthogonal honesty property) — the new spec does not replace it.

## Constitutional invariants honored

- **OSCAL is the wire format (invariant #8).** The e2e drives the
  SSP/AP/AR export bundle — the canonical export wire format.
- **Audit-period freezing (invariant #10).** The export runs against an
  audit period; the freeze semantics are data-tier-enforced and the e2e
  exercises the in-scope export path.
- **Manual evidence is first-class (invariant #9).** The exported bundle
  draws from the unified evidence ledger regardless of source.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` — OSCAL export + audit-period
  freezing (the v1-binary auditor hand-off).
- `web/e2e/README.md` — spec-precondition rules.
- ADR-0010 (`docs/adr/0010-oscal-cosign-signing.md`) — the signing modes.

## Dependencies

- **#388** (board-pack export e2e) — `merged`. The download-assertion
  pattern this spec mirrors.
- **#413** (cosign-kms + embedded signing Phase 1) — `merged`. The signed
  bundle this spec asserts the shape of.
- **#217** (OSCAL export disclosure) — `merged`. The disclosure spec this
  slice preserves.

## Anti-criteria (P0 — block merge)

- **P0-423-1.** Does NOT re-verify cosign signatures cryptographically in
  the browser — signature verification is the Go integration tier (slice
  425). The e2e asserts the signed artifact is _produced_, not valid.
- **P0-423-2.** Does NOT add a cross-tenant export negative case to the
  e2e — RLS denial is asserted at the Go integration tier where the role
  context is directly controllable.
- **P0-423-3.** Does NOT relax a spec precondition the docker-compose
  bring-up cannot provide — a seed gap is a spillover slice (`web/e2e/README.md`).
- **P0-423-4.** Does NOT delete or weaken the slice-217 disclosure
  assertion.
- **P0-423-5.** Does NOT modify `_STATUS.md` from inside this slice's own
  commits.

## Skill mix (3-5)

- `engineering-advanced-skills:browser-automation` (Playwright spec)
- `tdd` (assert-first e2e)
- `verify` (run the app, confirm the download fires)
- `simplify` (pre-PR)

## Notes for the implementing agent

- `web/e2e/board-pack-export-e2e.spec.ts` (slice 388) is the canonical
  download-assertion template — copy its `waitForEvent("download")` +
  content-type assertion shape.
- The Go handler is `internal/api/oscalexport/handler.go` (98 floor) —
  read it for the exact `Content-Type` + filename the spec must assert.
- The signing manifest mode is recorded per slice 413 (`Mode`
  discriminator in the export manifest). If the CI harness has no KMS,
  the default is `embedded-ed25519` — assert that path and leave a
  comment pointing at slice 425 for the real-KMS round-trip.
