# 022 — Policy entity + version control + 5 stock policies bundled

**Cluster:** Policies
**Estimate:** 2d
**Type:** HITL

## Narrative

Implement the policy library: `policies` rows with markdown body, version, effective_date, owner, approver, status (`draft → under_review → approved → published → superseded`). Policies are heavily versioned — every approved publish creates a new row referencing its predecessor. Bundle 5 high-signal stock policies as starting templates: Information Security Policy, Access Control Policy, Vendor Management Policy, Incident Response Plan, Change Management Policy. HITL: a compliance practitioner should review the stock policy text before merge — these are real policies users will rely on, not placeholder docs. Reject the anti-pattern of 50 placeholder templates.

## Acceptance criteria

- [ ] AC-1: `POST /v1/policies` creates a draft; subsequent publishes create new versioned rows linked to predecessor
- [ ] AC-2: 5 stock policies seeded on fresh deploy (under `policies/stock/`), each with owner=tenant_admin
- [ ] AC-3: Each stock policy includes: title, version, effective_date, body_md, owner role, approver role, linked_control_ids (link to ≥3 controls each)
- [ ] AC-4: State transitions enforce: `under_review → approved` requires approver role check
- [ ] AC-5: PDF render of a policy via `GET /v1/policies/:id/pdf`
- [ ] AC-6: HITL review log at `docs/audit-log/stock-policies-review.md`
- [ ] AC-7: A policy without ≥1 linked control surfaces a `warning: orphan_policy` flag

## Constitutional invariants honored

- **Anti-pattern rejected (policy template libraries dressed as a feature):** 5 real policies with linked controls, not 50 placeholder docs
- **Invariant 7 (SCF canonical):** policies link to controls anchored at SCF

## Canvas references

- `Plans/canvas/02-primitives.md` §2.6 (Policy entity)
- `Plans/canvas/10-roadmap.md` §10.1 (Policy library row)

## Dependencies

- #002

## Anti-criteria (P0)

- Does NOT ship policy text without HITL review
- Does NOT exceed the 5-policy stock bundle (later additions are user-authored)
- Does NOT permit publish without linked controls (orphan policies are surfaced)

## Skill mix (3–5)

- Go CRUD + workflow state
- Markdown rendering
- PDF generation (chromedp or wkhtmltopdf)
- Compliance policy authoring (HITL)
- sqlc versioned-row patterns
