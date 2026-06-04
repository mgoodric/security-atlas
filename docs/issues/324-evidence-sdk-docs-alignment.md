# 324 — Evidence SDK docs alignment: push-only wire reality + connector-profile-as-metadata

**Cluster:** Docs / Quality
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

The 2026-05-27 architecture review (run via `voltagent-qa-sec:architect-reviewer` against `main` at commit `7f58715b`) found a load-bearing mismatch between the project's documented connector contract and the actual gRPC wire surface.

The documentation claims:

- **`CLAUDE.md` invariant #3** — "The Evidence SDK exposes one canonical inbound API (`IngestEvidence`) through two profiles: **connector** (pull/subscribe) and **pusher** (push). Both are first-class peers."
- **`Plans/EVIDENCE_SDK.md` §3** — documents a "Connector profile" gRPC contract with methods `Describe()`, `AuthMethods()`, `HealthCheck(creds)`, `ListEvidenceKinds()`, `Pull(kind, since, scope_filter)`, `Subscribe(kind, scope_filter)`, `VerifyProvenance(record)`. The section opens with: "Methods are exposed over a gRPC contract; the connector runs as a separate process."
- **`Plans/canvas/04-evidence-engine.md` §4.1** — carries the same Connector-profile methods table inline.

The wire reality (`proto/connectors/v1/connectors.proto` + `proto/evidence/v1/evidence.proto`):

- The connector-management service has exactly two RPCs: `Register` (connector self-announces) and `List`.
- The evidence-ingest service has exactly one RPC: `Push(record) → Receipt`.
- There is **no** `Pull` RPC. There is **no** `Subscribe` RPC. There is no `Describe` / `AuthMethods` / `HealthCheck` / `ListEvidenceKinds` / `VerifyProvenance` RPC.
- Every shipped connector binary (`atlas-aws`, `atlas-github`, `atlas-okta`, `atlas-1password`, `atlas-osquery`, `atlas-jira`, `atlas-manual`) is a long-running process that holds source-side credentials, pulls or receives webhooks from the source, and calls `sdkClient.Push(ctx, record)` against the platform. The wire transport is unconditionally push.
- The `profiles_supported []string` field on `RegisterRequest` (`["pull"]`, `["push"]`, `["pull", "push"]`, etc.) describes the **source-side fetch direction** the connector uses, not a platform-side wire-format dimension.

The architectural call is not in question. The reviewer explicitly identified push-only-on-the-wire as one of the architecture's unsung wins (§6 "What I would not change", item 3): pull/subscribe at the wire layer would require the platform to schedule and connect _out_ to connectors, moving credentials platform-side and adding scheduling state to the platform. Push-only keeps credentials in the connector, the platform's surface area minimal, and matches how every modern observability/security platform works (Datadog Agent, OpenTelemetry Collector, GitHub Actions webhook receivers).

What this slice does is reconcile the three documentation surfaces with that wire reality so future contributors — especially community connector authors — do not implement against a gRPC interface that doesn't exist. The conceptual two-profile model is preserved; it still meaningfully distinguishes "connector pulls from the source on a schedule" from "source pushes to the connector via webhook." It's reframed as **operator-facing metadata about the connector's source-side behavior**, not as a wire-format dimension.

**Bundled: security-audit follow-up watchlist.** The same architecture review also flagged one BYPASSRLS code path (`internal/platform/status.go:BootstrapTenantID`) for periodic re-audit. The mitigation is a one-paragraph watchlist entry. Since both findings are docs-only and were surfaced in the same review session, bundling them into a single slice avoids two-PR churn for ~2 lines of additional content. See AC-5.

**Scope discipline.** This slice updates the three identified docs surfaces, adds the watchlist file, and updates downstream docs that cite the phantom methods. It does NOT:

- Add `Pull` / `Subscribe` / `Describe` / `AuthMethods` / `HealthCheck` / `ListEvidenceKinds` / `VerifyProvenance` RPCs to the wire. That's the wrong architectural direction per the reviewer.
- Touch `proto/connectors/v1/*.proto` or `proto/evidence/v1/*.proto`.
- Touch connector binaries under `connectors/`.
- Touch Go or TypeScript source code beyond `.proto` doc-comments if those drift.
- File the OAuth grants map (slice 325) or the legacy-bearer responder retirement (slice 326). Each gets its own slot.

## Threat model

Docs-only slice; no auth surface, no code change, no schema change. STRIDE pass:

