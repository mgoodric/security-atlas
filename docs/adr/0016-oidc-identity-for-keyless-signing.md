# ADR 0016 — OIDC identity for cosign-keyless OSCAL-bundle signing

**Status:** Proposed — **ADOPT-DEFERRED** (recommendation pending maintainer sign-off; this ADR is the slice-455 decision gate that unblocks slice 414 / 368b).

**Date:** 2026-06-11

**Slice:** 455 (`docs/455-oidc-identity-strategy-spike`).

**Decision-only spike.** This ADR ships no production code (slice 455 P0-455-1). It resolves the **secondary decision** that [ADR-0010](0010-oscal-cosign-signing.md) § Decision explicitly deferred — _which OIDC-identity strategy unblocks the keyless leg (368b / slice 414)_ — and flips slice 414's status accordingly. The recommendation, its confidence, and the single decision the maintainer must make are stated in [§ Decision](#decision).

**Resolves:** the gating BLOCKER on slice 414 (`docs/issues/414-cosign-keyless-oscal-signing-phase-2.md`) and the open "which OIDC-identity strategy" line in [ADR-0010 § Decision](0010-oscal-cosign-signing.md) and `docs/issues/368-cosign-signing-migration.md` § 368b.

**Slot note:** the next free sequential ADR slot is **0016**. Slots 0001–0015 are occupied. (0003 is a pre-existing double-occupancy: `0003-audit-period-freeze-hash-inputs.md` + `0003-oauth-authorization-server.md`; this ADR does not touch that collision, per slice 455 AC-1.)

---

## Scope precision (read this first)

This ADR inherits — and does not relitigate — [ADR-0010 § Scope precision](0010-oscal-cosign-signing.md). Two distinct artifacts get signed in this project, by two distinct identities. **Conflating them is the error this section exists to prevent.**

| Artifact                                                  | Signed where                                     | Signed by what identity                                                        | Keyless today?                                                              | This ADR's surface? |
| --------------------------------------------------------- | ------------------------------------------------ | ------------------------------------------------------------------------------ | --------------------------------------------------------------------------- | ------------------- |
| **Release binaries + container images + `checksums.txt`** | In **CI** at release time                        | **GitHub Actions OIDC** (`token.actions.githubusercontent.com`)                | **YES** — slice 084 (cosign v3 keyless); slice 451 extends it to SLSA/SBOM. | **NO**              |
| **OSCAL audit-export bundles**                            | At **runtime**, inside a deployed atlas instance | The deployed instance's own identity (`embedded-ed25519` / `cosign-kms` today) | **NO** — the subject of ADR-0010 and of this ADR's keyless leg.             | **YES**             |

**This ADR is about the second row only.** Option (a) below (GitHub-Actions-OIDC) is the _first_ row's solution — slice 451's flow — and it does **not** transfer to the runtime surface, for the structural reason ADR-0010 already developed: the runtime signer is a deployed atlas instance, an identity public Fulcio does not federate. Naming that boundary precisely is slice 455 AC-6 / P0-455-5.

---

## Context

### What is actually blocked

ADR-0010 adopted cosign as the committed direction and re-scoped the build into two phases:

- **Phase 1 (368a / slice 413, in-progress):** `cosign-kms` + retained `embedded-ed25519` + the mode discriminator + dispatch. This closes the bulk of the named auditor friction (ad-hoc key, no stock verifier) with stock `cosign verify-blob` and **no Fulcio/Rekor/OIDC dependency.**
- **Phase 2 (368b / slice 414, `not-ready`):** `cosign-keyless` + Fulcio + Rekor + the transparency-log value — gated on a single unanswered question.

That question is the **OIDC-identity blocker** (ADR-0010 § "The OIDC-identity blocker"). Restated with the verified claim shape from [ADR-0003 (OAuth AS)](0003-oauth-authorization-server.md):

