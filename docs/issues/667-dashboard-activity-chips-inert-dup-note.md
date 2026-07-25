# 667 — Dashboard "Recent activity" filter chips are inert; placeholder note duplicated

**Cluster:** Dashboard
**Estimate:** XS-S (<0.5d)
**Type:** JUDGMENT (hide vs wire the chips)
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-013).

## Narrative

The All / Evidence / Controls / Approvals chips on the dashboard "Recent activity" card are
**non-interactive** (generic elements, no handler). A developer-facing note — "Filter chips
activate once the activity endpoint widens beyond the evidence branch" — is **repeated 4×**
in the DOM (in `title` attributes). Re-verified on `main` build `2a3805b`.

## Threat model

None — dead-control + internal-text cleanup.

## Acceptance criteria

- [ ] **AC-1.** JUDGMENT (decisions log): either **hide** the chips until the activity
      endpoint supports filtering, or **wire** them to real filtering. Default lean: hide
      (don't ship inert controls) unless the endpoint already supports the filter.
- [ ] **AC-2.** The developer-facing placeholder note is **removed** from user-facing markup
      (no internal "activate once…" text in `title`/DOM, and not duplicated).
- [ ] **AC-3.** If hidden: no empty chip row renders. If wired: each chip filters the feed
      and reflects active state (aria + visual).

## Anti-criteria

- Does NOT leave inert chips with an internal explanatory tooltip (the current state).

## Dependencies

- The dashboard Recent-activity card (`web/app/(authed)/dashboard`, the activity component).
- Related to slice 669 (activity feed signal-to-noise) — keep the filter model consistent if both touch filtering.

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-013** (priority medium /
severity minor). Re-tested open on build `2a3805b`.