- **S (Spoofing):** N/A. No new auth surface.
- **T (Tampering):** N/A. No user-input flow.
- **R (Repudiation):** N/A. No audit-log writes.
- **I (Information disclosure):** The reworded docs must not accidentally reveal future / aspirational platform behavior that isn't shipped. Mitigation: the reword stays grounded in present-tense observable behavior; speculation about future RPCs is excluded. The watchlist entry (AC-5) flags one BYPASSRLS code path for periodic re-audit but does NOT disclose any new attack surface — the path is already documented in `internal/platform/status.go` source comments (`BootstrapTenantID` doc string is ~40 lines of public reasoning).
- **D (Denial of service):** N/A. Static markdown.
- **E (Elevation of privilege):** N/A. Not an authz surface.

**Threat-model verdict: CLEAN.** Docs alignment with established wire reality.

## Acceptance criteria

- [ ] **AC-1.** `CLAUDE.md` invariant #3 reworded to reflect single-inbound-Push wire reality. Proposed replacement language (the implementing agent may choose an equivalent shape — record the choice in the decisions log):

      ```
      3. **The Evidence SDK exposes one canonical inbound API (`IngestEvidence`)** — a single `EvidenceIngestService.Push(record) → Receipt` gRPC RPC. Connectors are first-class peers in the operator's mental model: each is a separate process that holds source-side credentials and emits to the platform via `Push`. The connector's `profiles_supported` registration metadata (`pull`, `subscribe`, `push`) describes how the connector retrieves data **from the source** (scheduled poll, event subscription, or webhook receipt); the **platform-side wire surface is always push**. (canvas §4.1, `EVIDENCE_SDK.md`)
      ```

- [ ] **AC-2.** `Plans/EVIDENCE_SDK.md` §1 ("The framing — one ingestion API, two SDK profiles") preserved but reframed:

      - The two-row table's "Direction" column renamed to "Source-side direction" (or equivalent — the goal is to make clear the direction is between connector and source, not connector and platform).
      - The "Who initiates" column clarified to "Who initiates the source-side fetch."
      - Add a paragraph below the table making the wire reality explicit: "Both profiles emit to the same platform-side RPC (`EvidenceIngestService.Push`). The profile distinction lives in the connector process's own scheduling, not on the wire."

- [ ] **AC-3.** `Plans/EVIDENCE_SDK.md` §3 ("Connector profile (pull / subscribe) — recap"):

      - The methods table (`Describe`, `AuthMethods`, `HealthCheck`, `ListEvidenceKinds`, `Pull`, `Subscribe`, `VerifyProvenance`) is removed — those RPCs do not exist on the wire.
      - The section is rewritten as a description of the **internal connector loop pattern**: config-load → auth → source-side pull/subscribe/webhook-receipt → emit via `sdkClient.Push`. Cite one canonical example (recommend `connectors/aws/cmd/aws-connector/cmd_run.go` or `connectors/github/cmd/atlas-github/cmd_run.go`).
      - Add an explicit "What does NOT exist on the wire" callout listing the seven phantom RPCs so contributors do not try to implement against them.

- [ ] **AC-4.** `Plans/canvas/04-evidence-engine.md` §4.1: the same methods table (lines ~20–28 per current source) is removed; the framing paragraph is updated to match the wire reality; the link to `EVIDENCE_SDK.md` is preserved.

- [ ] **AC-5.** Add `docs/audits/_FOLLOWUP_WATCHLIST.md` (new file) with the BootstrapTenantID periodic re-audit entry:

      - File path (`internal/platform/status.go`)
      - Function name (`BootstrapTenantID`)
      - Risk summary (BYPASSRLS graceful-degradation fallback to "oldest user's tenant_id by created_at" for pre-slice-210 instances)
      - Audit cadence (proposed: annual, aligned with Q2 audit anniversary; refine per maintainer judgment)
      - Last reviewed (`2026-05-27 via architecture review`)
      - Pointer to the architect-reviewer transcript or this slice for context

- [ ] **AC-6.** Decisions log at `docs/audit-log/324-evidence-sdk-docs-alignment-decisions.md` captures:

      - Why the two-profile language is preserved at all (vs deleted entirely)
      - The exact wording chosen for "source-side direction" vs alternatives (`inbound`, `fetch mode`, `retrieval direction`, etc.)
      - The watchlist entry's cadence judgment
      - Confidence (`high` / `medium` / `low`) per decision

- [ ] **AC-7.** Cross-references audited:

      - `docs-site/docs/connector-authoring.md` (operator-facing connector authoring guide) — if it references the phantom methods, update.
      - `docs/spec/control-bundle.md`, `docs/openapi.yaml` — quick `rg "Pull\(kind|Subscribe\(kind|AuthMethods\(|ListEvidenceKinds"` pass.
      - `README.md` — no expected drift but verify nothing leaked.
      - Any ADR that cites the phantom RPC list.

