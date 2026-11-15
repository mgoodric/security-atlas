# #1814 — exports, board pack and dashboard snapshot print category names: decisions log

Migrate step of #1745 (expand -> migrate -> contract). The risk-register
export, the dashboard snapshot export and the board brief and pack read a
risk's category from `risks.category_id` and print the organisation's name
for it. The `risk_category` enum is still dual-written until the contract
ticket.

- detection_tier_actual: none
- detection_tier_target: none

(No product bug surfaced.)

## D1. `category` stays the slug; `category_name` is appended

Every surface keeps its `category` column or key, now read as the slug of
the referenced category (equal to the enum value for the seven seeded
categories), and gains `category_name` beside it. In the two CSV shapes
(`/v1/risks/export`, the dashboard snapshot's `risks.csv`) the new column is
appended last, the same rule #1746 and #1748 followed, so consumers keyed on
column position keep working. Both CSV writers already pass every cell
through the formula sanitiser, which covers the organisation-entered name.

## D2. Names are read in the caller's tenant transaction, one category read

As in #1812, each read fetches the tenant's categories once
(`ListRiskCategories`, under RLS) and fills slug and name from the
reference, rather than joining in SQL and changing the generated row type of
`ListRisks` / `ListRisksForBoardBrief`. The two board stores (brief and
pack) shared an identical projection; it is now one helper,
`riskRowsFromDB`.

## D3. Board packs freeze the name at generation; old packs show the slug

A brief or pack is a frozen snapshot, so `RiskAging.category_name` is
written into its content at generation time. Renaming a category later does
not rewrite a stored pack; the next generated pack shows the new name.
Packs stored before this change have no `category_name`. They still decode,
and every renderer (the two narrative templates, the two PDF tables, the web
top-risks table) goes through a fallback to the slug (`CategoryLabel`), so
they render as they did before.

## D4. The web board-pack table is in this batch

The ticket's audience is the board reader. The web top-risks table reads
the same stored-pack JSON, so it shows `category_name` with the slug as
fallback. This is a two-line display change; the rest of the web category
work stays in the web batch of #1745.
