# Risks carry the organisation's risk ID (#2838) — decisions log

Parent spec: #2812 (risk register import). This ticket lands the key the
import's duplicate detection and parent links will use.

- detection_tier_actual: none
- detection_tier_target: integration

## Decisions

**D1 — Column name `external_ref`, a nullable `TEXT`.** The name the spec
uses. It is singular and unrelated to `controls.external_refs` (a JSONB list
of framework references).

**D2 — Exact, case-sensitive uniqueness after trimming.** The value is
trimmed on write and an empty value is unset. "R-014" and "r-014" are two
IDs: a register's ID is the organisation's own string, and folding case
would refuse IDs a register legitimately distinguishes. If the import finds
case-variant duplicates in practice, a case-insensitive index is an additive
change.

**D3 — Enforced by a partial unique index, not an application check.**
`risks_tenant_external_ref_key ON risks (tenant_id, external_ref) WHERE
external_ref IS NOT NULL`. Concurrent creates with the same ID cannot both
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

**D6 — Not added to the `created` audit row.** That row's shape is shared
by every create path (#2835). The ID is visible on the risk and in every
later `fields_changed` row; adding it to `created` is left to the import
work if it needs it there.
