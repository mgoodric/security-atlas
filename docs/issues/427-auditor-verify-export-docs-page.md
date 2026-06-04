# 427 — Publish the auditor-facing "verify a signed OSCAL export" page in the docs site

**Cluster:** Docs
**Estimate:** S
**Type:** JUDGMENT
**Status:** `merged` (`2dcb70be`, #974 — auditor verify-export docs-site page)

## Narrative

**Why.** The working "verify a signed OSCAL bundle" flow already exists — `docs/runbooks/oscal-signing.md` (Auditor verification section, ~lines 155-189) documents both the `cosign verify-blob` path for `cosign-kms` bundles and the `atlas oscal verify` path for `embedded-ed25519` bundles. But that runbook is an internal operator/maintainer document: it is **not** in `docs-site/mkdocs.yml` nav, so it never reaches the published documentation site an auditor reads. Independent, third-party verification of a signed export is the entire payoff of slice 413 / [ADR-0010](../adr/0010-oscal-cosign-signing.md) — and the v1 binary criterion is "survive third-party diligence". An auditor who cannot find a published, auditor-voiced page on how to verify a bundle gets none of that value.

**What.** Add one new published page `docs-site/docs/audit/verify-export.md`, written in **auditor voice** — stock `cosign` tooling only, no atlas internals, no operator-side configuration. The page tells an external auditor who has been handed a signed export bundle exactly how to confirm it is authentic and untampered. Add a nav entry under a new "For auditors" (or "Audit" — see decisions) section in `docs-site/mkdocs.yml`. Cross-link to it from the existing OSCAL SSP-export walkthrough (`docs-site/docs/walkthroughs/oscal-ssp-export.md`).

**Scope discipline.** This is a docs-only slice. It does NOT change `internal/oscal/`, the CLI, the manifest format, or the signing modes. It does NOT re-document operator-side KMS configuration (that stays in the runbook). It surfaces the auditor-facing subset of the existing runbook on the published site and keeps it correct against the shipped CLI/cosign commands. The runbook stays as the operator/maintainer source of truth; the new page is the auditor-facing derivative.

## Threat model

Docs slices still get a STRIDE pass: a verification guide that publishes a **wrong** command is a trust/integrity threat — an auditor who runs a broken or insecure verification command and sees it "pass" has been given false assurance, which directly undermines the diligence-survival criterion.

**S — Spoofing.** No new endpoints. The page documents how an auditor confirms the signer's identity (the manifest's `signature.key_ref` / published KMS public key for `cosign-kms`; the embedded ed25519 public key for `embedded-ed25519`). _Anti-criterion:_ the page MUST NOT instruct the auditor to trust a key supplied inside the same bundle without independent corroboration for the `cosign-kms` case — it must direct them to the operator-published KMS public key (`cosign public-key`) or the manifest's `key_ref`, not a bundle-embedded secret.

**T — Tampering (load-bearing).** The whole point of the page is tamper-detection. The published commands MUST exactly match the shipped behavior: the bundle digest is the sha256 over sorted `filename:memberhash` lines, and `cosign verify-blob` / `atlas oscal verify` validate the signature over that digest. _Threat:_ a command that silently passes on a tampered bundle (e.g. omitting `--insecure-ignore-tlog=true` and having cosign error for the wrong reason, or verifying the wrong blob). _Mitigation:_ every command on the page is copied from the verified runbook and an AC requires it round-trips against a real signed bundle.

**R — Repudiation.** N/A for the auditor verification path itself (read-only). The page may note that the operator-side export already records what was signed.

**I — Information disclosure.** The page is published publicly. _Threat:_ leaking an operator's real KMS ARN / key reference as a copy-pasteable default. _Anti-criterion:_ all key references on the page are placeholders (`awskms:///alias/<your-key>`), never a real ARN.

**D — Denial of service.** N/A (static page).

**E — Elevation of privilege.** N/A (no auth surface; verification is offline/read-only).

## Acceptance criteria

