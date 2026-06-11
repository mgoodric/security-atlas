# 662 — Board pack §05 (Vendor burndown) not rendered; raw key leaks in publish blocker

**Cluster:** Board packs
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (whether §05 is in-scope for v1 board pack)
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-005).

## Narrative

On the board-pack review page, rendered sections jump from **§04 → §06** — **§05 (Vendor
burndown) is not rendered** and therefore likely cannot be approved. Yet the "Not ready to
publish" required-sections list still demands it AND displays the **raw internal key
`vendor_burndown`** inline among human-readable labels ("…Open findings, **vendor_burndown**,
Operational metrics…"). Net effect: the pack may be **impossible to reach 100% / publish**
because a required section is never approvable, and a raw key is shown to the user.
Re-verified on `main` build `2a3805b`.

## Threat model

No new data/scope/wire. Board-pack publish is a human-approved artifact (constitutional
AI-assist boundary) — this slice only fixes section rendering + the blocker label, not any
approval semantics.

## Acceptance criteria

- [ ] **AC-1.** Determine why §05 (`vendor_burndown`) does not render — a missing/!renderable
      section component, an empty-data guard that drops the section entirely, or a
      template-vs-required-list mismatch. Fix so §05 renders with its own "Approve section"
      control like every other section.
- [ ] **AC-2.** The publish-blocker list shows a **human-readable label** for every required
      section (no raw `vendor_burndown` key); the key→label mapping is complete.
- [ ] **AC-3.** JUDGMENT (decisions log): if Vendor burndown is genuinely out-of-scope for
      the v1 board pack, then it is removed from the **required-sections** set (so the pack can
      reach 100%) rather than left required-but-unrenderable. Record which path was chosen.
- [ ] **AC-4.** A draft pack with an empty/zero-vendor tenant can reach an
      approvable/publishable state (no permanently-blocking unrenderable section).

## Anti-criteria

- Does NOT change board-pack approval/publish semantics (still human-approved per section).
- Does NOT fabricate vendor-burndown content to fill the section (empty tenant → honest
  empty/—, per ATLAS-009 sibling).

## Dependencies

- The board-pack review/publish surface (`internal/board/*`, the board-packs web routes).
- Relates to slice 664 (vendor burndown empty-state honesty) and slice 665 (board generate-draft validation).

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-005** (priority high /
severity major). Re-tested open on build `2a3805b`.
