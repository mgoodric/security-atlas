# Controls

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - What a Control is in security-atlas (and why automated and manual
      ones are first-class peers)
    - How a Control is shaped — fields, lifecycle, ownership, scope
    - How to view, attest to, and reason about your control set
<!-- prettier-ignore-end -->

A **Control** in security-atlas is a single requirement that produces a
pass / fail / `n/a` / inconclusive state per scope cell, per point in
time. Controls live in the [Unified Control Framework
graph](https://github.com/mgoodric/security-atlas/blob/main/Plans/UCF_GRAPH_MODEL.md):
**one control, N framework satisfactions** — never one row per
framework.

## The shape

| Field                  | What it means                                                                           |
| ---------------------- | --------------------------------------------------------------------------------------- |
| `scf_id`               | Canonical SCF code (e.g. `IAC-01`). The anchor that mappings hang off.                  |
| `title`, `description` | Human-readable.                                                                         |
| `control_family`       | SCF taxonomy: AAA, AST, BCD, CFG, CHG, CLD, CPL, CRY, ...                               |
| `implementation_type`  | `automated` · `semi_automated` · `manual_attested` · `manual_periodic`                  |
| `owner_role`           | Who owns it (RACI). Resolved to a real person via the org's role assignments.           |
| `lifecycle_state`      | `draft` → `proposed` → `active` → `deprecated` → `retired`. Soft-versioned; reversible. |
| `applicability_expr`   | Boolean over scope dimensions — when this control applies.                              |
| `evidence_query_ids[]` | What evidence the platform reads to evaluate this control.                              |
| `policy_ids[]`         | What governance documents reference it.                                                 |

A `manual_attested` control has the same surface as an `automated` one —
lifecycle, ownership, freshness, scope, evidence trail. The only
difference is the evaluation source: an authorized owner uploads the
evidence (a screenshot, a signed PDF, a meeting log) or asserts state
with a digital acknowledgment. **Constitutional invariant 9 — manual
evidence is first-class.**

## Browsing the control set

Sign in and open **Controls** in the sidebar. The list view shows every
Control in the active tenant, filterable by family, framework
satisfaction, owner, and lifecycle. The hero-dashboard screenshot in the
[README](https://github.com/mgoodric/security-atlas/blob/main/README.md#screenshots)
shows the control browser in context.

Click any row to open the **Control detail** view. From there you can:

- See the framework requirements this control satisfies (SOC 2, ISO
  27001, etc.) — each shown with its STRM relationship type (`equal`,
  `subset_of`, `intersects`, `superset_of`, `no_relationship`).
- See current pass / fail state per scope cell.
- See the evidence query that drives evaluation and the latest matching
  evidence records.
- Open the policy that governs it (if linked).
- Open the risks it treats (if any).

## Attesting to a manual control

Manual controls render the same detail view as automated ones, with one
extra surface: an **Attest** button visible only to the owner role.

1. Open the Control detail view.
2. Click **Attest**.
3. Upload the supporting artifact (PDF / image / log file).
4. Add the attestation narrative (what you confirmed, on what date, against
   what evidence).
5. Submit.

The attestation lands in the evidence ledger as a record with
`source_attribution.actor = user:<your-id>` and `evidence_kind` matching
the manual schema the control declares. After it lands, the control's
`freshness_class` clock resets; the lifecycle clock starts ticking
toward the next required attestation.

## Bulk import via OSCAL

Controls are not authored one-at-a-time for SCF anchors — the SCF
catalog importer (slice 006) seeds the canonical anchor set on first
boot. For framework-specific control sets (the SOC 2 v2017 50-control
kit shipped in slice 010, for example), the platform reads OSCAL
catalogs:

```sh
just atlas-cli catalog import \
  --framework soc2 \
  --version v2017 \
  --catalog ./catalogs/soc2-v2017.oscal.json
```

The importer is idempotent — re-running with the same OSCAL file is a
no-op (content-addressed by sha256 of the catalog). See [Framework
setup](../framework-setup.md) for the end-to-end framework activation
flow.

## What changes when a control retires

`retired` controls remain in the evidence ledger and the audit trail —
they are not deleted. New evidence stops being collected; existing
mappings and historical control state are preserved for point-in-time
replay. Retiring a control does NOT retroactively remove satisfaction
from any frozen [AuditPeriod](../first-audit.md).

This is the practical answer to the "what about the year we DID have
that control?" auditor question — the answer is "open the audit period
from that year and the control state is exactly what it was."

## Next steps

- [Risks →](risks.md) — what controls treat
- [Evidence →](evidence.md) — what controls read
- [Policy →](policy.md) — what controls implement
- [Scope →](scope.md) — where controls apply
- [Framework →](framework.md) — what controls satisfy

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
Page issues can also be filed via the **Edit this page** link above.