- [ ] **AC-1.** New file `docs-site/docs/audit/verify-export.md` exists, written in auditor voice (second person, "you have been handed a bundle"), referencing only stock `cosign` and `atlas oscal verify` — no operator KMS-configuration content.
- [ ] **AC-2.** The page documents the `cosign-kms` verification path with a `cosign verify-blob` invocation that matches the runbook (`--key`, `--signature`, `--insecure-ignore-tlog=true`, the digest blob).
- [ ] **AC-3.** The page documents the `embedded-ed25519` verification path via `atlas oscal verify <bundle-dir>`.
- [ ] **AC-4.** The page explains how the bundle digest is derived (sha256 over sorted `filename:memberhash` lines) so an auditor understands what tamper-detection covers.
- [ ] **AC-5.** The page explains how to determine which mode a bundle used (the `manifest.json` `signature.mode` field) so the auditor picks the right verification path.
- [ ] **AC-6.** All KMS key references on the page are placeholders, not real ARNs (threat-model I).
- [ ] **AC-7.** A nav entry pointing at `audit/verify-export.md` is added to `docs-site/mkdocs.yml` under an auditor-facing section.
- [ ] **AC-8.** The OSCAL SSP-export walkthrough (`docs-site/docs/walkthroughs/oscal-ssp-export.md`) gains a cross-link to the new verify page.
- [ ] **AC-9.** The new page cross-links back to the operator runbook (`docs/runbooks/oscal-signing.md`) for the configuration side, without duplicating its operator content.
- [ ] **AC-10.** `mkdocs build --strict` passes from `docs-site/` (no broken internal links, no missing-nav warnings) — this is the mechanical gate that the page is wired in and its links resolve.
- [ ] **AC-11.** The two documented verification commands are confirmed against a real signed bundle (e.g. produce a `cosign-kms` and an `embedded-ed25519` bundle, run the exact commands from the page, confirm pass; tamper one member, confirm fail) and the result is noted in the decisions log.

## Constitutional invariants honored

- **OSCAL is the wire format** (#8) — the page is about verifying the exported OSCAL bundle, the canonical audit-binding artifact.
- **Evidence integrity** (tech stack: sha256 content-hash + cosign signing) — the page operationalizes the verifier side of that commitment for an external party.
- **AI-assist boundary** — verification is deterministic and human-run; no inference surface is introduced.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` — auditor role, OSCAL export, audit-period freezing.
- `Plans/canvas/09-tech-stack.md` — evidence integrity (cosign signing of audit-export bundles).
- [ADR-0010](../adr/0010-oscal-cosign-signing.md) — the signing-mode design authority.

## Dependencies

- **#413** (cosign-kms + embedded-ed25519 signing, Phase 1) — `merged`. The runbook and CLI this page surfaces already exist.
- **ADR-0010** — `merged`. Design authority for the signing modes.
- (Informational) **#414** (cosign-keyless Fulcio/Rekor) — NOT a dependency; the page covers only the two shipped Phase-1 modes and must not document keyless verification as available.

## Anti-criteria (P0 — block merge)

- **P0-427-1.** Does NOT document `cosign-keyless` (Fulcio/Rekor/tlog) verification as available — that mode is slice 414 and not shipped. The page covers only `cosign-kms` and `embedded-ed25519`.
- **P0-427-2.** Does NOT publish a real KMS ARN / key reference; placeholders only (threat-model I).
- **P0-427-3.** Does NOT change any code under `internal/oscal/`, `cmd/atlas-cli/`, or the manifest format — docs-only.
- **P0-427-4.** Does NOT duplicate the operator-side KMS-configuration content into the auditor page; it links to the runbook for that.
- **P0-427-5.** Does NOT publish a verification command that the shipped CLI/cosign does not actually run (AC-11 is the guard).

## Skill mix (3-5)

- `grill-with-docs` — align the auditor page against the runbook + ADR-0010 + canvas §8.
- `Security` — threat-model verification (the published-wrong-command integrity threat).
- `simplify` — keep the auditor page tight (one screen, two commands).
- `verify` — run the two documented commands against real bundles (AC-11).

## Notes for the implementing agent

- The source content already exists and is correct: `docs/runbooks/oscal-signing.md` "Auditor verification" section. Lift the **auditor-facing** subset only; leave the operator KMS-config sections in the runbook.
- `mkdocs build --strict` is the load-bearing gate here — the project's existing nav uses `navigation.expand` and the git-revision-date plugin with `strict: false` on its own dates but mkdocs itself runs `--strict` for broken links / missing nav. A page that exists but isn't in nav, or a broken cross-link, fails the build.
- Naming the nav section: prefer "For auditors" over "Audit" to disambiguate from the existing "Audit logs" nav entry. Record the chosen label in the decisions log.
- The SSP-export walkthrough already ends at "signed bundle"; the cross-link belongs at that handoff point ("once you hand the bundle to an auditor, they verify it as follows → ").
- Detection-tier target for any command-correctness bug here is `manual_review` (AC-11's hand-run check) — there is no automated verify-the-docs-command tier in v1; note that in the decisions log.
