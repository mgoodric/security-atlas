# Slice 451 — SLSA provenance + SBOM for binary / CLI / SDK releases · decisions log

**Type:** JUDGMENT
**Slice:** `docs/issues/451-slsa-provenance-sbom-binary-cli-sdk-releases.md`

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

> One real bug surfaced **during** the slice and was caught at the cheapest
> available tier for a tag-triggered workflow (manual review of the action's
> `--help` against the YAML): the workflow self-verify step initially used
> `--cert-identity-regexp` (the cosign spelling) where `gh attestation verify`
> wants `--cert-identity-regex`. The release workflow is tag-triggered and
> cannot run on a PR, so no CI tier could have caught it pre-merge — manual
> review IS the target tier here. `actionlint` validated the YAML/shell but
> does not know action-specific flag names. Recorded as `actual == target`
> (not a gap) because no cheaper automated tier exists for this surface.

---

## Decisions made

### D1 — SLSA generator: `actions/attest-build-provenance` (not `slsa-github-generator`, not goreleaser-native)

**Options considered:**

1. `actions/attest-build-provenance` — GitHub's first-party action; mints a SLSA
   provenance attestation bound to the GitHub Actions OIDC workflow identity via
   Fulcio, writes it to the repo attestations store, verifiable with stock `gh
attestation verify` / `cosign` / `slsa-verifier`.
2. `slsa-framework/slsa-github-generator` — a separate reusable workflow that
   produces a SLSA L3 provenance file; heavier (a whole called-workflow), and it
   wants to own the build, which collides with GoReleaser owning the build.
3. GoReleaser-native provenance — GoReleaser can emit an in-toto provenance
   predicate, but it is not OIDC-identity-bound the way the GitHub action is, and
   it would diverge from the proven container path.

**Chosen:** Option 1 — `actions/attest-build-provenance@a2bbfa25…` (v4), pinned to
the **exact same SHA** the container job (`container-publish.yml`) already uses.

**Rationale:** the spec (notes + AC-2) explicitly points at this action, and the
repo already runs it successfully for container images. Reusing the proven,
in-repo pattern is the lowest-risk path, keeps one mental model for a verifier
("atlas provenance == a GitHub Actions OIDC attestation"), and gives per-artifact
digest binding via `subject-path` globbing every release asset — which satisfies
P0-451-2 (no aggregate-only attestation). `slsa-github-generator` would have meant
re-architecting the build ownership for no verifier-facing benefit.

**Confidence:** high.

### D2 — SBOM format: SPDX JSON (not CycloneDX)

**Options considered:** SPDX (`spdx-json`) vs CycloneDX (`cyclonedx-json`). syft and
GoReleaser support both equally; the choice is about ecosystem fit, not capability.

**Chosen:** SPDX JSON (`spdx-json`), emitted as `<artifact>.spdx.sbom.json`.

**Rationale:**

- **Cross-surface consistency.** `container-publish.yml`'s buildx `sbom: true`
  emits SPDX by default. Choosing SPDX for the binary surface gives a diligence
  verifier **one format across both supply-chain surfaces** (containers and
  binaries), rather than asking them to handle SPDX for images and CycloneDX for
  binaries.
- **Tooling lingua franca.** `slsa-verifier`, `gh`, and most stock SBOM consumers
  treat SPDX as the canonical interchange format; SPDX is also the ISO/IEC 5962
  standard.
- CycloneDX is equally defensible (richer vulnerability/VEX affordances); the
  cross-surface-consistency argument is what tips it. See revisit list.

**Confidence:** medium-high.

### D3 — SBOM scope: per-archive (not aggregate, not per-binary)

**Options considered:** one SBOM per released **archive** (`artifacts: archive`),
one aggregate SBOM for the whole release, or one per raw binary.

**Chosen:** per-archive (`artifacts: archive` in `.goreleaser.yaml` `sboms:`).

**Rationale:** AC-1 says "per release artifact"; the archive is the unit a user
actually downloads and a verifier actually checks (the cli-only and bundle
archives are distinct downloads). Ten archives → ten SBOMs, each scoped to exactly
what that download contains. An aggregate SBOM would blur which components ship in
the CLI-only vs the bundle artifact and reads as the false-assurance shape P0-451-2
warns against. Snapshot build confirmed 10 SBOMs, 19–118 components each.

**Confidence:** high.

### D4 — SBOM gating: hard-fail on empty/zero-component (reconcile `continue-on-error`)

**Options considered:** keep syft best-effort (`continue-on-error: true`, the
pre-slice posture) vs make the SBOM a gated, must-succeed artifact.

**Chosen:** gated. Removed `continue-on-error` from the syft-download step AND added
a dedicated **"Gate SBOMs (fail on empty / zero-component)"** workflow step that
asserts (a) at least one SBOM exists and (b) every SBOM is valid SPDX JSON with a
non-zero `.packages` count.

