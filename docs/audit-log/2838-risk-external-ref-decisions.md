# Risks carry the organisation's risk ID (#2838) — decisions log

Parent spec: #2812 (risk register import). This ticket lands the key the
import's duplicate detection and parent links will use.

- detection_tier_actual: none
- detection_tier_target: integration

## Decisions

**D1 — Column name `external_ref`, a nullable `TEXT`.** The name the spec
uses. It is singular and unrelated to `controls.external_refs` (a JSONB list
of framework references).

**D2 — Case-insensitive uniqueness after trimming; the typed case is
kept.** The value is trimmed on write and an empty value is unset. "R-014"
and "r-014" are the same ID and collide: for a key the import uses to
detect duplicates and resolve parent links, a case variant is far more
likely a typing slip than a second risk. The value is stored and shown as
typed, so a risk can change only the case of its own ID. (Revised in review:
the first cut compared exactly.)

**D3 — Enforced by a partial unique index, not an application check.**
`risks_tenant_external_ref_key ON risks (tenant_id, lower(external_ref))
WHERE external_ref IS NOT NULL`. Concurrent creates with the same ID cannot both
land. The store maps the index's 23505 (matched by constraint name) to
`ErrExternalRefTaken`; the HTTP layer returns 409 with code
`external_ref_taken`. A CHECK holds the shape (trimmed, non-empty, at most
128 characters) as defence in depth behind the store's normalisation.

**D4 — Changed through PATCH as a record field.** It rides the existing
`fields_changed` audit row (before/after keyed `external_ref`, null when
unset), so it is audited exactly like the other record edits. `null` or an
empty string clears it.

**D5 — Export column appended last.** Existing column positions hold, as
for every earlier addition to the export.

**D6 — In the `created` audit row.** `CreatedState` carries `external_ref`
(null when unset), so an ID set at creation is recorded where the risk's
origin is, beside title, category, treatment and owner. Every create path
writes that row through `RecordCreatedTx` (#2835); the MCP and aggregation
paths set no ID and record null. (Revised in review: the first cut left it
out.)
