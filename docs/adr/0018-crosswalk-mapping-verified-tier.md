# ADR 0018 — Crosswalk-mapping verified-tier governance

**Status:** Accepted

**Date:** 2026-06-15

**Resolves:** [`CLAUDE.md`](../../CLAUDE.md) "Open decisions remaining" — _Control catalog governance (community-contributed controls, verified tier)_, **mapping-tier sub-question only**. The contributed-_control_ tier and public-marketplace sub-questions remain deferred to the public-marketplace conversation.

**Implements through:** [`docs/issues/483-crosswalk-mapping-verified-tier-governance.md`](../issues/483-crosswalk-mapping-verified-tier-governance.md)

---

## Context

The phase-2 framework expansion (ISO / PCI / CSF / HIPAA crosswalks — slices 438/447/480/481) ships every STRM edge as **agent-authored draft data**: the slice-438 loader writes `source_attribution = community_draft` on each `fw_to_scf_edges` row, with `scf_official` reserved for a publisher's official crosswalk. That field records **where a mapping came from** (provenance). It does not record **how trusted the mapping is now**, and there is no mechanism to promote a reviewed mapping to a trusted tier or to distinguish, on the read path, a vetted mapping from an unreviewed draft.

The platform's confidence rollup (slice 482) and, downstream, board/audit narratives consume these mappings. A mapping falsely treated as trustworthy could make the platform assert coverage the operator has not actually vetted. The constitutional AI-assist boundary is explicit: the platform must **never auto-approve its own mappings**. So a trust tier is needed, with a human-gated promotion act and an audit trail of who promoted what.

`CLAUDE.md` lists this as an open governance decision to settle before the public-marketplace conversation. This ADR settles the **mapping-tier** sub-question, which the active framework-expansion work needs now.

## Decision

### 1. A `mapping_tier` ladder, orthogonal to `source_attribution`

`fw_to_scf_edges` gains a `mapping_tier` enum, **distinct from** the existing `source_attribution` provenance field — the two answer different questions and are never collapsed:

- `source_attribution` (provenance): _where did this mapping come from?_ — `scf_official` | `community_draft` (unchanged).
- `mapping_tier` (trust): _how trusted is it now?_ — `draft` | `under_review` | `verified` | `rejected`.

The tier ladder is the **three-state lifecycle plus a rejected terminal**:

```
[draft] --claim--> [under_review] --verify--> [verified]
   |                     |
   +---------------------+--reject--> [rejected]   (terminal)
```

- `under_review` is an explicit "a reviewer has claimed this mapping" state between `draft` and `verified`. We chose the three-state ladder over a bare `draft → verified` because the intermediate state makes "who is reviewing what" legible and produces a richer audit trail — worth the small extra ceremony for a trust signal that feeds audit-binding narratives.
- `rejected` is a terminal state reachable from `draft` or `under_review` (a mapping judged wrong).
- **No skip-to-verified** for a `community_draft` edge — it must pass through `under_review`. The state machine validates legal transitions server-side.

### 2. Seed policy

- `scf_official` edges may seed directly at `verified` (a publisher's official crosswalk is trusted on arrival).
- `community_draft` edges start at `draft`.

### 3. Who may promote (the trust act)

**Any admin/maintainer-tier role** may transition a mapping's tier — the same catalog-write boundary the slice-438 loader uses (reuse the existing admin bearer/role middleware; promotion is not narrowed to `super_admin`). Verification is a privileged catalog-write capability: a `control_owner` or auditor viewer must **not** be able to self-verify a mapping (a non-admin transition is rejected 403). Every transition appends an immutable audit row (reviewer id, from-tier, to-tier, timestamp, note) in the **same transaction** as the tier change.

> Rationale for "any admin/maintainer role" rather than `super_admin`-only: verification is a routine catalog-curation act that the maintainer and any delegated catalog admin should be able to perform, not a rare super-privileged operation. The audit trail — not a narrower role — is what makes "who verified this?" answerable. (Revisit if a multi-operator deployment wants a tighter gate.)

### 4. Read-path exposure

`/anchors` (+ `/coverage`) surface the `mapping_tier` **label** so the operator and the slice-482 rollup can distinguish verified from draft. Reviewer identity is **not** on this public catalog payload; the reviewer-level audit trail is an admin/maintainer-scoped read. The slice-482 confidence formula is **unchanged for now** — the read exposes the tier, but tier-weighting of the confidence score is a deferred follow-on (avoids coupling two slices' scoring changes).

### 5. Deferrals (explicitly out of scope)

- Contributed-_control_ (whole SCF-anchor-equivalent) governance + a verified-control tier.
- A public community-contribution intake portal and marketplace.

These depend on the public-launch governance posture (slice 181's GOVERNANCE.md pure-community model) and are a separate follow-on once the marketplace conversation begins.

## Consequences

- The trust dimension becomes first-class and orthogonal to provenance and to version (ADR 0019) — the three are independent axes on a mapping.
- The AI-assist "no auto-approve its own mappings" boundary is operationalized for the crosswalk layer: nothing auto-promotes a `community_draft`; a human transition is required to reach `verified`.
- Migration is additive + reversible; existing `source_attribution` data is never rewritten or dropped (`community_draft` → tier defaults to `draft`).
- Downstream (482 rollup, board/audit narratives) can later weight by tier without re-litigating the governance model.

## Alternatives considered

- **Two-state ladder (`draft → verified`).** Rejected: simpler, but loses the "claimed for review" signal and a chunk of the audit trail's value for a trust signal that feeds audit-binding artifacts.
- **`super_admin`-only promotion.** Rejected as the default: verification is routine catalog curation; the audit trail provides accountability without over-narrowing who can do the work. (Available as a future tightening.)
- **Collapsing provenance and tier into one field.** Rejected (P0-483-3): they answer different questions; an official mapping can still be re-reviewed, and a community draft can become verified — both need both axes.
