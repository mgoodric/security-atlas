# 024 — Vendor lite module

**Cluster:** Vendor lite
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement the minimum-viable vendor management module: a `vendors` entity with contract dates, DPA status, review cadence, criticality, last-review-date, owner, linked SOWs. Designed for ~30–80 vendors (not 5,000). This is the minimum to retire the user's vendor-tracking spreadsheet — practitioner research showed vendor reviews universally live in spreadsheets even when GRC tools have vendor modules. Phase 2 adds questionnaire issuance + evidence reuse from vendor trust centers. The slice delivers value because the user can stop using the spreadsheet and the dashboard's "Vendor risk burndown" panel becomes real.

## Acceptance criteria

- [ ] AC-1: `POST /v1/vendors` creates with: name, criticality (low/medium/high), contract_start, contract_end, dpa_signed (bool + date), review_cadence (annual/biannual/etc.), last_review_date, owner_user, linked_sow_uri
- [ ] AC-2: `GET /v1/vendors` lists with filter by criticality, review-overdue
- [ ] AC-3: `GET /v1/vendors/burndown?criticality=high` returns review-on-time fraction (powers dashboard panel + slice 032 board pack)
- [ ] AC-4: A vendor with `last_review_date + review_cadence < now` flagged `overdue=true`
- [ ] AC-5: Frontend has a simple vendor list view + create/edit form
- [ ] AC-6: Vendors are scope-tagged (per slice 017) so multi-product orgs can scope vendor relationships

## Constitutional invariants honored

- **Invariant 6 (RLS):** vendor rows tenant-scoped
- **Invariant 4 (multidimensional scope):** vendors scoped per dimension tuple

## Canvas references

- `Plans/canvas/10-roadmap.md` §10.1 (Vendor module row — lite)
- `Plans/canvas/01-vision.md` §1.4 ("Vendor risk module must work for ~30–80 vendors, not 5,000")

## Dependencies

- #002, #017

## Anti-criteria (P0)

- Does NOT include questionnaire issuance (phase 2)
- Does NOT include trust-center scraping (phase 2)
- Does NOT exceed lite scope — keep the slice tight

## Skill mix (3–5)

- Go CRUD
- Postgres date math (overdue calculations)
- sqlc-typed queries
- Next.js form + list views
- Scope tagging utilities (from slice 017)
