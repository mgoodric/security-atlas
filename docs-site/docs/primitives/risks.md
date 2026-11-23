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
exportable (slice 136) as CSV / JSON / XLSX for board reporting; the
`parent_risk_ids` column lists the risks each row rolls up to. The
`category` column carries the category's slug and `category_name` your
organisation's name for it, so a renamed category still round-trips.

## Importing a risk register

**Risks → Import** (or `POST /v1/risks/imports`) reads a CSV or XLSX
register. Every row is staged for review or rejected with a reason, and
nothing becomes a risk until an approver approves it. A batch has one
scoring methodology (`nist_800_30` by default, `qualitative_5x5` or
`fair`), and every row's scores are read in that methodology's shape.

### Re-importing an export

A file from the register export re-imports with no column mapping. The
title, description, category, themes, review date, organisation risk ID
and owner arrive as they left. So do the scores:

- `inherent_score` and `residual_score` are the export's JSON. They are
  checked against the batch's methodology with the same rule a single
  create uses, so choose the methodology the exported risks were scored
  with. A score of another shape (a FAIR score in a NIST batch) rejects
  its row as `invalid_score`. An unscored risk (`{}`) imports unscored.
- A row gives each score either as its JSON column or as its pair of
  columns (likelihood and impact, or loss event frequency and loss
  magnitude), not both.

`owner_user_id` and `org_unit_id` are ids in the exporting
organisation. Imported back into that organisation, the owner is
linked when they are still an active user, and the risk is placed in
the org unit when the row names no org unit by name. Into another
organisation they name nothing, so they stay unmapped values on the
row, and nothing is rejected for them.

`parent_risk_ids` follows the same rule, id by id. Each id that is a
risk of the uploading organisation is kept on the row, and the approved
risk is linked under each of them, by the same rules as **Attach a
child** (below). An id that names no risk of the organisation stays an
unmapped value. Into another organisation every id stays unmapped: to
rebuild the hierarchy there, give a parent risk ID column with the
parents' organisation risk IDs. When a row has both, the parent risk ID
column is used and `parent_risk_ids` stays unmapped.

### Parent risk IDs

A parent risk ID column names the risk each row rolls up to, by
organisation risk ID. When the row is approved its risk is linked under
that parent, if the parent is a risk of the organisation. If the parent
is still a pending row, in this import or another one, the row waits
and the review page counts it under **Waiting on a parent**. It is
linked once a row with that ID is approved (or marked duplicate of an
existing risk), and fails once no pending row carries the ID any more.
An import has no expiry: a parent row left pending keeps its children
waiting, so approve or reject it. A link that fails flags the row
**Parent link failed** with the reason, and the risk is imported
without it; link it by hand from the parent's **Attach a child**.

### Text in a score column

The import does not translate words into scores. The bare headers
`Likelihood` and `Impact` are the inherent likelihood and impact of a
`nist_800_30` or `qualitative_5x5` batch, and must hold whole numbers
from 1 to 5. `LEF` and `LM` (and `Loss Event Frequency`, `Loss
Magnitude`) are the inherent pair of a `fair` batch, and must hold plain
numbers such as `0.5` or `250000`, with no currency sign or thousands
separator.

So an `Impact` column holding `High`, `Medium` or `Low` rejects every
row that has a value in it as `invalid_score`. Either:

- replace the words with numbers in the sheet before upload (for
  example High = 4), or
- leave the column out in the mapping step (`ignore_column=Impact` on
  the API). Its values are then kept on each row as unmapped, and the
  rows import unscored.

In a `fair` batch, `Likelihood` and `Impact` are not score columns, so
they stay unmapped and reject nothing. The same holds for `LEF` and
`LM` in a NIST or 5x5 batch.

## Rolling risks up into a parent risk

An org or company risk can have child risks: team findings that
evidence it, for example, under an enterprise risk you imported. On
the parent's detail page, **Children** lists them and **Attach a
lower-level risk** adds one. **Parents** on a child's page lists the
risks it rolls up to.

- A child must sit at a lower level than its parent (team < org <
  company) and be scored on the 5x5 grid. A risk cannot be attached
  under itself or under one of its own children, at any depth.
- Attaching or detaching never changes the parent's own score or
  treatment. The page shows the **roll-up** from the children beside
  the assessed severity, and flags the parent when the children roll
  up higher than it was assessed.
- A risk with at least one child is a **roll-up**. The register, its
  count, the dashboard's top risks and the board's top risks leave
  roll-ups out, so the same exposure is not counted twice. Tick **Show
  roll-ups** on the register to list them; each carries a **Roll-up**
  marker that links to its children. `GET /v1/risks?include_rollups=true`
  is the API equivalent.
- The API is `POST /v1/risks/{id}/children` with
  `{"child_risk_ids": [...]}`, `DELETE /v1/risks/{id}/children/{child_id}`,
  and `GET` on `/v1/risks/{id}/children` and `/v1/risks/{id}/parents`.

### Bulk writes queue up per organisation

Attaching children, and any risk write whose themes an active
aggregation rule targets, take one lock per organisation so two links
cannot build a cycle between them. The lock is per organisation, not per
theme: a write on `ownership` waits for one on `patching` if both themes
have rules. Writes whose themes no rule targets skip it.

For a bulk importer or a connector this means:

- Parallel requests for one organisation that match a rule run one at a
  time. Sending them concurrently gains nothing and holds connections
  while they wait. Send them in sequence, or keep concurrency low.
- Each request holds the lock for the length of its rule evaluation, so
  a large import into rule-targeted themes is slower than one into
  themes with no rule. Activate rules after the import when that
  matters: activation does not re-fire on risks written before it.
- A link and a risk delete that deadlock against each other are retried
  by the server, up to three attempts, before a 500. A `link_child_risks`
  MCP proposal that hits one stays pending; confirm it again.

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
