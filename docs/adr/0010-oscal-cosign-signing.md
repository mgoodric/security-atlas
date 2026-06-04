# ADR 0010 — OSCAL export-bundle signing: cosign/Sigstore vs status-quo ed25519

**Status:** Proposed — **ADOPT-DEFERRED** (recommendation pending maintainer sign-off; this ADR is the slice-400 decision gate for slice 368).

**Date:** 2026-06-03

**Decision-only spike.** This ADR ships no production code (slice 400 P0-400-1). It is the maintainer's go/no-go gate for slice 368 (the ~5d cosign-migration build). The recommendation, its confidence, and the single decision the maintainer must make are stated in [§ Decision](#decision).

**Resolves:** slice 327 security-audit finding **M-3** (`docs/audits/327-security-audit-security-auditor-report.md`) — `internal/oscal/sign.go` signs OSCAL export bundles with in-process ed25519 detached signatures rather than the canvas §9-named cosign flow. Routes through slice 368 (`docs/issues/368-cosign-signing-migration.md`).

**Slot note:** ADR slots 0001–0009 are occupied (0003 is a pre-existing double-occupancy: `0003-audit-period-freeze-hash-inputs.md` + `0003-oauth-authorization-server.md`; this ADR does not touch that collision). Next free sequential slot is **0010**.

---

## Scope precision (read this first)

There are **two distinct artifacts** that get signed in this project. Conflating them is the most common error this ADR exists to prevent.

| Artifact                                                  | Signed where                                                                                                                                | Signed by what identity                                                        | Already cosign?                                                                                                                                |
| --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| **Release binaries + container images + `checksums.txt`** | In **CI** at release time (`.goreleaser.yaml signs:` block, `.github/workflows/release.yml`, `container-publish.yml`)                       | **GitHub Actions OIDC** token → Fulcio short-lived cert                        | **YES** — slice 084 migrated this to cosign v3 Sigstore protobuf-bundle (`*.sigstore.json`) keyless via `token.actions.githubusercontent.com`. |
| **OSCAL audit-export bundles**                            | At **runtime**, inside a **deployed atlas instance**, when an operator/auditor exports an SSP/AP/AR/POA&M bundle (`internal/oscal/sign.go`) | In-process **ed25519** ephemeral or `OSCAL_SIGNING_KEY` keypair (slice 030 D1) | **NO** — this is the subject of this ADR.                                                                                                      |

**This ADR is about the second row only.** The release-signing keyless flow works precisely because the signer is GitHub Actions, an identity Fulcio's public root _already federates_. A deployed atlas instance is a different identity entirely, and that difference is the load-bearing cost the rest of this document develops.

---

## Context

### The status quo, concretely

`internal/oscal/sign.go` produces a detached ed25519 signature over a deterministic bundle digest. The digest is the sha256 over a sorted `filename:memberhash` concatenation, so it changes if any member's bytes change. `VerifyBundle` re-derives the digest and checks both that the manifest digest matches the recomputed digest **and** that the signature verifies against it — closing the "tamperer rewrites the digest field" gap. The signing key comes from `OSCAL_SIGNING_KEY`; when unset, an ephemeral keypair is generated (the public key travels in the manifest, so the signature is self-consistent but not anchored to a long-lived identity).

The crypto is sound. `cosign sign-blob`'s underlying primitive is _also_ a detached signature over a content digest — the cryptographic shape is equivalent. What the status quo lacks is **Sigstore ecosystem integration**: a Fulcio-issued identity binding ("who signed this?"), a Rekor transparency-log entry ("when, provably?"), and stock-tooling verifiability.

### The auditor friction the status quo creates — name it precisely

The v1 binary success criterion is "survive third-party security review / diligence the diligence tool." An auditor or a customer's security team verifying an atlas OSCAL export today must:

1. **Trust an ad-hoc embedded public key with no external anchor.** The manifest carries a raw hex ed25519 public key. There is no certificate, no issuer, no chain back to a known identity. The verifier's only assurance that _this_ key is _atlas's_ key is out-of-band ("the operator told me so"). For a tool whose pitch is auditor-friendliness, "trust me, that's our key" is exactly the friction we are selling against.

