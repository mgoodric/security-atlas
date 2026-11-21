# Issue #2841 — Risk import: one methodology per batch, scores from columns

The scoring slice of spec #2812 (Q16), on top of the tracer (#2839,
`2839-risk-import-tracer-decisions.md`) and header detection (#2840).

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — An omitted methodology is `nist_800_30`

**Decision.** The upload's `methodology` field is optional. Omitted, the batch
takes `risk.DefaultMethodology` (`nist_800_30`), the same default as
`POST /v1/risks`. A value is trimmed. A repeated field is a 400, because a
batch has one methodology.

**Why.** A single create already defaults the same way, and the batch
methodology is still stored and shown either way. Requiring the field would
break the tracer's CLI and the export round trip (#2840) without making any
batch more correct.

## D2 — The import offers three methodologies, not five

**Decision.** `nist_800_30`, `qualitative_5x5` and `fair` are accepted.
`cis_ram`, `iso_27005` and any unknown value are a 400 that names the
accepted values. The batch's `methodology_chk` holds the same three.

**Why.** The ticket names the shapes to read: likelihood and impact, and
loss event frequency and loss magnitude. `cis_ram` and `iso_27005` validate
against a placeholder schema ("an object with at least one property"), so no
columns map onto them. Offering them would mean either inventing a shape or
creating every row unscored under a methodology the register did not use.

## D3 — Score fields are column-map fields, scoped to the methodology

**Decision.** There are eight new fields: `inherent_` and `residual_`
followed by `likelihood`, `impact`, `loss_event_frequency` or
`loss_magnitude`. Only the batch methodology's four are mapped. Another
methodology's score column stays unmapped and is kept on the row: a
"Likelihood" column in a FAIR batch is one example. Naming another
methodology's field in `column_map` is a 400. Bare "Likelihood"/"Impact" and
"LEF"/"LM" (and "Loss Event Frequency"/"Loss Magnitude") alias the inherent
pair. Only a "Residual …" header maps to the residual pair.

**Why.** It matches how the other fields work: aliases plus explicit
overrides, and nothing silently dropped. It also stops a register that
carries both kinds of columns from being read twice.

## D4 — The create path's score rule decides at parse time

**Decision.** Each row's cells are built into the methodology's JSON shape,
then passed through `risk.PrepareCreateScores`, the rule every create path
already uses. The result is what gets staged, so a FAIR score is staged with
its derived `annualized_loss_exposure`. Before that, the parser checks things
the create rule cannot see, and each check gives a specific reason:

- half a pair: a likelihood with no impact;
- a cell that is not a number in range (5x5 and NIST: a whole number 1..5);
- a residual with no inherent score.

**Why.** Without its own check, the parser would read a residual with no
inherent as an unscored row and drop the residual without saying so.
Running the store's rule at parse time means approval sees a score it has
already accepted. That keeps one score rule for import, API and MCP.

## D5 — FAIR cells are plain numbers

**Decision.** A FAIR cell must parse as a plain decimal. These are
`invalid_score`, not guessed at:

- `$250,000`, `1,000` and anything else carrying a currency sign or thousands
  separator;
- hex floats;
- `Inf` and `NaN`.

**Why.** "1,5" is 1.5 in some locales and 15 in others. A misread loss
magnitude goes straight into the board's dollar exposure. A rejection that
says "a plain non-negative number such as 0.5 or 250000" is cheap to fix in
the file.

## D6 — A rejection detail quotes at most 40 characters of a cell

**Decision.** An `invalid_score` detail repeats the cell, truncated to 40
characters.

**Why.** It is enough to find the cell, and a pasted paragraph is not echoed
back (the #2502 concern on the score write path).

## D7 — The export round trip does not carry scores yet

**Observation, not built.** `GET /v1/risks/export` writes `methodology`, and
writes `inherent_score` and `residual_score` as raw JSON columns. None of
them is an alias here, so a re-imported export lands them in unmapped and
creates the risks unscored under the batch methodology. Reading the JSON
columns and a per-row methodology would be a separate decision. The spec
gives the batch one methodology, which conflicts with an export that mixes
methodologies.
