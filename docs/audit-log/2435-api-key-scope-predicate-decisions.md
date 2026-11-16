# 2435 — API-key scope_predicate evaluated in the scope grammar: decisions log

Issue: mgoodric/security-atlas#2435. `api_keys.scope_predicate` is JSONB with
default `'{}'`, but ingest parsed it as a `key=value[,key=value]` string. Any
non-empty predicate went to that parser, and `{}` is not empty, so every key
read back from `api_keys` was refused at push with `rejected_scope_violation`.
That covers keys issued by `POST /v1/admin/credentials`, every key reloaded
into the credstore at startup, the NATS hop that carries the predicate in a
header, and MCP confirms under such a key.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The #2088 worker found it in a
  scratch test.
- **detection_tier_target:** integration.
  `TestScopePredicate_DefaultKeyPushes` issues a key through the real admin
  handler, authenticates its bearer from `api_keys`, and pushes. Before the fix
  it failed for all three default bodies with
  `decision=rejected_scope_violation ... predicate="{}"`; it passes after.

## Decisions

### D1: one grammar, not a second predicate schema

The predicate uses the `applicability_expr` grammar (`internal/scope`):
`eq`, `in`, `and`, `or`, `not`, `true`. The scope builder (#2088), exceptions
and framework scopes already write that grammar. A bare
`{"dimension": "value"}` map is not accepted. It is reported as a missing
`op` and denied.

### D2: a multi-valued record must satisfy the predicate in every cell

A record's dimension can carry several values, so the record is expanded to
the cross product of single-valued cells, and the predicate must hold in each
one. "Any cell" would let a prod-only key push a record tagged
`environment: [prod, staging]`. Expansion is capped at 4096 cells; past the
cap the push is denied, never evaluated partially.

### D3: a restricted key refuses a record with no dimensions

Under a restricted key, a record with no scope dimensions is denied, even for
a predicate such as `not(eq …)` that an empty cell would satisfy.
Validation already refuses an unscoped push before the predicate runs, so
this guard is defence in depth, covered by `scope.TestMatchRecordScope`.

### D4: malformed denies, with the reason audited

Decoding is strict: unknown fields, trailing data, and non-object JSON are all
refused, along with unknown ops. A malformed predicate is denied at push, and
the audit row records the parse error. Both issue paths refuse it up front:
HTTP returns 400 and gRPC returns `InvalidArgument`. Before this change, HTTP
issue sent the raw string to the JSONB insert and returned 500.

### D5: legacy key=value is converted at issue, with no data migration

The legacy form is converted to an `and` of `eq` nodes, both at issue and at
evaluation, for in-memory and proto callers. No `api_keys` row can hold the
raw form, because it is not JSON and the JSONB insert has always rejected it.
No migration is needed and no data changes.

### D6: whitespace-padded `{ }` is not treated as unrestricted

Only the exact `""`, `{}` and `null` mean unrestricted. That matches the
existing `isEmptyExpr`, and Postgres always renders an empty JSONB object as
`{}`. Any other spelling goes through strict decoding and is denied.