- Slice 188's AS mints JWTs whose machine subject is `sub = "client:<client-id>"`, audience `aud = ["https://<atlas-instance>/api"]`, and `atlas:idp_issuer` set per deployment. The **issuer (`iss`) is per-deployment and non-public** — each self-hosted atlas has its own issuer URL.
- **Public Fulcio issues certs only to OIDC identities in its federated trust root** (Google, GitHub Actions, GitLab CI, Microsoft, Kubernetes SA, SPIFFE, Buildkite, and a small curated set). A self-hosted atlas's bespoke per-deployment issuer is **not** in that set, and an operator **cannot** add it — onboarding an issuer to the public Sigstore root is a Sigstore governance process, not a flag.

So `cosign-keyless` cannot mint a Fulcio cert for "atlas's identity" until we decide _whose_ OIDC identity backs the signature and _which_ Sigstore root vouches for it. That is the decision this spike records.

### The constraints that bound the choice

The decision is not made in a vacuum. Four project commitments rule out the easy answers:

1. **Self-hostable OSS, no data leaves deployment (CLAUDE.md / canvas §1).** An option that forces every self-host to phone a third-party IdP or a public transparency log on the export hot path violates the local-first default.
2. **Air-gap is a first-class deployment shape (ADR-0010, slice 368 P0-368-1).** Any option that becomes the _default_ for air-gap is categorically wrong — Mode C cannot run without internet.
3. **Solo-leader persona (canvas §1).** Standing up Sigstore control-plane infrastructure is a plausible ask for a sophisticated connected operator and an absurd one for the v1 solo persona.
4. **The transparency-log is horizoned to v3 (canvas §9).** "Full Sigstore transparency-log in v3" — Phase 1 (`cosign-kms`) already satisfies the literal "cosign signing of audit-export bundles" commitment with stock tooling. The keyless leg is the v3 horizon, not a v1 must-have.

These constraints are the reason the recommendation lands where it does: the value of keyless is real, but its near-term reachability for the _primary_ deployment shapes is low, and the kms fallback already banks most of the auditor-friction win.

---

## The options

Each option below states, per slice 455 AC-2: **(1)** what it requires at runtime, **(2)** which identity Fulcio (or the chosen CA) certifies and who can obtain a token for it, **(3)** what becomes publicly visible in the cert/log, **(4)** its failure modes, and **(5)** its fit for self-host-solo vs SaaS.

### Option (a) — GitHub-Actions-OIDC, release artifacts only

| Aspect              | Detail                                                                                                                                                                 |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime requirement | None at deployment runtime. This identity exists only in CI (`token.actions.githubusercontent.com`), inside the GitHub Actions release job.                            |
| Identity certified  | The GitHub Actions workflow identity (repo + ref + workflow path). Whoever can trigger a release on `mgoodric/security-atlas` can obtain it. Already Fulcio-federated. |
| Publicly visible    | Repo, ref, workflow path in the Fulcio cert + a public Rekor entry — acceptable for a public OSS release.                                                              |
| Failure modes       | Bound to the release job; **does not exist inside a deployed instance at OSCAL-export time.**                                                                          |
| Self-host fit       | **N/A for the runtime surface.** This is slice 451's flow for the _release-artifact_ row, not the OSCAL-bundle row.                                                    |

**Verdict: out of scope for this ADR's surface.** It already works (slice 084 / 451) for release artifacts. It is named here only to fence it off (AC-6 / P0-455-5): it solves a _different_ artifact signed by a _different_ identity. It cannot sign a runtime OSCAL bundle.

### Option (b) — Per-deployment federated issuer (atlas's AS issuer federated into a Fulcio it trusts)

