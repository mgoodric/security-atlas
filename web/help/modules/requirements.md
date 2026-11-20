---
id: requirements
title: Requirement detail
routes: [/requirements]
terms:
  [requirement, anchor, strm, org-mapping, coverage-strength, confidence-band]
diagrams: []
---

A requirement is one line of a framework, such as a single SOC 2
criterion, that an auditor checks you against. You don't build a control
for each requirement. The requirement maps to SCF anchors, and your
controls sit on those anchors, so one control can satisfy requirements in
several frameworks. This page shows that chain for one requirement.

## Coverage

The coverage number runs from 0 to 1. For each anchor, the strength of
its mapping is multiplied by how effective your best control on that
anchor has proved in evaluation, and the requirement takes the best
path. The band names the result: **uncovered** when nothing contributes,
**weak** below 0.5, **partial** from 0.5, and **strong** from 0.8. If
your framework scope's Statement of Applicability excludes the
requirement, the page says so and shows the justification.

## Mappings

**Catalog mappings** come from the published crosswalk. Each shows its
STRM relationship (equal, subset, superset or overlapping) and its
strength. **Org mappings** are your organisation's own, with the
rationale, source, author and date; they count toward your coverage
only. A control credited only through an org mapping is marked, so you
can see how much of the coverage rests on your own claims.

The controls listed are yours on those anchors. Open one to see its
evidence.

## Who can do what

- **Read this page**: everyone in your organisation.
- **Add or retire an org mapping**: admins and GRC engineers, from the
  Audited under report.
- **Change a catalog mapping**: no one here. The crosswalk comes from
  the SCF release.
