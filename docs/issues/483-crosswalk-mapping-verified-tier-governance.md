# 483 — Crosswalk-mapping verified-tier governance (community_draft → reviewed → verified)

**Cluster:** Catalog
**Estimate:** L (2-3d) — schema + state-machine + review API + audit trail
**Type:** JUDGMENT (the tier definitions + who-can-promote are governance
policy calls)
**Status:** `ready` (governance decision recorded 2026-06-15 in
[`docs/adr/0018-crosswalk-mapping-verified-tier.md`](../adr/0018-crosswalk-mapping-verified-tier.md):
three-state ladder `draft → under_review → verified` (+ `rejected`); promotion gated to any
admin/maintainer role; provenance vs tier kept orthogonal; `scf_official` seeds at `verified`,
`community_draft` at `draft`; read exposes the tier label but the slice-482 confidence formula is
unchanged for now. The slice's AC-9 ADR is satisfied by 0018 — the build implements against it.)

## Narrative

CLAUDE.md "Open decisions remaining" lists **"Control catalog governance
(community-contributed controls, verified tier)"** as an unresolved open
decision, to decide before "Public marketplace conversation." The framework
expansion now landing (ISO/PCI/CSF/HIPAA crosswalks — slices 438/447/480/481)
makes this decision pressing for the **crosswalk-mapping layer specifically:
every STRM edge those slices ship is agent-authored DRAFT data**, marked
`source_attribution: community_draft` by the loader, with no mechanism to
promote a reviewed mapping to a trusted tier or to distinguish, in the read
path, a vetted mapping from an unreviewed draft.

The foundation already exists. The slice-438 loader writes a
`source_attribution` field on every `fw_to_scf_edges` row, with the documented
contract: `scf_official` for a publisher's official crosswalk vs
`community_draft` for the agent-authored initial set (`internal/api/soc2import`
package doc, guardrail #2). What is missing is the **governance state machine**
that turns that flat attribution into a reviewable tier ladder, plus the audit
trail of who promoted what.

This slice ships, for the crosswalk-mapping layer:

1. **A mapping-tier state machine** on `fw_to_scf_edges`:
   `draft → under_review → verified` (and a `rejected` terminal), distinct from
   the existing `source_attribution` provenance field (provenance = _where it
   came from_; tier = _how trusted it is now_). `scf_official` edges may seed
   directly at `verified`; `community_draft` edges start at `draft`.
2. **A review/promotion API** — an admin/maintainer-gated transition endpoint
   that moves a mapping between tiers, recording reviewer, timestamp, and a
   note. Promotion to `verified` is the trust act.
3. **An audit trail** of every tier transition (append-only), so an auditor can
   answer "who verified this ISO→SCF mapping and when?"
4. **Read-path tier exposure** — the `/anchors` + `/coverage` reads surface the
   mapping tier so the operator (and the slice-482 coverage rollup) can
   distinguish a verified mapping from an unreviewed draft, and optionally weight
   confidence by tier.

**This is the mapping-layer scope only.** The broader open decision also covers
_community-contributed controls_ (whole SCF-anchor-equivalent controls authored
outside the project) and a public marketplace — those are larger and depend on
the public-launch governance posture (slice 181's GOVERNANCE.md established the
pure-community model). This slice resolves the **mapping-tier** sub-question,
which is the one the active framework-expansion work needs, and explicitly
defers the contributed-controls + marketplace sub-questions to a follow-on once
the public-marketplace conversation begins.

**Scope discipline.** Mapping-tier governance state machine + review API +
audit trail + read exposure. It does **not** build a community-contribution
intake portal, does **not** define the contributed-_control_ (not mapping)
governance, does **not** ship a public marketplace, and does **not** auto-promote
mappings (a human makes the verify call — consistent with the constitutional
"no auto-approve its own mappings" AI-assist boundary). **Follow-on slices:**
contributed-control governance + verified-control tier; public marketplace
intake.

## Threat model (STRIDE)

This slice adds a state machine + admin-gated transition endpoint that changes
how _trusted_ a catalog mapping is. The trust signal is the asset: a mapping
falsely promoted to `verified` could make the platform assert coverage the
operator hasn't actually vetted — feeding the slice-482 confidence rollup and,
downstream, board/audit narratives.

**S — Spoofing.** The promotion endpoint is a new authenticated write surface.
An unauthenticated or under-privileged caller must not transition tiers.
**Mitigation:** the transition endpoint is gated to the admin/maintainer role
(the same catalog-write boundary the 438 loader uses); reuse the existing bearer

- role middleware; no anonymous transition.

**T — Tampering.** The tier value drives trust. Tampering risks: (a) a direct
DB write bypassing the state machine (e.g. `draft → verified` skipping review),
(b) a client coercing an invalid transition.
**Mitigation:** the state machine validates legal transitions server-side
(`draft → under_review → verified`; `→ rejected`; no skip-to-verified except the
`scf_official` seed path); the transition is the only write path to the tier
column for tenant/admin callers; the audit-trail row is written in the same
transaction as the tier change (no untracked transition).

**R — Repudiation.** "Who verified this mapping?" must be answerable — the whole
point of the tier.
**Mitigation:** every transition appends an immutable audit row (reviewer id,
from-tier, to-tier, timestamp, note); the trail is append-only (no update/delete
of trail rows). This mirrors the slice-018 FrameworkScope approval-record
pattern.

**I — Information disclosure.** Mapping tier is catalog-level reference data
(not tenant-confidential) — but the audit trail names reviewers.
**Mitigation:** the tier field on the read path exposes only the tier label, not
reviewer identity; the reviewer-level audit trail is an admin/maintainer-scoped
read, not on the public `/anchors` payload. No tenant-confidential field is
added to the catalog read.

**D — Denial of service.** Bounded single-mapping transition writes; bounded
per-requirement tier reads.
**Mitigation:** the transition operates on one edge; the read adds one column to
existing bounded payloads. No unbounded list-all-transitions hot path.

**E — Elevation of privilege.** Promotion to `verified` is the trust act and
must be a privileged capability — a control_owner or auditor viewer must not be
able to self-verify a mapping.
**Mitigation:** the transition endpoint requires the admin/maintainer role; the
role matrix is asserted in an integration test (a non-admin transition is
rejected 403). This is the load-bearing E mitigation.

## Acceptance criteria

**Backend — tier state machine + audit trail**

- [ ] **AC-1.** `fw_to_scf_edges` gains a `mapping_tier` enum
      (`draft | under_review | verified | rejected`) distinct from the existing
      `source_attribution` provenance field. Additive, reversible migration;
      existing `community_draft` edges default to `draft`, `scf_official` edges
      may seed at `verified`.
- [ ] **AC-2.** A transition validates legal moves server-side
      (`draft → under_review → verified`; any → `rejected`; the `scf_official`
      seed-to-verified path) and rejects illegal skips (e.g.
      `draft → verified` for a community draft).
- [ ] **AC-3.** Each transition appends an immutable audit row (reviewer id,
      from-tier, to-tier, timestamp, note) in the SAME transaction as the tier
      change.

**Backend — review/promotion API**

- [ ] **AC-4.** An admin/maintainer-gated endpoint transitions a mapping's tier;
      a non-admin caller is rejected 403 (the E mitigation).
- [ ] **AC-5.** The `/anchors` (+ `/coverage`) read surfaces the mapping tier
      label so the operator + the slice-482 rollup can distinguish verified from
      draft. Additive field; no reviewer identity on this payload.

**Tests**

- [ ] **AC-6.** Integration test (`//go:build integration`): a full
      `draft → under_review → verified` transition writes the audit trail and
      surfaces the tier on the read path, against real Postgres.
- [ ] **AC-7.** Integration test (threat-model E): a non-admin caller's
      transition attempt is rejected 403; an illegal skip transition is rejected.
- [ ] **AC-8.** Pure-Go unit test covers the transition-legality state machine
      branches without a DB.

**Docs / JUDGMENT artifact + ADR**

- [ ] **AC-9.** An ADR (`docs/adr/NNNN-crosswalk-mapping-verified-tier.md`)
      records the mapping-tier governance decision: the tier ladder, who may
      promote, the provenance-vs-tier distinction, and the explicit deferral of
      contributed-_controls_ + marketplace to a follow-on. (Per CLAUDE.md: a new
      architectural decision lands as an ADR; the open decision it resolves is
      the mapping-tier sub-question.)
- [ ] **AC-10.** A decisions log
      (`docs/audit-log/483-mapping-tier-governance-decisions.md`) records the
      tier definitions, the promotion-role choice, and the "Revisit once in use"
      list. Include the `detection_tier_actual` / `detection_tier_target`
      header.
- [ ] **AC-11.** A changelog entry.

## Constitutional invariants honored

- **#7 — SCF is the canonical control catalog; mappings go requirement → SCF
  anchor.** This slice governs the _trust tier_ of existing requirement → anchor
  mappings; it creates no new edge shape and no requirement → requirement edge.
- **AI-assist boundary (hard) — "no auto-approve its own mappings."** The tier
  state machine REQUIRES a human transition to `verified`; nothing auto-promotes
  an agent-authored `community_draft`. This slice operationalizes that boundary
  for the crosswalk-mapping layer.
- **#6 — Tenant isolation (RLS).** The tier is catalog-level; the audit trail's
  reviewer-scoped read stays behind the admin boundary, not the tenant read
  path.

## Canvas references

- `Plans/canvas/03-ucf.md` §3.2 — STRM strength + the auditor-judgment framing
  the tier formalizes.
- `Plans/canvas/03-ucf.md` §3.3 — versioning + mapping lineage (the tier is the
  trust dimension orthogonal to version).
- `CLAUDE.md` "Open decisions remaining" — "Control catalog governance
  (community-contributed controls, verified tier)"; this slice resolves the
  mapping-tier sub-question.
- `CLAUDE.md` "AI-assist boundary (hard)" — "no auto-approve its own mappings."
- Slice 181 GOVERNANCE.md — pure-community model context for the verified tier.

## Dependencies

- **OQ — control-catalog governance.** RESOLVED 2026-06-15 — the maintainer recorded the
  mapping-tier governance decision in [`docs/adr/0018-crosswalk-mapping-verified-tier.md`](../adr/0018-crosswalk-mapping-verified-tier.md)
  (tier ladder + promotion-role choice + provenance/tier orthogonality + deferrals). This
  unblocks the slice; the AC-9 ADR is satisfied by 0018 (the build implements against it,
  expanding 0018 only if implementation surfaces a refinement).
- **#438** (generic crosswalk loader + `source_attribution`) — `merged`. This
  slice adds the orthogonal tier dimension.
- **#482** (coverage-strength rollup) — composes: the rollup may weight by tier
  once both land (not a hard dependency; 483 can ship tier exposure independently).

## Anti-criteria (P0 — block merge)

- **P0-483-1.** Does NOT auto-promote any mapping — a human transition is
  required to reach `verified` (AI-assist boundary).
- **P0-483-2.** Does NOT allow a non-admin to verify a mapping (threat-model E;
  AC-7).
- **P0-483-3.** Does NOT collapse provenance (`source_attribution`) and tier
  (`mapping_tier`) into one field — they answer different questions.
- **P0-483-4.** Does NOT write a tier transition without an audit-trail row in
  the same transaction (threat-model R).
- **P0-483-5.** Does NOT ship a community-contribution intake portal, a
  contributed-_control_ (not mapping) tier, or a public marketplace — those are
  the deferred sub-questions.
- **P0-483-6.** Does NOT expose reviewer identity on the public `/anchors`
  catalog payload (threat-model I).
- **P0-483-7.** Migration is additive + reversible; does NOT rewrite or drop
  existing `source_attribution` data.

## Skill mix (3-5)

`grill-with-docs` · `database-designer` (additive tier column + append-only
audit-trail table + the in-transaction transition) · `tdd` (integration-first;
the role-gate + transition-legality assertions are load-bearing) ·
`security-review` (new admin write surface + trust-signal integrity) ·
`simplify`.

## Notes for the implementing agent

- This slice is `not-ready` by design: it resolves a logged open decision, so the
  maintainer records the tier-ladder + promotion-role governance call (via the
  AC-9 ADR) before build. If picked up before that, STOP and surface the open
  decision per the template's open-question rule — do NOT guess the governance
  model.
- The provenance-vs-tier distinction is the crux: `source_attribution` already
  exists and says where a mapping came from; `mapping_tier` says how trusted it
  is now. Keep them orthogonal.
- Pattern-match the audit-trail + approval-record shape to slice 018's
  FrameworkScope approval lifecycle (approver, approved_at, diff/note) — the
  project already has this shape.
- Detection-tier classification: set both fields to `none` unless a bug
  surfaces during the build.
