# Slice 596 — FAIR risk methodology decisions log

- detection_tier_actual: none
- detection_tier_target: unit

## D1 — FAIR and 5x5 rollups stay separate

Decision: FAIR risks aggregate in a FAIR-only quantitative view. The org-unit
hierarchy keeps the existing 5x5 `risk_counts` buckets for `nist_800_30` and
`qualitative_5x5` only, and exposes FAIR as `fair_exposure` with
`risk_count` plus summed `annualized_loss_exposure` dollars/year.

Rejected option: normalize FAIR annualized dollars into the 1..25 severity
scalar. That would make the hierarchy look continuous while hiding a scale
conversion policy the product has not defined. A board-facing dollar exposure
number is meaningful on its own; a dollar-to-ordinal normalization is not.

## D2 — FAIR score shape

Decision: canonical FAIR JSON uses:

```json
{
  "loss_event_frequency": 2,
  "loss_magnitude": 50000,
  "annualized_loss_exposure": 100000
}
```

`annualized_loss_exposure` is derived as
`loss_event_frequency * loss_magnitude`. The backend accepts legacy `lef`/`lm`
aliases and normalizes them before persistence so existing callers and fixtures
do not break.

## D3 — CIS RAM / ISO 27005 remain out of scope

Decision: only FAIR is made real in this slice. The create form still excludes
`cis_ram` and `iso_27005` because they do not yet have methodology-specific
score widgets or aggregation semantics.