| Aspect              | Detail                                                                                                                                                                                                                                                          |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime requirement | `cosign`; atlas's AS issuing a dedicated `client:oscal-signer` machine token (slice 188 supports this); a **Fulcio whose trust root includes atlas's per-deployment issuer**; Rekor reach.                                                                      |
| Identity certified  | `sub = client:oscal-signer`, `iss = <this deployment's atlas issuer>`. Issuance is scoped to whoever holds the `oscal-signer` client's credentials — operator-controlled, auditable via the slice-188 `oauth_token_exchanges` / client-credentials audit trail. |
| Publicly visible    | The cert/log carries the **per-deployment issuer URL** + the `oscal-signer` subject. If that issuer URL or a tenant identifier is deployment-identifying, it leaks into a public log (information-disclosure threat below).                                     |
| Failure modes       | **Public Fulcio will not federate an arbitrary self-hosted issuer** (this is the blocker). So "federated" here means a Fulcio _the operator controls_ accepts the issuer — which collapses into Option (c). Against public Fulcio, (b) is not reachable.        |
| Self-host fit       | Identity story is **clean and auditable** (atlas's own scoped machine identity, exactly slice 368's original premise). Reachability depends entirely on _which_ Fulcio — and only a private one will federate atlas's issuer.                                   |

**Verdict: the right _identity_ shape, but it only materializes on top of Option (c).** (b)'s machine-identity model (scoped `oscal-signer`, operator-auditable issuance, not broadly mintable) is exactly what P0-455-3 wants. But "a Fulcio that trusts atlas's issuer" is, in practice, an operator-run Fulcio — so (b) is the _identity layer_ and (c) is the _infrastructure layer_ of the same answer.

### Option (c) — Private Sigstore stack (operator-run Fulcio + Rekor)

| Aspect              | Detail                                                                                                                                                                                                                                   |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime requirement | `cosign`; an operator-run **Fulcio + Rekor + a CA trust root the operator controls**, federating atlas's AS issuer; cosign configured with the private TUF root.                                                                         |
| Identity certified  | Same as (b): `client:oscal-signer` from atlas's own issuer. The operator's CA vouches for it. Issuance is fully operator-scoped and auditable.                                                                                           |
| Publicly visible    | **Nothing leaves the deployment.** The transparency log is the operator's own Rekor — which honors "no data leaves deployment" but **weakens external-auditor verifiability** (the auditor must trust a log only the operator can see).  |
| Failure modes       | Heaviest operational surface: the operator runs the entire Sigstore control plane (Fulcio, Rekor, CT log, TUF root maintenance). Fulcio/Rekor outage blocks export unless Mode-A/B fallback is configured.                               |
| Self-host fit       | **Coherent for a sophisticated connected operator; absurd for the v1 solo persona.** Air-gap: still impossible if the Sigstore stack is reachable only over a network, but at least it is the operator's own network (not the internet). |

**Verdict: the only option that yields full keyless semantics _and_ honors "no data leaves deployment."** It is the cleanest end-state for a sophisticated connected operator and the natural home for (b)'s identity model. Its cost is the operational weight, and its tension is that an operator-private Rekor weakens the **external-auditor** transparency value (Repudiation threat below) — the auditor sees a log they cannot independently corroborate. That trade-off is acceptable _because the kms mode (413) already gives the auditor a stock-`cosign verify-blob` story without any log at all_; the private Rekor is additive, not the sole assurance.

### Option (d) — Sigstore-root onboarding (add atlas's issuer to the public Sigstore root)

| Aspect              | Detail                                                                                                                                                                                                                   |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Runtime requirement | Same as keyless against public Fulcio — _if_ onboarding ever completes.                                                                                                                                                  |
| Identity certified  | atlas's per-deployment issuer, certified by **public** Fulcio. But a per-deployment issuer cannot be a single onboarded entry; each self-host has a different issuer URL — there is nothing stable to onboard.           |
| Publicly visible    | Per-deployment issuer + subject in the **public** Rekor — strongest external-auditor verifiability, but maximal information disclosure (every export, public, forever).                                                  |
| Failure modes       | Onboarding is a Sigstore **governance** process: slow, uncertain, and conceptually mismatched — the public root federates _classes_ of issuers (GitHub Actions, Google), not thousands of bespoke per-self-host issuers. |
| Self-host fit       | **None.** There is no single atlas issuer to onboard; the per-deployment model is structurally incompatible with a one-time public-root onboarding.                                                                      |