2. **Write or run a bespoke verifier.** `cosign verify-blob` cannot validate the bundle without bespoke handling of the ad-hoc key. There is no `atlas verify` story an external party can run without first understanding our custom envelope. Compare the release-binary flow, where any third party runs a documented stock `cosign verify-blob …` line (see `.goreleaser.yaml` release-notes template).

3. **Have no independent timestamp / non-repudiation record.** With no Rekor entry, "when was this signed, and can the signer deny it later?" has no transparency-log answer. For an _audit-evidence_ artifact, that is the exact property a reviewer is trained to ask about.

This is genuine friction, and it is the right thing to want to close. The question this ADR answers is not _whether_ the cosign end-state is better — it plainly is — but _which modes are reachable at acceptable cost, for which deployment shapes, and on what timeline._

### Constitutional commitments in play

- **Canvas §9 / CLAUDE.md "Evidence integrity":** "sha256 content-hash per record (v1) + **cosign signing of audit-export bundles** … Full Sigstore transparency-log in v3." The cosign commitment is named; the transparency-log is explicitly a v3 horizon.
- **Slice 030 decisions log §D1** documented the in-process ed25519 choice as a deliberate trade-off (avoid a fragile external-binary dependency on every export and in CI) and flagged "swap for cosign keyless + Fulcio transparency log" as a **v3 revisit** at **medium confidence**.
- **Invariant #2 (separated ingest/eval + append-only ledger)** and **invariant #8 (OSCAL is the wire format)** are untouched by any signing-mode choice — this ADR changes how the _export_ is signed, not the export format or the evidence record.

---

## The three modes (slice 368's proposal)

Slice 368 proposes a `Mode` discriminator on `oscal.Signature` with three values. Below is what each _actually requires at runtime_ and how each _fails_.

### Mode A — `embedded-ed25519` (status quo, retained)

| Aspect              | Detail                                                                           |
| ------------------- | -------------------------------------------------------------------------------- |
| Runtime requirement | None beyond the Go binary. Key from `OSCAL_SIGNING_KEY` or ephemeral.            |
| Network dependency  | **None.** Hermetic.                                                              |
| Identity anchor     | None (raw public key in manifest).                                               |
| Verifier story      | Bespoke / `atlas oscal verify`. **Not** stock `cosign verify-blob`.              |
| Failure modes       | Malformed key only. Cannot fail on network/identity grounds because it has none. |
| Air-gap fit         | **Perfect.** The only mode an air-gapped deployment can use.                     |

### Mode B — `cosign-kms` (cosign binary + a cloud KMS-backed key)

| Aspect              | Detail                                                                                                                                                            |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime requirement | `cosign` binary on PATH/bundled; a KMS reference (`ATLAS_COSIGN_KMS_REF` → AWS KMS / GCP KMS / Azure Key Vault); cloud credentials to use that key.               |
| Network dependency  | The KMS endpoint. **No Fulcio, no OIDC-identity dance.** Optionally Rekor if the operator opts into transparency-log upload, but KMS signing does not require it. |
| Identity anchor     | The KMS key + its IAM policy. "Who can sign" = "who holds the KMS use-permission." Strong, operator-controlled, no public federation needed.                      |
| Verifier story      | Stock `cosign verify-blob --key <kms-ref-or-exported-pubkey>`. The auditor uses a stock tool against a key the operator publishes.                                |
| Failure modes       | cosign binary missing; KMS unreachable / IAM denied; clock skew on cred signing. Degrades to Mode A if `ATLAS_OSCAL_ALLOW_EMBEDDED=true`.                         |
| Air-gap fit         | Poor-to-none (KMS is a network service), but **does not need the public Sigstore infrastructure** — viable for a connected self-host with its own cloud KMS.      |

### Mode C — `cosign-keyless` (Fulcio + Rekor + an OIDC machine identity)

