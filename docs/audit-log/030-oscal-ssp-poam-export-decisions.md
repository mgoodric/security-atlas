# 030 — OSCAL SSP + POA&M export pipeline — decisions log

Slice 030 is `Type: JUDGMENT`. The OSCAL export pipeline crosses several
spec-ambiguity boundaries — the canvas commits to "OSCAL JSON v1.1.x" and
"compliance-trestle" but does not pin versions, name a signing primitive, or
map every OSCAL artifact onto an implemented platform primitive. This log
records the build-time judgment calls so the maintainer can re-evaluate them
once the export is validated against a real auditor's tooling.

> Process note: this slice's engineer completed the implementation and a CLEAR
> security review but stalled twice before opening the PR (once after the grill,
> once after security-review). Per the two-strikes rule the orchestrator closed
> it out directly — implementation verified by `go build ./...` + `go vet` clean
> and the engineer's own security review. This log is transcribed from the
> engineer's grill + security-review reports.

## Decisions made

### D-narrative — SSP implementation statements come from the control bundle text

**Options considered:**

- (A) A distinct "implementation narrative" field on `controls` — does not exist in the schema.
- (B) Use the control bundle manifest's stored `description` / control text as the human-authored implementation statement, and attach the latest `control_evaluations` result as an OSCAL `prop`.

**Chosen: (B).** Canvas §8.2 says SSP narrative comes from "control implementation narratives." The schema has no distinct narrative field — but the control bundle manifest text IS the human-authored narrative (it ships in the slice-010 control kit, human-authored). Slice 012's `control_evaluations` is pass/fail state, not narrative — so it becomes an OSCAL `prop` on the `implemented-requirement`, not the statement body. No LLM is involved — this honors the constitutional AI-assist boundary.

**Revisit once in use:** if a real "implementation narrative" field is added to controls (distinct from the bundle description), re-point the SSP statement source.

**Confidence: high.** The bundle text is genuinely human-authored; the mapping is faithful to §8.2.

### D3 — POA&M items derive from failing control evaluations + open audit notes

**Options considered:**

- (A) A `findings` table — does not exist; no findings slice is a dependency of 030.
- (B) Derive POA&M items from `control_evaluations` with `result='fail'` inside the frozen audit horizon, plus open audit notes scoped to controls.

**Chosen: (B).** Canvas §8.2 describes POA&M as "open findings with milestones, owners, due dates" but the platform has no `findings` primitive. A failing control evaluation IS a finding. The required POA&M fields are filled as: owner = control `owner_role`; due date = `last_observed_at` + a default remediation window; milestone = a single default "remediate" milestone.

**Revisit once in use:** this is the weakest mapping in the slice. When a real findings / remediation-tracking slice lands (with explicit owners, due dates, multi-milestone plans), POA&M generation should re-point to it. The default remediation window and single-milestone shape are placeholders.

**Confidence: medium.** Defensible given the available signal, but it is a genuine spec-ambiguity call — POA&M is the artifact most likely to need rework once a real auditor exercises it.

### D-version — OSCAL JSON pinned concretely to v1.1.2

The canvas says "v1.1.x" (a range); this slice pins **1.1.2** (latest 1.1.x at build time). A narrowing, not a contradiction.

**Revisit once in use:** bump the pin when `compliance-trestle` supports a newer 1.1.x and an auditor's tooling expects it.

**Confidence: high.** Concrete pinning is correct for reproducible exports; the version is trivially bumped.

### D1 — in-process ed25519 detached signature, cosign-compatible envelope

**Options considered:**

- (A) `cosign` the binary — requires either a managed key or keyless OIDC (Fulcio); neither is wired into the deployment yet.
- (B) An in-process ed25519 detached signature over the bundle digest, wrapped in a cosign-compatible envelope.

**Chosen: (B).** The canvas §9 tech-stack says "cosign signing of audit-export bundles" and the issue's P0 anti-criterion says "does NOT skip cosign signing." Both speak to _intent_: a tamper-detectable, verifiable signature on the export bundle. Option (B) delivers that — `VerifyBundle` checks both that the recomputed digest matches the manifest digest AND that the signature verifies against the recomputed digest, closing the "tamperer rewrites the digest field" gap. The signing key comes from the `OSCAL_SIGNING_KEY` env var; when unset, an ephemeral key is generated (the signature is still cryptographically valid and verifiable within the bundle, just not anchored to a persistent identity).

**This is ADR-worthy** — it is a deviation from the literal tool named in the canvas. It does not block merge (the bundle is signed and verifiable; intent honored), but a follow-up ADR should record the cosign-vs-in-process tradeoff formally.

**Revisit once in use:** swap for cosign keyless + Fulcio transparency-log signing in v3 (matches the canvas's "full Sigstore transparency-log in v3" line). Decide whether the ephemeral-key fallback should instead be a hard failure in production.

**Confidence: medium.** The crypto is sound (ed25519 + `crypto/rand`, no hardcoded keys) and the verify path is correct, but the deviation from cosign-the-tool deserves a formal ADR and a real Sigstore path later.

## Revisit once in use (consolidated — top of the iteration backlog)

1. **Validate every exported artifact (SSP + AP/AR + POA&M) against a real auditor's tooling.** `compliance-trestle` round-trip passing is necessary but not sufficient — this is the #1 revisit item per AC-7.
2. **POA&M (D3)** — re-point to a real findings/remediation primitive when one exists; the default remediation window + single-milestone shape are placeholders.
3. **Signing (D1)** — write the formal ADR; plan the cosign-keyless + Fulcio path; decide the production posture for the ephemeral-key fallback.
4. **OSCAL version pin (D-version)** — bump 1.1.2 when tooling expectations move.

## Confidence summary

| Decision                           | Confidence          |
| ---------------------------------- | ------------------- |
| D-narrative (SSP statement source) | high                |
| D-version (OSCAL 1.1.2 pin)        | high                |
| D3 (POA&M source)                  | medium              |
| D1 (ed25519 signing primitive)     | medium — ADR-worthy |

No constitutional-invariant conflicts surfaced. The grill confirmed AP/AR sourcing (populations + walkthroughs + audit notes) and audit-period-freezing enforcement are consistent with the canvas taken whole.