**Rationale:** AC-4 / P0-451-4 — once the SBOM is a published, diligence-relevant
artifact, a silently-empty SBOM is _false assurance_, which is worse than no SBOM.
The download step's `continue-on-error` made sense when `sboms:` was not declared
(syft was optional); now GoReleaser hard-invokes syft per archive, so a missing or
empty SBOM must fail the release. The gate's negative path was proven locally: an
injected `{"packages":[]}` SBOM exits 1 ("GATE CORRECTLY FAILED").

**Confidence:** high.

### D5 — Edge-image signing: stay deliberately UNSIGNED (re-confirm slice 207 D1)

**Options considered:** sign `:edge` / `:main-<sha7>` images with keyless cosign, OR
keep them unsigned (provenance + SBOM only — today's behavior).

**Chosen:** keep them deliberately unsigned. Updated `container-publish.yml`'s
top-of-file comment to record that slice 451 reviewed and re-confirmed this.

**Rationale:** a cosign keyless signature is most useful when it binds to a stable,
human-meaningful, promoted identity — a release-cut tag. A `:main-<sha7>` image is a
moving, non-promoted, per-merge target; its trust anchor is the **provenance
attestation** (which binds builder identity and is already present), not a
signature. Signing every main-push image would dilute the load-bearing invariant
"an atlas cosign signature means a _released_ artifact" without giving a verifier
anything actionable they don't already get from the attestation. slice 207 D1 made
this call deliberately; nothing in the supply-chain landscape since changes it.

**Confidence:** high.

### D6 — Self-verify via `gh attestation verify` (mirror the existing checksums self-verify)

**Chosen:** add a **"Self-verify SLSA provenance (round-trip)"** step that runs
`gh attestation verify` against a representative subject from each artifact class,
asserting the cert identity is THIS repo's `release.yml` workflow — mirroring the
existing "Self-verify signed checksums" step (AC-7 / AC-8 unchanged-and-additive).

**Rationale:** AC-7 wants the verify path proven runnable; the existing checksums
self-verify is the in-repo precedent for "break on arrival, not in the wild."
`gh` ships on the runner (no atlas-internal tooling), so the self-verify mirrors
exactly what an external verifier runs from §12.2 of `RELEASE_READINESS.md`.

**Confidence:** high.

---

## Revisit once in use

- **D2 (SPDX vs CycloneDX)** — re-evaluate if/when the project adds VEX
  (Vulnerability Exploitability eXchange) statements or a vuln-scanning gate over
  released SBOMs. CycloneDX's native VEX support could tip the choice; today the
  cross-surface-consistency argument dominates. Low cost to switch (`spdx-json` →
  `cyclonedx-json` in one `.goreleaser.yaml` arg) — but switch _both_ surfaces
  together or the consistency argument inverts.
- **D5 (edge unsigned)** — revisit if a consumer materially relies on running
  `:edge` in a trust-sensitive context (e.g. a downstream pins `:edge` in a
  production deploy and their diligence asks for a signature, not just
  provenance). The attestation is the answer today; document that in the consumer's
  question rather than reflexively signing edge.
- **Provenance subject-path globbing** — once a real tagged release runs, confirm
  the `subject-path` globs (`dist/*.tar.gz`, `*.zip`, `*_checksums.txt`,
  `*.spdx.sbom.json`) match exactly the intended asset set and nothing stray in
  `dist/` (e.g. the `metadata.json` / `artifacts.json` GoReleaser writes) gets
  attested unintentionally. The current globs are class-scoped specifically to
  avoid that, but a tagged-run sanity check is the real proof.
- **SDK artifacts** — when per-language SDK release artifacts actually start
  flowing through this GoReleaser run (they are named in the matrix but the SDK
  archive shapes may evolve), confirm each SDK archive lands in `dist/` matching
  the provenance/SBOM globs. The verify guide §12.2 already covers them by
  substitution, but a real SDK release is the proof.

---

## Confidence summary

| Decision                               | Confidence  |
| -------------------------------------- | ----------- |
| D1 — attest-build-provenance generator | high        |
| D2 — SPDX SBOM format                  | medium-high |
| D3 — per-archive SBOM scope            | high        |
| D4 — hard-gate empty SBOM              | high        |
| D5 — edge stays unsigned               | high        |
| D6 — gh-based self-verify              | high        |

---

## Licensing note (project licensing discipline)

The supply-chain tooling this slice invokes is license-clean to bundle into the
release pipeline:

- **syft** (Anchore) — Apache-2.0. Invoked by GoReleaser's `sboms:` block on the
  runner; no redistribution of syft itself, only its SPDX output (which carries no
  syft-license obligation onto the SBOM).
- **actions/attest-build-provenance** (GitHub) — MIT.
- **slsa-verifier** (referenced in the verify guide, not invoked in CI) —
  Apache-2.0.
- **cosign** (Sigstore) — Apache-2.0 (already in use pre-slice).

No copyleft tooling enters the release path; no tool is _redistributed_, only
invoked. The SBOM/provenance _artifacts_ produced are data, not licensed code.
