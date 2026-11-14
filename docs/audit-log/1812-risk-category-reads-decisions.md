# #1812 — risk API and store read the category reference: decisions log

Migrate step of #1745 (expand -> migrate -> contract). The risk store and
`/v1/risks` API now read a risk's category from `risks.category_id`; the
`risk_category` enum is still dual-written until the contract ticket. The
calls below were made at build time and are recorded for revisit.

- detection_tier_actual: none
- detection_tier_target: none

(No product bug surfaced.)

## D1. `category` stays the slug; `category_id` and `category_name` are added

The ticket asks for the category's slug and name on the risk response.
`category` already carried the enum value, and the seeded slugs equal the
enum values, so `category` now carries the slug read from the reference and
existing clients (web, CLI, imports) read the same string. `category_name`
and `category_id` are additive. A nested `category` object was rejected: it
would break every reader of the string field in this batch, ahead of the web
batch of #1745.

## D2. Category names are filled by the store, from one category read

`ListRisks` / `GetRiskByID` keep returning `dbx.Risk`. The store reads the
tenant's categories once per call (`ListRiskCategories`, a handful of rows)
and fills slug and name from the reference. Joining in SQL would change the
generated row type of every risk query and ripple through every caller; the
extra read is cheap at v1 cardinality.

## D3. Changing a risk's category goes through `PATCH /v1/risks/{id}`

No path could change a risk's category before this ticket, and the ticket's
"can be moved off it" needs one. `PATCH /v1/risks/{id}` already exists as the
narrow, audited update for a risk's placement; it now also takes `category`
(slug or id) in the same transaction, with the same lock order as the other
risk writes (#1867 / #2204): resolve the category (vocabulary), then the
aggregation rules' advisory locks, then the row. The aggregation hooks run
after the write, so a rule's meta-risk can follow a child's new category.

Each change writes a `category_changed` row to `risk_audit_log`
(migration `20261030000000` widens the action CHECK). Moving onto an
archived category is refused like any new assignment, even when it is the
risk's current one; naming the risk's current active category is a no-op.

## D4. `?category=` takes several values, archived ones included

`?category=a&category=b` and `?category=a,b` both work. Each value is a slug
or an id. Archived categories are valid filters (#1745 user story 5: archived
categories stay filterable on demand). An unknown value is a 400 that names
every slug the tenant has, archived ones included, since all of them are
valid filters. The filter runs after the roll-up exclusion of #2060, so
`count` still equals the rows returned.

## Out of scope (later #1745 batches)

The CSV export, board pack, dashboard, MCP tools, aggregation-rule default
category, and the web still read the enum or show the slug. Each has its own
migrate ticket.