**Verdict: rejected — structurally mismatched and governance-uncertain.** The public root federates issuer _classes_, not per-deployment issuers. Pursuing it would be slow, uncertain, and would not even produce a usable result for the self-host shape. (P0-455-3 is also at risk: a broadly-onboarded "atlas issuer class" would make the signing identity broadly mintable across unrelated deployments.)

### Option (e) — DON'T-ADOPT-KEYLESS (keep `cosign-kms` + `embedded-ed25519`)

| Aspect              | Detail                                                                                                                                                                                         |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime requirement | Phase 1 only: `embedded-ed25519` (hermetic, air-gap) + `cosign-kms` (connected, operator KMS). **No Fulcio, no Rekor, no OIDC-identity dance.**                                                |
| Identity certified  | KMS mode: the operator's KMS key + IAM policy ("who can sign" = "who holds KMS use-permission"). Strong, operator-controlled, no public federation.                                            |
| Publicly visible    | Nothing — no public log at all.                                                                                                                                                                |
| Failure modes       | No transparency-log / non-repudiation record (auditor-friction item #3 from ADR-0010 stays open). That is the _smallest_ of the three friction items, and canvas §9 already horizons it to v3. |
| Self-host fit       | **Best for the primary persona.** Air-gap → embedded; connected-with-KMS → kms; both stock-`cosign verify-blob`-verifiable. No new operational surface, no internet coupling, no log to run.   |

**Verdict: the correct _near-term default_ for self-host-solo and air-gap.** ADR-0010 already established that Phase 1 closes the bulk of the named auditor friction. For the primary persona, (e) is not a cop-out — it is the low-regret answer that the constraints point to.

---

## The recommendation reconciles two surfaces

The options do not collapse to one winner because the deployment shapes have different reachability:

- For **self-host-solo and air-gap**, the answer is **(e)**: keyless is unreachable or operationally absurd; kms + embedded already bank the verifiability win.
- For a **sophisticated connected operator / SaaS** that genuinely wants the transparency-log, the buildable answer is **(b)-on-(c)**: a private Sigstore stack federating atlas's scoped `oscal-signer` issuer. This is the only option that yields full keyless semantics, keeps the identity auditable and not broadly mintable (P0-455-3), and honors "no data leaves deployment."

So the keyless leg (414 / 368b) **is** buildable — as an **opt-in mode for the connected/private-Sigstore shape**, never as the air-gap or solo default. That makes 414 `ready` with a tightened scope, not `wontfix`.

---

## Decision

**Recommendation: ADOPT-DEFERRED — build keyless as an opt-in `cosign-keyless` mode backed by Option (b)-on-(c) (atlas's scoped `oscal-signer` AS identity federated into an operator-run private Sigstore stack); keep Option (e) as the near-term default for every primary deployment shape. Confidence: HIGH.**

Concretely:

1. **Reject (a)** for this surface — it is slice 451's release-artifact flow, a different identity (AC-6 / P0-455-5).
2. **Reject (d)** — structurally mismatched (no stable per-deployment issuer to onboard) and governance-uncertain; also risks broad mintability (P0-455-3).
3. **Adopt (b)-on-(c)** as the keyless _direction_: atlas's AS issues a dedicated, scoped `client:oscal-signer` machine identity (slice 188 already supports this), federated into an **operator-run private Sigstore** (Fulcio + Rekor) whose trust root the operator controls. This is the only identity whose issuance is auditable and **not broadly mintable** (satisfies P0-455-3 / the Spoofing + Elevation axes below).
4. **Keep (e) the default** for self-host-solo, air-gap, and connected-without-private-Sigstore: `embedded-ed25519` (air-gap) / `cosign-kms` (connected-with-KMS). This is unchanged from ADR-0010's default table.

### Recommended default per deployment shape

| Deployment shape                                  | Recommended OSCAL-signing default                          | Keyless available?                                                        |
| ------------------------------------------------- | ---------------------------------------------------------- | ------------------------------------------------------------------------- |
| **Self-host, air-gapped**                         | `embedded-ed25519`                                         | **No** — categorically unreachable (no network).                          |
| **Self-host, connected, no private Sigstore**     | `cosign-kms` (or `embedded`)                               | **No** — no Fulcio that federates atlas's issuer; kms is the strong mode. |
| **Self-host / SaaS, connected, private Sigstore** | `cosign-kms` at GA; opt-in `cosign-keyless` once 414 lands | **Yes, opt-in** — Option (b)-on-(c).                                      |

This **preserves ADR-0010's default table** (it does not flip the SaaS GA default to keyless). Keyless is an _opt-in capability_ for operators who run their own Sigstore, never an imposed default.

### The single decision the maintainer must make

> **Approve recording Option (b)-on-(c) — opt-in `cosign-keyless` via atlas's scoped `oscal-signer` identity federated into an operator-run private Sigstore — as the keyless direction, with Option (e) (`cosign-kms` + `embedded-ed25519`) remaining the default for all primary deployment shapes? This flips slice 414 to `ready` with the scope tightened to "opt-in private-Sigstore keyless mode" (not a default flip), and confirms that public-Fulcio keyless (options a/d) is NOT pursued for the runtime OSCAL surface.**

### Confidence rationale

**High**, because the load-bearing facts are verified against the actual sources, not assumed:

- The per-deployment non-public issuer is the real slice-188 claim shape (`sub = client:<client-id>`, per-deployment `iss`), confirmed in ADR-0003. Public Fulcio's federated-root model is a documented fixed set. The (a)/(d) rejections follow structurally, not from preference.
- (b) and (c) are not two competing options but one layered answer — (b) is the identity, (c) is the infrastructure — which is why the recommendation names them together.
- The kms fallback (413) already banks the two largest auditor-friction items, so deferring the transparency-log leg to an opt-in mode is low-regret (canvas §9 already horizons it to v3).
- The scoped `oscal-signer` identity is auditable and not broadly mintable, which is the property P0-455-3 makes load-bearing.

The residual uncertainty is operational (how heavy a private-Sigstore bring-up is for the target operator), which is exactly why keyless stays **opt-in and deferred** rather than a default.

---

## Threat model (decision-level, per slice 455)

This is a decision artifact (no input/availability surface), so the STRIDE pass reasons about the _options' trust properties_, not an implementation.

- **S — Spoofing (central axis).** The recommended Option (b)-on-(c) certifies a single, scoped `client:oscal-signer` identity from atlas's own issuer, vouched for by an operator-controlled CA. Issuance is gated on the `oscal-signer` client credentials and logged via slice 188's audit trail — **not broadly mintable** (P0-455-3 satisfied). Options (a)/(d) are rejected partly _because_ they widen who can obtain a signing token (a class identity, or any release-triggerer).
- **R — Repudiation.** The recommendation accepts a **weaker external-auditor transparency** trade-off: a private Rekor (Option c) is a log the operator runs, not a publicly-corroborable one. Accepted because kms mode (413) already gives the auditor a stock-`cosign verify-blob` story; the private Rekor is additive non-repudiation, not the sole assurance. An operator wanting public-Rekor non-repudiation is told that is out of scope for the runtime surface (it conflicts with "no data leaves deployment").
- **I — Information disclosure.** Options (b)/(d) against a _public_ log would leak the per-deployment issuer URL + subject (potentially deployment- or tenant-identifying) into a permanent public record. The recommended private-Sigstore path (c) keeps cert/log contents inside the deployment, eliminating that leak.
- **E — Elevation of privilege.** A misconfigured federation could let one identity sign as another. The recommendation scopes issuance to a single dedicated `oscal-signer` client (not the tenant-switch / human-subject path) and references slice 188's machine-token model so a compromised general atlas machine token cannot mint audit-binding signatures by default.
- **T / D.** Not applicable to a decision-only artifact (noted for completeness per the mandatory pass).

---

## Consequences

**Positive:**

- Slice 414 / 368b is **unblocked as `ready`** with a concrete, buildable identity model — atlas's scoped `oscal-signer` identity (slice 188) federated into an operator-run private Sigstore — rather than the structurally-impossible public-Fulcio premise the original 368 doc carried.
- The "no data leaves deployment" default is honored: the recommended keyless path keeps cert/log inside the operator's control plane.
- The primary persona is protected: keyless is opt-in for operators who run Sigstore, never imposed on air-gap or solo self-host. ADR-0010's default table is preserved.
- The signing identity is auditable and narrowly scoped — the trust-root scoping that motivates keyless is intact (P0-455-3).

**Negative / accepted trade-offs:**

- **Keyless requires the operator to run a private Sigstore stack** — a heavy ask. Accepted: it is opt-in, and the kms mode covers the operator who will not run Sigstore.
- **Private-Rekor transparency is weaker for an external auditor** than a public log. Accepted: kms already delivers stock-tool verifiability; the private log is additive.
- **Public-Fulcio keyless for the runtime surface is explicitly NOT pursued** (options a/d rejected). An operator wanting their atlas exports in the _public_ Sigstore transparency log cannot have it for the runtime surface — that conflicts with the local-first default. (Release artifacts remain publicly keyless-signed via slice 451 — a separate surface.)

### Impact on slice 414 (do NOT edit 414 here beyond the status flip + BLOCKER line)

This decision **tightens 414's scope**: 414 should build `cosign-keyless` as an **opt-in mode for the private-Sigstore deployment shape**, using atlas's scoped `oscal-signer` identity (slice 188) against an **operator-run Fulcio/Rekor with an operator-controlled trust root** — _not_ against public Fulcio (which cannot federate atlas's issuer). 414's AC-4 (SaaS keyless-default flip) should be **revised to "opt-in keyless availability,"** NOT a default flip — ADR-0010's default table stands. Air-gap stays `embedded-ed25519` (414 P0-414-2, unchanged).

---

## Cross-references

- **[ADR-0010](0010-oscal-cosign-signing.md)** — the parent ADOPT-DEFERRED decision; this ADR resolves the "single decision … secondary decision" (the OIDC-identity strategy) it deferred. Inherits its § Scope precision and three-mode framework.
- **[ADR-0003 (OAuth AS)](0003-oauth-authorization-server.md)** — the per-deployment non-public issuer + `client:<client-id>` machine-subject claim shape that is the root cause of the keyless blocker; the `oscal-signer` identity rides on slice 188's `client_credentials` grant.
- **Slice 414** (`docs/issues/414-cosign-keyless-oscal-signing-phase-2.md`) — the keyless build this spike unblocks; status flipped to `ready` with the scope tightened per § Consequences.
- **Slice 400** (`docs/issues/400-oscal-cosign-spike-adr.md`) — the Phase-1 decision spike this mirrors in shape.
- **Slice 368** (`docs/issues/368-cosign-signing-migration.md`) § 368b — the parent build whose BLOCKER line this resolves.
- **Slice 413** (`docs/issues/413-cosign-kms-oscal-signing-phase-1.md`) — the `cosign-kms` Phase-1 fallback this weighs keyless against.
- **Slice 451** (`docs/issues/451-slsa-provenance-sbom-binary-cli-sdk-releases.md`) — the GitHub-Actions-OIDC keyless flow for the _release-artifact_ surface (Option a); kept distinct from the runtime OSCAL surface (P0-455-5).
- **Canvas §9** (`Plans/canvas/09-tech-stack.md`) + **CLAUDE.md "Evidence integrity"** — "Full Sigstore transparency-log in v3"; the keyless leg is correctly horizoned, the kms leg satisfies the literal v1 cosign commitment.