| Aspect              | Detail                                                                                                                                                                                                                              |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime requirement | `cosign` binary; **a live OIDC identity** acceptable to Fulcio's federation set; network reach to **Fulcio** (cert issuance) **and Rekor** (transparency log).                                                                      |
| Network dependency  | Fulcio + Rekor + the OIDC issuer — three live external dependencies on the export hot path.                                                                                                                                         |
| Identity anchor     | A Fulcio-issued short-lived cert bound to the OIDC subject. This is the strongest anchor — and the reason this mode is the headline value.                                                                                          |
| Verifier story      | Stock `cosign verify-blob --certificate-identity … --certificate-oidc-issuer …` with the Rekor inclusion proof. Exactly the release-binary flow, now for export bundles.                                                            |
| Failure modes       | **Fulcio outage** → cannot mint cert → export blocked unless fallback configured. **Rekor outage** → cannot log → same. **No OIDC identity** → cannot even attempt (see the blocker below). **Air-gap** → categorically impossible. |
| Air-gap fit         | **None.** This mode cannot run without internet + a federated OIDC issuer.                                                                                                                                                          |

### The OIDC-identity blocker (the load-bearing finding)

Slice 368's notes suggest atlas's own AS (slice 188's `client_credentials` machine identity, `oauth_client:oscal-signer`) could mint the Fulcio cert. **This does not work against public Fulcio as written**, and the reason is structural, not a config detail:

- Slice 188's AS mints JWTs with `aud = <atlas-instance-issuer>` and `atlas:idp_issuer = "atlas-oauth-client"` — a **per-deployment, non-public issuer**. Each self-hosted atlas has its _own_ issuer URL.
- **Public Fulcio only issues certs to OIDC identities from its federated trust root** (Google, GitHub Actions, GitLab CI, Microsoft, Kubernetes SA, SPIFFE, Buildkite, and a small curated set). A self-hosted atlas instance's bespoke per-deployment issuer is **not** in that set and cannot be added by the operator — adding an issuer to the public Sigstore root is a **Sigstore governance/onboarding process**, not a flag atlas can set.

Therefore `cosign-keyless` against **public** Fulcio/Rekor is reachable only via:

1. **A public IdP as the signing identity** (e.g., a Google/GitHub service account atlas authenticates as). This works but (a) drags a _new external IdP dependency_ into the signing path for self-hosts that may not want a Google/GitHub coupling, and (b) makes "who signed?" resolve to a generic cloud SA, weakening the "this is _atlas's_ identity" story unless carefully scoped per tenant/deployment.
2. **A private Sigstore stack** (operator runs their own Fulcio + Rekor + a trust root they control, federating atlas's AS). This is the cleanest _identity_ answer and the most operationally heavy — it is standing up the entire Sigstore control plane per deployment. Appropriate for a sophisticated connected operator; absurd for the v1 solo-leader persona.

This is the single biggest reason `cosign-keyless` is not a near-term default for the project's primary deployment shapes.

---

## Cost ledger

| Cost                     | Mode A | Mode B (kms)                | Mode C (keyless)                       | Notes                                                                                                                                |
| ------------------------ | ------ | --------------------------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| External `cosign` binary | —      | required                    | required                               | Bundle in container (one Dockerfile line, slice 368's recommendation) vs operator-install. **License: clean to bundle (see below).** |
| Fulcio as a live dep     | —      | —                           | **yes**                                | Outage blocks export unless Mode-A fallback configured.                                                                              |
| Rekor as a live dep      | —      | optional                    | **yes**                                | The transparency-log value _is_ the dependency.                                                                                      |
| OIDC machine identity    | —      | —                           | **yes — and blocked as designed**      | See the OIDC-identity blocker. The hardest part of Mode C.                                                                           |
| Cloud KMS + IAM          | —      | **yes**                     | —                                      | Operator must run a cloud KMS and grant atlas use-permission.                                                                        |
| CI needs cosign binary   | —      | yes (for integration tests) | yes (for integration tests)            | CI already installs cosign for release signing; reuse is cheap. The _new_ cost is cosign on the **runtime** atlas image, not CI.     |
| Build effort             | 0      | ~part of 5d                 | ~bulk of 5d                            | Mode B is the simplest (no Fulcio); Mode C's Fulcio/Rekor/identity wiring is most of the 5d.                                         |
| Air-gap regression risk  | none   | n/a                         | **must not become default** (P0-368-1) | Air-gapped self-host is a major user shape and can use only Mode A.                                                                  |

### cosign license — bundling check (P0-368-4 / CLAUDE.md licensing section)

**Finding: bundling the cosign binary is license-clean.**

- **cosign is Apache License 2.0** (sigstore/cosign, `LICENSE` at repo root). Apache-2.0 is a permissive license that **explicitly permits redistribution in binary form**, including bundling into a container image, provided the license text and any `NOTICE` attribution are preserved in the distribution.
- This is **fully compatible** with both candidate project licenses under open-decision (Apache-2.0 — trivially; AGPL-3.0 — Apache-2.0 is one-way compatible _into_ (A)GPL-3.0 work, the standard direction). No copyleft conflict either way.
- It is **categorically different** from the CC-BY-NC-SA OpenGRC constraint in CLAUDE.md's licensing section: that one forbids _copying code_; this is redistributing an _unmodified upstream binary under a permissive license_ — the same thing we already do implicitly by depending on cosign in CI.
- **Mechanical obligation when slice 368 lands:** the bundling Dockerfile step must (1) pin a known-good cosign version (currently 3.x stable; release signing already uses v3.0.6+), (2) include cosign's `LICENSE` + `NOTICE` in the image (e.g. under `/usr/share/licenses/cosign/`), and (3) record the version + provenance in `docs/governance/asset-inventory.md` §2.3 (which already names "cosign signing key planned" / slice 368 as a tracked asset).

**No legal blocker to bundling.** This finding stands on Apache-2.0's plain terms; the SCF-redistribution legal-review caveat in CLAUDE.md is a _different artifact_ (the SCF catalog) and does not bear on the cosign binary.

---

## Options evaluated

Scored against (a) value to the v1 binary criterion, (b) reachability for the **primary persona** (solo leader, often air-gap-capable self-host), (c) build/operational cost, (d) fit with the canvas §9 commitment + invariants.

### Option 1 — DON'T-ADOPT (keep ed25519 permanently; revise canvas §9)

Document the ed25519 path as the permanent answer; downgrade canvas §9's cosign line to "cosign-equivalent in-process signature."

- (a) value: **Low.** Leaves the named auditor friction (ad-hoc key, no stock verifier, no transparency log) unaddressed. Directly under-delivers on a canvas §9 commitment and on M-3.
- (b) reachability: **High** (already shipped).
- (c) cost: **Zero.**
- (d) fit: **Poor** — actively revises a constitutional commitment downward. Should only be chosen if the cosign value is judged illusory, which the auditor-friction analysis says it is not.

**Rejected.** The friction is real and the canvas commitment is deliberate.

### Option 2 — ADOPT ALL THREE MODES NOW (full slice 368 as written, keyless default for SaaS)

- (a) value: **High** for connected SaaS; the keyless path is the headline.
- (b) reachability: **Low for the primary persona.** Mode C's OIDC-identity blocker is unresolved; shipping a "keyless default" that silently requires a public-IdP coupling or a private Sigstore stack ships a default most self-hosts cannot actually use. Air-gap must fall back to Mode A regardless.
- (c) cost: **High** — the full 5d, most of it Mode C's Fulcio/Rekor/identity wiring, _before_ the identity question is settled.
- (d) fit: **Mixed** — honors §9 maximally but commits build effort to the least-reachable mode first.

**Rejected as the immediate plan** — it front-loads the hardest, least-reachable mode and ships a default the primary persona cannot use. (It is the _eventual_ destination; see Option 3.)

### Option 3 — ADOPT-DEFERRED: commit the direction, build the reachable subset first, gate keyless on the identity question

Commit to the cosign end-state. Sequence the build so the **reachable** value lands first and the **blocked** mode is gated on resolving its blocker:

- **Phase 1 (build soon):** the `internal/oscal/cosign` wrapper + **Mode B `cosign-kms`** + retain **Mode A** + mode discriminator + dispatch + backward-compat. This delivers stock-`cosign verify-blob` verifiability _immediately_ for any operator with a cloud KMS, with **no Fulcio/Rekor/OIDC-identity dependency**. The headline auditor-friction items (#1 ad-hoc key, #2 bespoke verifier) are _substantially_ closed by Mode B alone.
- **Phase 2 (gated):** **Mode C `cosign-keyless`** + Fulcio + Rekor + transparency-log handling, **gated on a prior decision** resolving the OIDC-identity blocker (public-IdP-coupling vs private-Sigstore vs Sigstore-onboarding). This is the "Full Sigstore transparency-log in v3" line in canvas §9 — and it lands when its identity prerequisite is real, not before.

- (a) value: **High and front-loaded** — the reachable value (Mode B) ships first; the transparency-log value (Mode C) lands when reachable.
- (b) reachability: **High** — Mode A serves air-gap, Mode B serves connected self-host + SaaS-with-KMS, Mode C is explicitly deferred until its blocker clears.
- (c) cost: **Controlled** — Phase 1 is the cheaper, lower-risk portion of the 5d; Phase 2's heavy Fulcio/identity work is not spent until the identity decision de-risks it.
- (d) fit: **Strong** — honors canvas §9 ("cosign signing of audit-export bundles" = Mode B satisfies the literal commitment with stock tooling; "Full Sigstore transparency-log in v3" = Mode C, correctly horizoned).

**Recommended.**

---

## Decision

**Recommendation: ADOPT-DEFERRED. Confidence: HIGH.**

Adopt cosign as the committed direction for OSCAL export-bundle signing, and **re-scope slice 368 into a phased, mode-sequenced build** (Option 3):

- **Build Phase 1 soon** (`internal/oscal/cosign` wrapper + `cosign-kms` + retained `embedded-ed25519` + dispatch + backward-compat + CLI `config-check`). This closes the bulk of the named auditor friction with stock `cosign verify-blob`, **without** taking on Fulcio/Rekor/OIDC as live runtime dependencies.
- **Defer Phase 2** (`cosign-keyless` + Fulcio + Rekor + transparency log) and **gate it on a prior, separately-decided resolution of the OIDC-identity blocker.** This is canvas §9's "Full Sigstore transparency-log in v3" line, correctly horizoned.

### Recommended default mode per deployment shape

| Deployment shape                         | Recommended default                                                                | Why                                                                                                                                                   |
| ---------------------------------------- | ---------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Self-host, air-gapped**                | **`embedded-ed25519`** (Mode A)                                                    | The only mode reachable with no network/identity. Non-negotiable (slice 368 P0-368-1).                                                                |
| **Self-host, connected, with cloud KMS** | **`cosign-kms`** (Mode B)                                                          | Stock-verifiable, operator-controlled identity, no Sigstore public-infra dependency.                                                                  |
| **Self-host, connected, no KMS**         | **`embedded-ed25519`** until operator opts in                                      | No KMS → Mode B unavailable; Mode C blocked on identity. Don't force a cloud coupling.                                                                |
| **SaaS (Helm)**                          | **`cosign-kms`** at GA; **`cosign-keyless`** once Phase 2 + identity decision land | The connected platform _should_ be the strongest; but ship the reachable strong mode (KMS) first, not a keyless default that can't mint a cert today. |

> Note: this **revises slice 368's AC-9** (which set the Helm/SaaS default to `cosign-keyless`). Per this ADR, the SaaS default at GA is `cosign-kms`; `cosign-keyless` becomes the SaaS default only after Phase 2 and the OIDC-identity decision. The slice 368 re-scope (below) records this.

### The single decision the maintainer must make

> **Approve ADOPT-DEFERRED + the phased re-scope of slice 368 (Phase 1 = kms + embedded now; Phase 2 = keyless gated on the OIDC-identity decision)? And confirm the per-deployment defaults above — specifically the revision of slice 368 AC-9's SaaS default from `cosign-keyless` to `cosign-kms`-at-GA.**

A secondary, _separable_ decision the maintainer may defer further: **which OIDC-identity strategy unblocks Phase 2** — (i) public IdP coupling, (ii) private Sigstore stack, or (iii) pursue Sigstore-root onboarding for atlas's AS. This ADR does **not** pick one; it documents that Phase 2 cannot start until one is chosen. Recommend resolving it in its own short spike when Phase 2 approaches.

### Confidence rationale

**High**, because the load-bearing facts are verified, not assumed:

- The cosign Apache-2.0 license is confirmed clean to bundle (plain license terms, no copyleft conflict either direction).
- The OIDC-identity blocker is grounded in slice 188's actual claim shape (`aud = <atlas-instance-issuer>`, non-public per-deployment issuer) vs Fulcio's actual federated-root model — not a guess.
- The air-gap constraint is a hard physical fact, not a preference: Mode C cannot run without internet + a federated issuer.
- Mode B delivers most of the _named_ auditor-friction value with the _least_ new operational surface, which makes "build the reachable value first" a low-regret sequencing call.

The residual uncertainty is entirely in Phase 2's identity strategy — which is exactly why it is deferred and gated rather than chosen here.

---

## Consequences

**Positive:**

- The canvas §9 cosign commitment is honored on a realistic path: Mode B satisfies "cosign signing of audit-export bundles" with stock tooling soon; Mode C delivers "Full Sigstore transparency-log in v3" when reachable.
- The named auditor friction (#1 ad-hoc key, #2 bespoke verifier) is substantially closed by Phase 1 alone — an external party runs a stock `cosign verify-blob` line against an operator-published KMS public key.
- The air-gap deployment shape is explicitly protected (Mode A retained; never the thing that breaks).
- The hardest, least-reachable work (Fulcio/Rekor/identity) is not spent until its prerequisite de-risks it.

**Negative / accepted trade-offs:**

- The transparency-log / non-repudiation value (auditor-friction item #3) is **not** delivered in Phase 1 — that arrives only with Mode C. Accepted: it is the smaller of the three friction items and the canvas already horizons it to v3.
- A SaaS operator wanting keyless today is told "KMS now, keyless after the identity decision." Accepted: shipping a keyless default that can't mint a cert would be worse.
- Carrying three modes is more surface than one. Mitigated: Mode A already exists; Mode B is the only genuinely-new runtime path in Phase 1; dispatch is additive (P0-368-2 backward-compat).

---

## Slice 368 re-scope (applied — AC-3 / AC-4)

Per this ADR, slice 368's single 5d "build all three modes, keyless-default-for-SaaS" scope is **re-scoped into two phased slices**, recorded in `docs/issues/368-cosign-signing-migration.md` and gated on maintainer approval of this ADR. See that doc's "Re-scoped by ADR-0010" section for the day-by-day → sub-slice mapping. Summary:

- **368a (Phase 1, ready on ADR approval, ~3d):** cosign wrapper + `cosign-kms` + retained `embedded-ed25519` + dispatch + manifest mode + backward-compat + CLI `sign|verify|config-check` + runbook + integration tests for Mode A & B. Default: air-gap → embedded; connected-with-KMS → kms.
- **368b (Phase 2, not-ready — gated on the OIDC-identity decision, ~2d):** `cosign-keyless` + Fulcio + Rekor + transparency-log dispatch + keyless integration tests + SaaS keyless default flip. **Blocked-by:** a separate identity-strategy decision (public IdP vs private Sigstore vs Sigstore-onboarding).

---

## Cross-references

- **Slice 327 M-3** (`docs/audits/327-security-audit-security-auditor-report.md`) — the finding this resolves.
- **Slice 368** (`docs/issues/368-cosign-signing-migration.md`) — the parent build, re-scoped here.
- **Slice 030 D1** (`docs/audit-log/030-oscal-ssp-poam-export-decisions.md`) — the in-process ed25519 decision + its v3 cosign revisit flag.
- **Slice 084** (`.goreleaser.yaml signs:`, `.github/workflows/release.yml`, `container-publish.yml`) — cosign v3 keyless for **releases/containers** (the _other_ artifact; the GitHub-Actions-OIDC precedent that does **not** transfer to runtime export signing).
- **Slice 188** (`docs/issues/188-oauth-token-endpoint-token-exchange.md`, `internal/api/oauth/token.go`) — the `client_credentials` machine identity whose non-public issuer is the OIDC-identity blocker for Mode C.
- **Canvas §9** (`Plans/canvas/09-tech-stack.md`) + **CLAUDE.md "Evidence integrity"** — the cosign commitment + v3 transparency-log horizon.
- **`docs/governance/asset-inventory.md` §2.3** — where the bundled cosign binary version + provenance must be recorded when 368a lands.
