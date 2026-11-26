# Issue #1737 — external_refs on control bundles

Build-time calls made while adding `external_refs` to `control.yaml`
([#1737](https://github.com/mgoodric/security-atlas/issues/1737)), including
the structured `tested_under` entry the triage note asked for so the #1735
"audited under" report can read it.

`detection_tier_actual = none` · `detection_tier_target = none` (a missing
field, not a bug; no defect surfaced during the build).

## D1 — One list with a `kind`, not two fields

**Decision.** `external_refs: [{system, kind, id, note}]`, where `kind` is
`reference` (default) or `tested_under`. For `tested_under`, `system` is the
framework slug and `id` the requirement code.

**Why.** Both are "an identifier this control traces to elsewhere"; one list
keeps a single parser, column, search path and UI row. A separate
`tested_under:` field would duplicate all four. #1735 filters on
`kind = 'tested_under'`.

## D2 — `tested_under` is not validated against the framework catalog

**Decision.** The parser checks shape only (`system` pattern, non-empty `id`,
at most 100 entries, id/note length caps counted in runes, no duplicate
`system`+`kind`+`id`). It does not check that `soc2` is a known slug or that
`CC3.1` exists in it.

**Why.** Adopters record refs from reports against framework versions the
platform may not have loaded, and rejecting the upload for that would push
them back to free text in `description`. Resolving a ref to a requirement is
the #1735 report's job, which can show an unresolved ref as a finding.

## D3 — A JSONB column, CHECK only that it is an array

**Decision.** `controls.external_refs JSONB NOT NULL DEFAULT '[]'` with
`CHECK (jsonb_typeof(external_refs) = 'array')`. No child table.

**Why.** Refs are written once per control version, like `evidence_queries`,
and read with the control. A child table would need its own RLS policy and
supersession copy for no read that needs it yet. The element shape is
enforced by the parser, the only writer.

## D4 — Search reads element values, not the JSONB text

**Decision.** `/v1/search` matches controls on
`string_agg(system || id || note)` over `jsonb_array_elements`, as a third
column next to title and description.

**Why.** `external_refs::text` would match the key names: a search for
"system" or "note" would hit every control with a ref. The integration test
pins that.

## D5 — Wire field is `omitempty`

**Decision.** `controlWire.external_refs` is omitted when empty.

**Why.** `controlWire` is shared with `/v1/requirements/{id}/coverage`, whose
row query does not carry refs; an always-emitted field would read `null` or a
false `[]` there. Matches `freshness_class`.
