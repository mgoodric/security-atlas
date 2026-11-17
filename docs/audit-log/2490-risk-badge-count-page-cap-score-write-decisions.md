# Issue #2490 — Server-side badge count, page cap, numeric scores on write

Three follow-ups from the review of #2485 (#2482).

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

One defect surfaced during the work: the platform's `?level=` filter is
the risk hierarchy (team | org | company), not a severity band, so
`level=high` is a 400 and could never have driven the badge. Found by
reading the code, before any request was sent. The new band filter is
pinned in Go unit and integration tests.

## D1 — The badge's band and the platform's band are one rule, on the platform

**Finding.** The ticket suggested `level=high&limit=1`. `level` filters
`risks.level`, the hierarchy tier; GET /v1/risks had no severity filter at
all. The bands lived in three places that agreed on the numbers but not
on FAIR:

| Where                                                | High cutoff | FAIR                                                 |
| ---------------------------------------------------- | ----------- | ---------------------------------------------------- |
| `risk.SeverityBand` (Go)                             | `>= 15`     | "Never band a Monetary value": FAIR is unbanded      |
| `severityBand` (`web/app/(authed)/risks/filters.ts`) | `>= 15`     | Bands whatever wire `severity` it is given           |
| `countHighSeverityRisks` (sidebar badge)             | `>= 15`     | Counted a FAIR risk whose wire `severity` reached 15 |

The wire `severity` is `risk.AssessedSeverity(inherent_score)`: the score's
`severity`, else likelihood x impact. A FAIR inherent score is
`{loss_event_frequency, loss_magnitude, ...}` and its schema allows extra
properties, so a FAIR risk created with `likelihood` and `impact` beside
its loss figures had a non-zero wire `severity`, and the badge counted it.

**Decision.** `risk.SeverityBand` is right: a FAIR risk's severity is a
dollar figure (OPENENGINE-596, not normalized into the 5x5 rollup), and a
5x5 band on it means nothing. The platform now owns the count:

- `risk.SeverityHighMin` (15) and `risk.SeverityMediumMin` (8) are the band
  floors. `SeverityBand` and the new `SeverityBandRange` both read them.
- `GET /v1/risks?severity_band=high|medium|low` keeps the non-FAIR risks
  whose assessed severity is in `SeverityBandRange(band)`. High has no top,
  as `SeverityBand` has none, so a stored severity above 25 stays High.
- The badge reads `total` from `?severity_band=high&limit=1`. Its other
  filters are the register defaults (open, roll-ups left out), the same set
  `fetchRisksList` used to count over.

The client-side `countHighSeverityRisks` and its threshold are deleted
rather than aligned, so there is no second copy of the cutoff to drift.
The register's `severityBand` in `filters.ts` is display only (the pill
colour) and is left as it is.

**The SQL mirrors `AssessedSeverity`, including its failure modes.** The
`sev` lateral in `ListRisksPage` and `CountRisksPage` returns 0 when the
score is not an object or when any of `severity`, `likelihood`, `impact` is
present and not a number, because Go's `json.Unmarshal` into `*float64`
fails on the whole object then. Each factor is truncated before
multiplying (`int(l) * int(i)`). The one difference left is that Go
decodes into a struct, which matches JSON keys case-insensitively, and SQL
does not: a key spelled `Severity` is read by Go and not by SQL. The 5x5
schemas require the lower-case keys, so only a `cis_ram` or `iso_27005`
score written with a capitalised key can differ. That is left as a known
gap rather than a case-folding SQL expression.
`TestSeverityBand_MatchesWireSeverity` seeds each of these shapes and
checks the filter against `SeverityBand(wire severity)` for every band.

**Not changed.** The wire `severity` of a FAIR risk still reports
`AssessedSeverity`, so the register's severity pill can colour a FAIR row
with extra 5x5 keys. Zeroing it would change a wire field, which is
outside this ticket.

## D2 — "N of M" has its own one-row query

The register's "N of M" shared the badge's query key and its whole-list
walk because both happened to read the same response. They now count
different things, so `registerSizeQ` reads `total` from
`/api/risks?limit=1` under `["risks", "register-size"]`. A response
without `total` falls back to the rows returned, as before; the badge
instead treats a missing `total` as an error and renders nothing, because
with `limit=1` the fallback would be a count of one page.

## D3 — Page cap returns a partial result

`fetchAllRisks` stops after `FETCH_ALL_RISKS_MAX_PAGES` (50) pages, 25,000
risks at the 500-row platform cap. It returns what it read with
`partial: true` and the platform's `total`, rather than throwing: its only
callers now are the two pickers, and a picker offering the first 25,000
risks is more useful than an error. Nothing renders `partial` yet; it is
there so a caller can.

## D4 — What "a score in its valid range" means on write

`risk.ValidateScoreNumbers` runs in `Store.Create` and in the PATCH
record path for every non-FAIR methodology. FAIR keeps
`FairScoreFromJSON`.

- The score fields are the four keys a reader takes a number from:
  `score` and `severity` (0..25, `SeverityScaleMax`), `likelihood` and
  `impact` (1..5). Each one present must be a JSON number in range. JSON
  `null` is refused like a string.
- Other keys are not checked. The residual deriver writes
  `inherent_score`, `weighted_control_effectiveness`, `breakdown` and
  `warning` beside `score`, and the 5x5 schema allows
  `impact_dollars_low/high`.
- A residual of `{}`, JSON null or nothing is unscored and allowed: it is
  what create stores by default and what the deriver later replaces. Any
  other residual must be an object carrying a value `ScoreValue` reads, so
  `{"note": "x"}` or `{"likelihood": 2}` is refused rather than stored as a
  blob that looks scored and reads as unscored.
- The inherent score is checked too. The 5x5 schemas already require
  integer likelihood and impact, but allow extra properties, so
  `{"likelihood":3,"impact":4,"severity":"20"}` passed the schema and then
  read as severity 0. `cis_ram` and `iso_27005` have a permissive schema
  and accepted `{"severity":"20"}` outright.
- PATCH cannot write `residual_score` (it is in `patchRefusedFields`, a
  400), so PATCH checks only the inherent score and leaves a stored
  residual alone: an unrelated edit is never refused for a residual it did
  not touch.

**Status code.** The ticket asks for 422. A score that fails its
methodology's JSON Schema is still a 400 (`ErrInherentScoreInvalid`, the
existing contract); the new refusal is a 422 (`ErrScoreNotNumeric`). The
split is: 400 for a body of the wrong shape for its methodology, 422 for a
well-shaped body whose score value the platform cannot read.

**Not covered.** The MCP `create_risk` confirm applier
(`internal/mcp/writeproposals/appliers.go`) inserts with a raw `INSERT`, a
fixed `'{}'` residual, and no `ValidateInherentScore` call at all. It never
writes a residual, so this ticket's rule is not reachable through it, but
its inherent score is unvalidated. That is a separate gap and is reported
on the PR rather than fixed here.

## D5 — Existing rows are counted, not fixed

`scripts/dry-run-2490-risk-score-numbers.sql` counts, per tenant and
methodology, the stored non-FAIR rows the new rule would refuse, in a READ
ONLY transaction that is rolled back. It prints no ids, titles or scores.
It has been run only against a scratch cluster seeded with one row of
each finding; it has not been run against any live install, and no data
was changed. The readers already treat such rows as unscored (#2482 D3),
so leaving them in place changes nothing a user sees.
