# 134 — In-app walkthrough refresh (sync with current UI state)

**Cluster:** Frontend / Docs
**Estimate:** 1-2d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` from the maintainer-driven full-docs cleanup. The user's informal term was "showboat"; the canonical name in the codebase is "walkthrough" (slices 027 + 070).

Slice 027 shipped the walkthrough-recording infrastructure; slice 070 shipped the v1 onboarding-walkthroughs content. Since then the platform has added pages and changed page chrome (the audit-log trio, admin/me role parity, the audit workspace, the risk hierarchy dashboard, etc.). The in-app walkthrough scripts likely click selectors that have moved, hover steps that point at affordances that no longer exist, and miss the operator-grade pages that ship the most value.

**What this slice ships:** an audit + refresh of every existing walkthrough script (in `docs/walkthroughs/` or wherever slice 027 + 070 landed them), re-recorded against the v1.10.0+ UI. New walkthroughs added for the pages that didn't exist when 027/070 shipped (audit-log page, audit workspace, risk hierarchy, settings, admin pages).

**Scope discipline (what is OUT):**

- README refresh — slice 132.
- mkdocs user-docs content refresh — slice 133.
- New walkthrough-recording infrastructure — slice 027 owns the recorder; this slice consumes only.
- Re-designing the walkthrough UX (popover positioning, click targets, etc.) — out of scope; refresh content, don't redesign delivery.

## Threat model

| STRIDE                       | Threat                                                                                                                                                  | Mitigation                                                                                                                                                                 |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a                                                                                                                                                     | n/a                                                                                                                                                                        |
| **T** Tampering              | A walkthrough that points the operator at a destructive action (`Delete control`, `Approve published policy`) without context could be misused.         | Each walkthrough step is read-only or read-with-explicit-warning; destructive actions get an explicit "this will publish to all tenants" callout.                          |
| **R** Repudiation            | n/a                                                                                                                                                     | n/a                                                                                                                                                                        |
| **I** Information disclosure | Walkthrough scripts may capture screenshots / animated GIFs / text from real data if recorded against a non-demo deployment.                            | Recording MUST be done against the slice-082 demo seed; same P0-A1 through P0-A3 from slice 132. If walkthroughs ship animated GIF/WebM previews, every frame is reviewed. |
| **D** DoS                    | n/a                                                                                                                                                     | n/a                                                                                                                                                                        |
| **E** Elevation of privilege | Walkthrough that admits a non-admin to an admin-only page (or vice versa) would leak that the role-check matrix is different from operator expectation. | Each walkthrough declares its required role (admin / auditor / grc_engineer / control_owner / viewer); script refuses to run when caller lacks the role.                   |

## Acceptance criteria (stub — to be expanded at pickup)

- [ ] AC-1: Inventory every existing walkthrough — list each script, its current state (working / broken-selector / stale-content), its role-requirement.
- [ ] AC-2: Audit + fix each existing walkthrough against the v1.10.0+ UI.
- [ ] AC-3: Add walkthroughs for the post-slice-027/070 pages: audit-log page, audit workspace, risk hierarchy dashboard, settings, admin pages.
- [ ] AC-4: Each walkthrough declares its role requirement + refuses to run for callers lacking the role.
- [ ] AC-5: Walkthroughs run against the slice-082 demo seed; the seed is sufficient to drive every step.
- [ ] AC-6: Re-record any preview GIFs/WebMs against the refreshed UI (if previews are part of the walkthrough delivery — verify against current slice 027/070 conventions).
- [ ] Final AC: every walkthrough end-to-end completes against the demo seed in a Playwright smoke spec.

## Constitutional invariants honored

- **#9 Manual evidence is first-class.** Walkthroughs cover manual workflows on the same footing as automated ones.
- **AI-assist boundary.** No AI-generated walkthrough copy without human review.

## Canvas references

- `Plans/canvas/01-vision.md` — operator persona the walkthroughs guide.
- `Plans/canvas/04-evidence-engine.md` — the manual-evidence first-class principle applies to walkthroughs covering manual paths.

## Dependencies

- **#027** Walkthrough recording infrastructure (merged) — owns the recorder + delivery mechanism.
- **#070** v1 onboarding walkthroughs content (merged) — this slice refreshes that content.
- **#132** README refresh (parent) — establishes the screenshot capture pipeline this slice reuses for any walkthrough-preview imagery. **Gate: 132 must be `merged` before 134 flips to `ready`.**

## Anti-criteria (P0 — block merge)

- **P0-A1 through P0-A3:** Inherit slice 132's information-disclosure anti-criteria for any captured imagery / GIFs / WebMs.
- **P0-A4:** NO new walkthrough-recording infrastructure; consume slice 027's only.
- **P0-A5:** NO destructive-action walkthrough step without an explicit operator-warning callout.
- **P0-A6:** NO walkthrough that admits a caller to a page their role cannot reach (role-requirement gate enforced).
- **P0-A7:** NO real customer data in any walkthrough capture.
- **P0-A8:** NO vendor-prefixed test fixture tokens.

## Skill mix

- **`grill-with-docs`** — terminology + scope.
- **slice 027's walkthrough recorder** — consume only.
- **Playwright** — smoke spec for end-to-end walkthrough completion.
- **slice 132's screenshot pipeline** — for any preview imagery.

## Notes for the implementing agent

Slice 132 ships first; this slice consumes its screenshot pipeline. Slice 133 may also ship before this one — coordinate at pickup time so walkthrough-preview imagery and mkdocs-site imagery share visual style.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 132 from the maintainer-driven full-docs cleanup. User used the informal term "showboat"; the canonical name "walkthrough" is used throughout this slice doc per slice 027 + 070 convention.
