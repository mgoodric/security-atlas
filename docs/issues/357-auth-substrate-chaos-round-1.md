# 357 — Chaos experiment execution: auth-substrate chaos round 1

**Cluster:** Resilience
**Estimate:** 2d (three bundled experiments)
**Type:** JUDGMENT
**Status:** `ready` — **deferred to v2+** (execution slice)

## Narrative

Executes three chaos experiments designed in slice 335:

- **Experiment 4** — OIDC IdP unavailable
- **Experiment 6** — Cosign signing key absent at audit-export time
- **Experiment 8** — OPA decision-engine timeout

The designs live at `docs/audits/335-chaos-experiment-design.md`
§§Experiment 4, 6, 8. This slice does NOT redesign — it executes the
three as a bundled round because all three test fail-closed-vs-fail-
open discipline on auth-substrate surfaces and share decision-tree
shape.

This slice is **deferred to v2+**. It was filed by slice 335 as a
spillover slot per AC-4. The bundle decision is captured in slice
335's decisions log Decision D3. **This slice carries the third-most-
critical experiment (Exp 8, OPA timeout) — see slice 335 Decision
D8.**

### Why v2+ and why bundled

Slice 335 was design-only. The bundle is per Decision D3 — all three
test fail-closed authz/auth behavior under dependency unavailability.
Sharing one decisions log keeps the "fail-closed posture" finding
coherent.

### High-risk flag for Experiment 8

Slice 335 §High-risk experiments flags Exp 8 as requiring an
**additional reviewer** under the JUDGMENT-slice discipline. The
slow-policy hot-reload primitive does not yet exist; introducing it
touches the auth-critical-path. The reviewer must explicitly sign off
on the slow-policy injection mechanism before injection runs.

### Hypotheses under test

(Pulled verbatim from slice 335 for executor convenience.)

- **Exp 4 (OIDC):** Existing JWTs continue to work for TTL remainder;
  new logins fail with user-friendly error; key-rotation cron
  unaffected.
- **Exp 6 (cosign):** Platform refuses export with clear error; no
  unsigned bundle exported; no crash; no key-path leak in response.
- **Exp 8 (OPA):** Authz fails closed (403) on timeout; no 500; no
  2xx; audit log captures the timeout event.

## Threat model

Execution slice; injects controlled failures into auth surfaces.
STRIDE pass:

- **S:** Auth surface is the test target. Mitigation: containerized
  IdP only — never targets a real IdP. AC-4 enforces.
- **T:** Cosign key chmod 000 — restorable; key never moved outside
  the container.
- **R:** Outcomes logged in
  `docs/audit-log/357-auth-substrate-chaos-round-1-decisions.md`.
- **I:** Same as slice 335.
- **D:** **Load-bearing.** These ARE the failure-injection events.
- **E:** Dev-level access; slow-policy hot-reload primitive in Exp 8
  is the touchy surface — requires additional reviewer.

## Acceptance criteria

### Experiment 4 (OIDC IdP unavailable)

- [ ] **AC-1.** Containerized IdP (Dex / Keycloak) used — NOT a real
      external IdP.
- [ ] **AC-2.** Pre-execution checklist from slice 335 §Experiment 4
      satisfied (active JWT minted before injection; `exp` claim
      recorded).
- [ ] **AC-3.** IdP container detached from network; existing JWT
      verified to still authenticate on protected endpoint.
- [ ] **AC-4.** New `/oauth/authorize` attempt returns 503 with
      structured error body — NOT a stack trace.
- [ ] **AC-5.** Key-rotation cron continues to run without errors.
- [ ] **AC-6.** Network restored; new logins resume within 30s.

### Experiment 6 (cosign key absent)

- [ ] **AC-7.** Cosign key backed up before injection.
- [ ] **AC-8.** `chmod 000` on the key (or `mv` aside); audit-export
      triggered.
- [ ] **AC-9.** Response is HTTP 500 with structured body; NO bundle
      artifact left in object storage; NO key path in response body.
- [ ] **AC-10.** Key restored; subsequent export succeeds.

### Experiment 8 (OPA timeout) — HIGH-RISK

- [ ] **AC-11.** Additional reviewer signed off on the slow-policy
      injection mechanism BEFORE injection runs.
- [ ] **AC-12.** Original Rego policy file content saved to a known
      path before hot-reload.
- [ ] **AC-13.** OPA deployment shape verified: embedded library
      vs sidecar. Injection adapted to shape.
- [ ] **AC-14.** Slow-policy injected; protected endpoint hit with
      valid JWT. Status code MUST be 403 — NOT 2xx (bypass) and NOT
      500 (unhandled).
- [ ] **AC-15.** Audit log shows `policy_eval_timeout` event with
      correlation_id, principal, requested resource.
- [ ] **AC-16.** Original policy hot-reloaded; behavior restored.

### Shared

- [ ] **AC-17.** Post-experiment report at
      `docs/audit-log/357-auth-substrate-chaos-round-1-decisions.md`
      with per-experiment outcome.
- [ ] **AC-18.** Cross-references slice 335 (design), slice 187+ (auth
      substrate), slice 068 (schema-registry shape is referenced by
      adjacent Exp 7 in slice 358 — note for executor).
- [ ] **AC-19.** If ANY experiment falsifies, file a security-finding
      slice immediately — a fail-closed posture failing is a
      security-product credibility issue.

## Anti-criteria

- **P0-1.** Does NOT target a real external IdP — only containerized.
- **P0-2.** Does NOT target atlas-edge or hosted.
- **P0-3.** Does NOT permanently alter authz policies — slow-policy is
  hot-reloaded back to original.
- **P0-4.** Does NOT introduce a chaos framework as a dependency.
- **P0-5.** Does NOT auto-merge.
- **P0-6.** Does NOT skip the additional-reviewer requirement for
  Exp 8.

## Dependencies

- **#335** (chaos experiment design) — `merged`. The design contract.
- **#187+** (OAuth AS / OIDC RP) — provides the auth substrate.
- **#003** (Evidence SDK) — indirect, via the cosign signing of
  evidence artifacts.

## Notes for the implementing agent

1. Read `docs/audits/335-chaos-experiment-design.md` §§Experiment 4,
   6, and 8 FIRST.
2. Recommended execution order: Exp 6 (cosign) first — narrowest
   blast radius. Then Exp 4 (OIDC). Then Exp 8 (OPA) last — the
   high-risk experiment.
3. The additional-reviewer requirement for Exp 8 is non-optional.
   Document who reviewed in the decisions log.
4. If Exp 8 returns 2xx during the slow-policy window, this is a
   **critical** authz bypass — STOP, restore the policy, file a
   security finding immediately. Do NOT continue the bundle.