- [ ] **AC-8.** `pre-commit run --files <changed-paths>` passes; if prettier reformats, re-stage and amend.

## Constitutional invariants honored

- **Invariant #3 (this slice's subject):** properly reframed, not weakened. The single canonical `IngestEvidence` API is preserved; the misleading "first-class peers on the wire" framing is corrected to "first-class peers in the operator's mental model."
- **Anti-pattern: "Closed proprietary connectors" (canvas §1.6):** the rewording specifically helps community connector authors. They now know the actual wire contract (one Push RPC) and do not waste time implementing against phantom methods.
- **Documentation discipline (CLAUDE.md "Working norms"):** "The design is opinionated for a reason — most ambiguity is resolved in `Plans/`, not invented at the keyboard." This slice closes a gap where `Plans/` was generating ambiguity rather than resolving it.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 (Evidence SDK)
- `Plans/EVIDENCE_SDK.md` §1, §3 (the affected sections)
- `CLAUDE.md` Architecture Invariants §3

## Dependencies

None. All cited surfaces (canvas, SDK, CLAUDE.md) are stable and merged.

## Anti-criteria (P0 — block merge)

- **P0-324-1.** Does NOT add `Pull` / `Subscribe` / `Describe` / `AuthMethods` / `HealthCheck` / `ListEvidenceKinds` / `VerifyProvenance` RPCs to `proto/connectors/v1/*.proto` or anywhere on the wire. The reviewer explicitly identified this as the wrong direction; we are aligning docs to reality, not changing reality.
- **P0-324-2.** Does NOT delete the connector/pusher conceptual model from `CLAUDE.md` or `canvas/04`. The two-profile framing is genuinely useful as operator-facing metadata; the reword preserves it, only correcting the wire claim.
- **P0-324-3.** Does NOT touch connector binaries (`connectors/aws/`, `connectors/github/`, etc.) or their internal `profiles_supported` declarations. Those are correct as-is.
- **P0-324-4.** Does NOT roll up other architect-reviewer findings into this PR. The OAuth grants map (slice 325) and the legacy-bearer 410 responder retirement (slice 326) are tracked as separate slots.
- **P0-324-5.** Does NOT add marketing-y framing or unprompted superlatives. The board-narrative banned-phrase list (CLAUDE.md AI-assist boundary) applies to canvas / CLAUDE.md / SDK docs as much as to board narratives.
- **P0-324-6.** Does NOT modify `docs/issues/_INDEX.md` — orchestrator surface.
- **P0-324-7.** Does NOT auto-merge — JUDGMENT type. The maintainer reviews the rewording before merge. Reword choices for constitutional-invariant text warrant a human read.

## Skill mix

- **Markdown editing:** measured tone, accurate wording, link verification.
- **Cross-reference scanning:** ripgrep for every doc surface that cites the phantom methods.
- **Threat-model verification:** confirm the watchlist entry does not over-disclose.
- **Slice 323 pattern:** docs-only refresh with a decisions-log entry.

## Notes for the implementing agent

**Wording sensitivity.** `CLAUDE.md` invariant #3 is constitutional text. Changing its wording is JUDGMENT-grade. The AC-1 replacement is one acceptable shape; alternative wordings that achieve the same correctness are fine. The decisions log should record which wording you chose and why.

**Two-step verification.** After editing each surface (`CLAUDE.md`, `EVIDENCE_SDK.md`, `canvas/04`), re-read it cold. The reword preserves the conceptual model; it does NOT weaken the architectural commitment. If the rewrite reads as "we used to claim X, now we claim less than X" — that is wrong. The rewrite should read as "X is preserved, and we now describe X more precisely."

**Watchlist scope.** The `docs/audits/_FOLLOWUP_WATCHLIST.md` entry (AC-5) is the entire scope of the architectural review's E finding. It is a one-paragraph entry, not a slice. Future security-audit slices (cf. `docs/audits/2026-Q2-security-audit.md`) can read from this file at audit-planning time.

**Connector-authoring docs cross-check.** If `docs-site/docs/connector-authoring.md` references the phantom methods, it MUST be updated in this slice. Community connector authors are the highest-impact downstream consumers of these docs.

**No code changes.** This slice is pure documentation. If the engineer finds themselves editing `.go` or `.ts` or `.proto` files (beyond a doc-comment touch-up in a `.proto` file), that is scope creep — file a separate slice.

**Spillover discipline.** If during the rewording you find:

- Another canvas section that drifts → file a separate canvas-refresh slice (NOT in this PR).
- A proto file with a stale doc-comment → file a small comment-cleanup slice (NOT in this PR).
- Another constitutional invariant that disagrees with shipped reality → flag it in the decisions log and file a separate slice; do NOT silently amend a second invariant in this PR.
