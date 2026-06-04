# 451 — SLSA provenance + SBOM for binary / CLI / SDK releases

**Cluster:** Security
**Estimate:** M (1-2d)
**Type:** JUDGMENT

**Status:** `ready`

## Narrative

`release.yml` (the GoReleaser job) cosign-signs **only** the `checksums.txt`
file. It produces **no SBOM** and **no SLSA provenance attestation** for the Go
platform binaries, the `atlas-cli` binary, or the per-language SDK artifacts.
Meanwhile `container-publish.yml` already does the full supply-chain treatment
for **images**: `sbom: true` + `provenance: true` on the build-push step plus a
dedicated `actions/attest-build-provenance` step (SLSA-grade provenance,
push-to-registry).

That asymmetry sits exactly on the seam the project's central thesis exposes —
"diligence the diligence tool itself." A customer's security team verifying an
atlas **container** gets a signed SBOM and provenance; verifying an atlas
**binary or CLI download** gets a cosign-signed checksum file and nothing else.
For a GRC product, that gap is the kind of thing the diligence questionnaire
asks about by name.

This slice closes the asymmetry: SBOM + SLSA provenance for every release
artifact, plus a consolidated supply-chain verification guide so an external
verifier has one document covering containers, binaries, and OSCAL bundles.

**This slice supersedes the gap — it is net-new supply-chain hardening, not a
dependabot bump** (it has no single dependabot PR to supersede; it closes the
asymmetry those bumps' downstream consumers care about).

### What ships

1. **Per-release-artifact SBOM.** Enable GoReleaser's `sboms:` block (syft is
   already installed on the runner — `release.yml` leaves it on PATH precisely
   so a follow-up can enable this without a workflow change) emitting a
   CycloneDX (or SPDX — engineer JUDGMENT, recorded) SBOM per built artifact,
   uploaded to the GitHub Release.
