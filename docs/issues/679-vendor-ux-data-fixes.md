# 679 — Vendor UX/data: name/domain spacing, missing Delete control, owner role-as-email

**Cluster:** Vendors
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (vendor owner field type + delete affordance)
**Status:** `ready` — clusters three vendor findings (ATLAS-030 + 031 + 032).

## Narrative

Three vendor-surface defects, re-verified on `main` build `2a3805b` in the demo tenant.

| Sub           | Finding                                                                                                                                                                                                                                                          |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ATLAS-030** | Vendor list rows concatenate name + domain with **no separator** — "Pinecone Bank**pinecone**bank.example", "Riverstone Analytics**riverstone**-analytics.example". Reads as broken across all rows (CSS/render spacing).                                        |
| **ATLAS-031** | The Edit-vendor intro says "Delete removes the row and its cell bindings," but the page has **only "Save changes"** — no Delete control. (Save itself works.) Also no read-only vendor summary or review history is shown. Documented capability is unreachable. |
| **ATLAS-032** | The "Owner (email)" field (placeholder `alice@example.com`) is seeded with **"Head of Security"** — a role, not an email. Either the label/type is wrong or the seed is invalid; suggests no email validation on the field.                                      |

## Threat model

Vendor delete (if added) is a destructive mutation — must be RLS-tenant-scoped, confirm
before delete, and cascade/guard the cell bindings the copy describes. Owner-email validation
is input hygiene. No new wire surface beyond a delete endpoint if one is added.

## Acceptance criteria

- [ ] **AC-1 (030).** Vendor name and domain render with clear separation (spacing/secondary-
      text styling) across all rows.
- [ ] **AC-2 (031).** JUDGMENT (decisions log): either add the **Delete vendor** control the
      copy promises (with a confirm + correct cell-binding cleanup), or remove the copy that
      describes it. Default lean: add Delete (the copy already commits to it) with a confirm.
- [ ] **AC-3 (031).** Consider a read-only vendor summary / review history on the detail page
      (or record as a follow-on if out of scope) — at minimum the edit page should not be the
      only view.
- [ ] **AC-4 (032).** The "Owner (email)" field validates as an email; the demo seed populates
      it with a valid `@demo.example` address (not a role string). JUDGMENT: if the field is
      meant to hold a role/owner-name, relabel it and drop the email placeholder/validation.
- [ ] **AC-5.** Tests: list-row rendering separates name/domain; email validation rejects a
      non-email; the delete affordance matches the copy.

## Anti-criteria

- A Delete control (if added) does NOT skip confirmation or orphan cell bindings.
- Does NOT seed an invalid email into a validated field (fix the seed AND the validation).

## Dependencies

- `web/app/(authed)/vendors` (list + edit) + the vendor API (`internal/api/adminvendors`) + the vendor demo seed (`internal/demoseed`).

## Notes

Source: 2026-06-10 demo-tenant audit, items **ATLAS-030 (medium/minor), ATLAS-031 (medium/minor),
ATLAS-032 (low/minor)**. Re-tested open on `2a3805b`. (Vendor empty-state burndown is the
separate slice 664.)
