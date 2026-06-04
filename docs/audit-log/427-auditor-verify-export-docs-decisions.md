# Slice 427 — decisions log

Auditor-facing "verify a signed OSCAL export" page in the docs site.

- **Type:** JUDGMENT
- **detection_tier_actual:** none (no bug surfaced during the slice)
- **detection_tier_target:** manual_review (per the slice note — there is
  no automated verify-the-docs-command tier in v1; AC-11's hand-run check
  is the detection surface for a wrong-command bug)

## Decisions

### D1 — Nav section label: "For auditors" (not "Audit")

Per the slice note and to disambiguate from the existing **Audit logs**
nav entry (`audit-logs.md`, mkdocs.yml line 103), the new section is
labelled **For auditors** with a single child `Verify a signed OSCAL
export → audit/verify-export.md`. Placed immediately after the
Walkthroughs section, before REST API reference.

### D2 — Stock-cosign recipe uses the published public-key file, not the KMS ref

The runbook's auditor snippet shows `--key "awskms:///alias/atlas-oscal"`
(the manifest `key_ref`). Verified at build time (AC-11) that this path
requires live KMS credentials — an external auditor without KMS access
cannot use it (`atlas oscal verify` on a `cosign-kms` bundle failed with a
KMS `GetPublicKey`/IMDS credential error in the no-cloud harness). The
page therefore leads with the **operator-published public-key file**
(`cosign public-key … > atlas-oscal.pub`, then `--key atlas-oscal.pub`),
which verifies fully offline, and documents the `--key <key_ref>` form
only as a tip for auditors who do hold KMS access. This is also the
threat-model-S mitigation: the auditor trusts an out-of-band public key,
never a key embedded in the bundle.

### D3 — Page documents the stock-tool digest-recompute recipe explicitly

The runbook hand-waves the blob as `<digest-blob>`. For the page to be
self-sufficient (an auditor with no atlas tooling), it documents the
deterministic digest derivation as a runnable stock-tool recipe: `jq` over
`manifest.json` members → `filename:memberhash` lines → `LC_ALL=C sort` →
`sha256sum`, then `xxd -r -p` to get the **raw 32 digest bytes** that
`cosign verify-blob` reads on stdin. This was verified against a real
bundle (below) — the recomputed digest matched the manifest's
`signature.digest` exactly.

### D4 — Cross-link placement in the SSP-export walkthrough

Added as a new short section "10. Handing the Bundle to an Auditor" at the
natural handoff point (after the bundle is on disk), per the slice note
("once you hand the bundle to an auditor, they verify it as follows →").
The verify page links back to the walkthrough and to the operator runbook.

## AC-11 — commands confirmed against real signed bundles

Verified at build time (not in CI — manual_review tier, as noted). A
throwaway integration harness (`internal/oscal/zz_slice427_harness_test.go`,
**removed before commit**) wrote two real on-disk bundles using the
production `Signer.SignBundle` (embedded) and `KMSSigner.SignBundle` +
local-key cosign adapter (cosign-kms; the same `cosign sign-blob` /
`verify-blob` exec path the production `cosign.Client` drives for an
`awskms://` URI — see `internal/oscal/sign_cosign_integration_test.go`).

Then the **exact commands documented on the page** were hand-run:

1. **Digest recompute (stock tools):** `jq … | LC_ALL=C sort | sha256sum`
   on the cosign-kms `manifest.json` → recomputed
   `67b9d134…67ee9ad1`, **matched** the manifest's `signature.digest`.
2. **`cosign verify-blob` (published pubkey file):** with
   `--key atlas-oscal.pub --signature bundle.sig
--insecure-ignore-tlog=true bundle.digest` (raw 32-byte digest) →
   **`Verified OK`** (exit 0).
3. **Tamper check (threat-model T):** altered `ssp.json`; the recomputed
   bundle digest diverged (auditor stops at step 1), and feeding the
   tampered digest to `cosign verify-blob` returned **`invalid
signature`** (exit 1).
4. **`atlas oscal verify <embedded-bundle>`** → **`OK: … verifies
(mode=embedded-ed25519)`** (exit 0).

The existing `internal/oscal` + `internal/oscal/cosign` integration suites
(real `cosign` sign/verify round-trip + tamper-fail) also pass clean in
this worktree.

## P0 anti-criteria — confirmed

- **P0-427-1** No keyless/Fulcio/Rekor verification documented as
  available; an admonition explicitly states it is not available.
- **P0-427-2** All KMS references are placeholders
  (`awskms:///alias/<operator-key>`, `awskms:///alias/<your-key>`); no
  real ARN.
- **P0-427-3** Docs-only — no change under `internal/oscal/`,
  `cmd/atlas-cli/`, or the manifest format. (The harness test file was
  temporary and removed.)
- **P0-427-4** No operator KMS-configuration content duplicated; the page
  links to the runbook for that.
- **P0-427-5** Every documented command was hand-run against a real
  bundle (AC-11 above).