2. **SLSA provenance (L3).** Add `actions/attest-build-provenance` to the
   GoReleaser job covering the binary + CLI + SDK artifacts (subject = the
   built artifacts' digests), mirroring the container job's attestation.
3. **Edge-image-signing decision (JUDGMENT).** Resolve the open question the
   container workflow's top-of-file comment flags: do `:edge` / `:main-<sha7>`
   images get a keyless-OIDC cosign signature, or do they deliberately stay
   unsigned (provenance + SBOM only)? Today they are deliberately unsigned —
   slice 207 D1 bound the keyless guarantee to release-cut tag identity. Confirm
   or revise that, and record the decision.
4. **Consolidated verification guide.** Document the `cosign verify-attestation`
   / `cosign verify-blob` / `slsa-verifier` paths for containers + binaries +
   OSCAL bundles in `docs/RELEASE_READINESS.md`, with copy-pasteable, **anyone
   can run it** commands. The verify path must be independently runnable by a
   third party with no atlas-internal tooling.

## Threat model

STRIDE pass — supply-chain integrity is the entire point of this slice.

**S — Spoofing**

- _Threat:_ Without provenance binding the binary to the exact workflow
  identity that built it, an attacker who compromises a release channel can swap
  a malicious binary and a verifier has no cryptographic way to detect the
  builder identity mismatch.
- _Mitigation:_ `actions/attest-build-provenance` binds the artifact digest to
  the GitHub Actions OIDC workflow identity (the same federation Fulcio already
  trusts for the existing checksums signature). The verify guide shows how to
  assert the builder identity.

**T — Tampering**

- _Threat:_ A tampered binary that is not covered by the checksums file (e.g. a
  smuggled extra artifact) ships unsigned and unattested.
- _Mitigation:_ Provenance attestation covers each artifact's digest
  individually, not just an aggregate checksums file. SBOM lets a verifier
  detect an unexpected dependency injected into the build.
- _Anti-criterion:_ P0-451-2.

**R — Repudiation**

- _Threat:_ No transparency-log record of _what_ was built ("which deps were in
  this binary?") leaves disputes unresolvable.
- _Mitigation:_ The SBOM (uploaded per release) + the attestation's Rekor entry
  provide the durable, third-party-verifiable record.

**I — Information disclosure**

- _Threat:_ An SBOM can over-disclose internal package paths, private module
  names, or build-host details; a provenance statement can leak internal
  workflow/env metadata.
- _Mitigation:_ Review the generated SBOM + provenance for internal-only
  identifiers before the format is locked; the repo is private now but releases
  are the public-facing artifact. Do NOT include build-time secrets, env-var
  values, or non-public internal hostnames in the attestation.
- _Anti-criterion:_ P0-451-3.

**D — Denial of service**

- _Threat:_ Best-effort SBOM generation (syft is `continue-on-error: true`)
  could let a silently-empty SBOM ship, giving false assurance.
- _Mitigation:_ Gate the SBOM step so an empty/zero-component SBOM fails the
  release (do not leave SBOM generation best-effort once it is a published,
  diligence-relevant artifact).
- _Anti-criterion:_ P0-451-4.

**E — Elevation of privilege**

- _Threat:_ The attestation step needs `id-token: write` + `attestations:
write`; over-broad permissions on the GoReleaser job widen the blast radius
  of a workflow compromise.
- _Mitigation:_ Scope the new permissions at the job level (the release job
  already pins `id-token: write`; add `attestations: write` only). Do not add
  workflow-level permissions.
- _Anti-criterion:_ P0-451-5.

## Acceptance criteria

- [ ] **AC-1.** GoReleaser emits an SBOM (CycloneDX or SPDX — choice recorded in
      the decisions log) per release artifact, uploaded to the GitHub Release.
- [ ] **AC-2.** `actions/attest-build-provenance` runs in the GoReleaser job,
      attesting the binary + CLI + SDK artifact digests (SLSA provenance,
      push to the release / OIDC-bound).
- [ ] **AC-3.** The new job permissions are scoped at the job level
      (`attestations: write` added; `id-token: write` already present); no
      workflow-level permission widening.
- [ ] **AC-4.** SBOM generation is **gated**, not best-effort: an
      empty/zero-component SBOM fails the release (the existing
      `continue-on-error` on syft download is reconciled with this).
- [ ] **AC-5.** Edge-image-signing decision recorded in the decisions log
      (sign `:edge`/`:main-<sha7>` keyless, OR keep deliberately unsigned with
      rationale) — and `container-publish.yml`'s top-of-file comment updated to
      reflect the resolved decision.
- [ ] **AC-6.** `docs/RELEASE_READINESS.md` gains a consolidated supply-chain
      verification section covering containers, binaries/CLI/SDK, and OSCAL
      bundles, with independently-runnable `cosign verify-*` /
      `slsa-verifier` commands.
- [ ] **AC-7.** The verify path is proven runnable: a snapshot/dry GoReleaser
      run produces an SBOM + attestation that the documented commands verify
      (evidence in the PR body, or a self-verify step in the workflow mirroring
      release.yml's existing "Self-verify signed checksums" step).
- [ ] **AC-8.** Existing checksums cosign-signing + self-verify step is
      unchanged and still green (additive change only).
- [ ] **AC-9.** `pre-commit run --all-files` passes; CI green.
- [ ] **AC-10.** JUDGMENT decisions log at
      `docs/audit-log/451-slsa-provenance-sbom-decisions.md` records the SBOM
      format choice, the edge-signing decision, and the SBOM-gating choice with
      per-decision confidence + detection-tier fields.

## Constitutional invariants honored

- **Survive third-party security review / diligence the diligence tool
  (canvas §6, v1 binary criterion).** Closing the binary-vs-image supply-chain
  asymmetry is squarely this criterion.
- **Evidence integrity (invariant #2) / canvas §9 supply-chain commitments.**
  Provenance + SBOM strengthen the chain of trust around released artifacts.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — "Evidence integrity — cosign signing …
  Full Sigstore transparency-log in v3"; supply-chain commitments.
- `Plans/canvas/06-risk.md` / canvas §6 — v1 binary success criterion.
- ADR-0010 "Scope precision" (the release-artifacts row vs the OSCAL-bundle row
  — this slice is the release-artifacts row).
- Slice 084 (cosign v3 migration of release signing).
- Slice 207 (edge channel; `:edge` images deliberately unsigned — D1).

## Dependencies

- Supersedes the supply-chain asymmetry gap (no single dependabot PR; the gap
  the existing image-side provenance highlights by contrast).
- **#084** (cosign v3 release signing) — `merged`; this slice is additive to it.
- **#207** (edge channel) — `merged`; this slice resolves its deferred
  edge-signing question.
- Cross-ref **ADR-0010** (defines the two distinct signing surfaces; this slice
  is the CI-time release-artifact surface, NOT the runtime OSCAL-bundle surface).

## Anti-criteria (P0 — block merge)

- **P0-451-1.** Does NOT touch the runtime OSCAL-bundle signing path
  (`internal/oscal/sign.go`) — that is slices 368/413/414's surface (ADR-0010
  row 2). This slice is strictly the CI-time release-artifact surface (row 1).
- **P0-451-2.** Does NOT attest only the aggregate checksums file — provenance
  must cover each artifact digest.
- **P0-451-3.** Does NOT publish an SBOM or provenance statement containing
  build-time secrets, env-var values, or non-public internal hostnames.
- **P0-451-4.** Does NOT ship an empty/zero-component SBOM as if it were valid
  (gate it; do not leave it best-effort once published).
- **P0-451-5.** Does NOT widen workflow-level permissions — new permissions are
  job-scoped.
- **P0-451-6.** Does NOT auto-merge — supply-chain + a JUDGMENT edge-signing
  decision; maintainer reviews.

## Skill mix (3-5)

- `ci-cd-pipeline-builder` — wire the GoReleaser `sboms:` + attestation steps.
- `release-manager` — the release-artifact + verification-guide shape.
- `dependency-auditor` — SBOM format + slsa-verifier path.
- `security-review` — the permissions scoping + disclosure review.
- `runbook-generator` — the consolidated verification guide.

## Notes for the implementing agent

- syft is already installed on the release runner (`release.yml` "Install syft"
  step, left on PATH deliberately — see its comment). GoReleaser auto-invokes
  syft for `sboms:` if present. The minimal change is enabling the
  `sboms:` block in `.goreleaser.yaml` + adding the attestation step in
  `release.yml`.
- Mirror the container job's proven pattern:
  `actions/attest-build-provenance@<pinned-sha>` with
  `subject-name` + `subject-digest`. For multiple artifacts, the action accepts
  a digest set / a subject-path glob.
- The verification guide (AC-6) is the diligence-facing deliverable — write it
  for an external auditor who has only the public release page and stock
  cosign/slsa-verifier, no atlas-internal context.
- Edge-signing (AC-5) is the JUDGMENT call: slice 207 D1 deliberately left
  `:edge` unsigned (keyless guarantee bound to tag identity). Confirm that
  still holds, or revise — either is defensible; record which and why.
