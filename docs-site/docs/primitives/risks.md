# Risks

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - What a Risk is in security-atlas (and why it is not "a control
      failure")
    - How risks are scored, treated, and linked to controls
    - How to use the risk register day-to-day and at board reporting
      time
<!-- prettier-ignore-end -->

A **Risk** in security-atlas is a _statement of plausible loss_ — not a
control failure, not an open finding, not a vulnerability. Controls
**treat** risks; risks are not derived from controls. This is a
deliberate inversion of the typical GRC-tool model, and it carries the
risk register through scoring methodology changes, framework changes,
and control re-architecture without rewrites.

## The shape

| Field                  | What it means                                                                                                |
| ---------------------- | ------------------------------------------------------------------------------------------------------------ |
| `title`, `description` | Plausible-loss statement (e.g. "loss of customer PII from production database").                             |
| `category`             | `confidentiality` · `integrity` · `availability` · `privacy` · `regulatory` · `operational` · `financial`    |
| `methodology`          | `nist_800_30` · `fair` · `cis_ram` · `iso_27005` · `qualitative_5x5`                                         |
| `inherent_score`       | Methodology-specific JSONB (FAIR: LEF + LM; NIST: likelihood × impact 1–5).                                  |
| `treatment`            | `accept` · `mitigate` · `transfer` · `avoid`                                                                 |
| `treatment_owner`      | Who is accountable for the chosen treatment.                                                                 |
| `linked_control_ids[]` | What controls treat this risk. Many-to-many.                                                                 |
| `residual_score`       | Computed — `inherent_score` modified by the effective state of the linked controls and the treatment choice. |
| `review_due_at`        | When the risk must be re-scored.                                                                             |

**Default methodology: NIST 800-30 qualitative** — the lowest common
denominator most auditors and regulators expect. FAIR is supported for
orgs that have invested in it. Methodology is a **per-risk** field, not
global, so they coexist in the same register.

## The risk register

Sign in and open **Risks** in the sidebar. The register lists every
risk in the active tenant, with current inherent / residual scores,
treatment, owner, and review due date.

The register supports a hierarchy view (slice 113): roll risks up by
business unit, by category, or by parent risk. The hierarchy is also
exportable (slice 136) as CSV / JSON / XLSX for board reporting.

## Linking risks to controls

A risk without linked controls is a risk you are not treating. A
control without a linked risk is a control you cannot justify. The
risk-control linking surface lives on the risk detail view:

1. Open the risk in the register.
2. Click **Controls** in the sub-nav.
3. Click **Link control** and select from the active control set.

The link is **many-to-many**: one risk can be treated by many controls;
one control can treat many risks. When all linked controls are
`pass`, the residual score drops below the inherent score; when one
drifts to `fail`, the residual score reflects the degradation.

## Risk review cycle

Risks have a `review_due_at` timestamp. When a risk passes this date
without re-scoring, it appears on the **Stale risks** dashboard panel
and contributes a warning to the next [board
report](../board-reporting.md). The platform does not auto-re-score —
human judgment remains the input; the platform just tracks the
cadence.

A typical review:

1. Open the risk detail view.
2. Review the linked controls' current state and any new findings.
3. Update the `inherent_score` if the threat landscape has changed
   (e.g. a new attack technique against your industry surfaced).
4. Update the `treatment` if your stance has changed.
5. Click **Mark reviewed** — sets `review_due_at` to the next cycle
   per the org's review cadence policy.

## Risk in board reporting

The quarterly [board pack](../board-reporting.md) auto-populates with:

- Top-N risks by residual score
- Risks where residual ≥ inherent (untreated or under-treated)
- Risks past their `review_due_at`
- New risks added since the last pack
- Risks closed (treatment complete) since the last pack

You can also export the full register on-demand for offline review:

```sh
curl -fsS -X GET "http://localhost:8080/v1/risks/export?format=xlsx" \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  -o risk-register.xlsx
```

## Next steps

- [Controls →](controls.md) — what treats risks
- [Policy →](policy.md) — what governs risk treatment
- [Scope →](scope.md) — where risks apply
- [Board reporting →](../board-reporting.md) — how risks roll up

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
