# security-atlas#1731 — SOC 2 points of focus roll up into their criterion

The SCF workbook's SOC 2 column mixes the TSC criteria with their points of
focus (`CC1.1-POF1`). Both workbook loaders (wide SCF sheet and OLIR STRM) now
fold each point of focus into its parent criterion: no requirement row, an
edge from the parent, and metadata in the new
`framework_requirements.points_of_focus` JSONB column.

detection_tier_actual: none
detection_tier_target: none

## D1 — Recognize `<criterion>-POF<n>` in every workbook column

The roll-up keys on the code shape, not on the framework slug, so it applies
whatever slug or column the operator picks. No other framework in the SCF
workbook uses a `-POF<n>` suffix. Other sub-codes (NIST `AC-2(1)`) are left
alone; that is out of scope for this ticket.

## D2 — Edge precedence: own edge, then highest strength, then first seen

The criterion's own edge to an anchor always wins, whatever its strength. Among
point-of-focus edges to an anchor the criterion doesn't map itself, the highest
strength wins and a tie keeps the first one in source order. Relationship type
isn't used to break ties: STRM types aren't totally ordered, and strength is
the source's own measure of the mapping. A rolled-up edge's rationale is
prefixed `Rolled up from SOC 2 point of focus <code>.` so a reviewer can trace
it.

## D3 — Metadata shape and where it is written

`points_of_focus` is `[{code, scf_anchors[]}]`, ordered by point-of-focus
ordinal, anchors sorted. A point of focus lists every anchor it mapped, even
when the criterion's own edge won that anchor. The column is `NOT NULL DEFAULT
'[]'` and is written by the INSERT (the frozen-version immutability trigger
rules out a later UPDATE). The importer's unchanged check compares decoded
values, not bytes, because Postgres re-renders JSONB; an unchanged re-import
issues no UPDATE.

## D4 — The API omits the field when there are none

`requirement.points_of_focus` on `GET /v1/requirements/{id}/coverage` and
`/anchors` is present only when the requirement has points of focus. Always
emitting `[]` broke an existing client shape (an anchors integration test
decodes `requirement` as `map[string]string`), and every requirement outside
workbook-imported SOC 2 has none.

## D5 — No requirement detail view to extend

The ticket's desired behavior says the requirement detail view lists the
points of focus. `web/` has no requirement detail page today: the only
consumer is the BFF route `web/app/api/requirements/[id]/coverage`. The TS wire
type gains the optional field, and rendering it is left for the page when it
exists. The acceptance criteria only require the API.
