# Slice 129 — `actor_name` backend extension · decisions log

> JUDGMENT slice. Claude resolved the build-time questions inline and
> records them here so the post-merge maintainer iteration is traceable.
> The product runtime AI-assist boundary (CLAUDE.md) is constitutional
> and unchanged — this log is about how the slice was BUILT, not how
> the product behaves.

Branch: `backend/129-audit-log-actor-name`. Parent slices: 124 (unified
endpoint) + 125 (frontend `/audit-log` page).

---

## D1 — Wire shape: JSON `null` (not empty string) when no users row

**Decision.** When the LEFT JOIN onto `users` finds no match, the
response carries `"actor_name": null` (not `""`, not field omitted).

**Trade-off.**

| Option                         | Pros                                                                                    | Cons                                                                                                                                         |
| ------------------------------ | --------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) Empty string `""`          | Frontend `\|\|` falsy-fallback works without a null check                               | Ambiguous: distinguishes a real name `""` (the `users.display_name` column default!) from "no users row". The audit log MUST be unambiguous. |
| (b) JSON `null`                | Honest: the field is `null` iff no users row resolved. Frontend null-check is one line. | Empty-string `display_name` (a user who never set their name) ALSO renders to fallback — see the `renderActorLabel` empty-string test.       |
| (c) Omit field when no resolve | Smallest wire payload                                                                   | Cannot distinguish "older deployment without slice 129" from "this row had no resolve". Breaks P0-A6 graceful-degrade detection.             |

**Chose (b).** sqlc emits `*string` for the LEFT JOIN'ed nullable column;
Go's `encoding/json` serializes `nil` pointer to JSON `null` automatically.
No `omitempty` on the struct tag. The slice doc AC-2 explicitly permits
either `""` or `null` with "document the chosen shape in D1" — this is
that documentation.

The frontend's `renderActorLabel` (in `web/lib/api/audit-log.ts`) also
treats empty-string `actor_name` as a no-resolve (falls back to truncated
actor_id). That handles the corner case where a user's `display_name`
column has its schema-level default `''` and would otherwise render an
empty cell to the operator — operator-hostile. See the
`treats empty-string actor_name as a no-resolve and falls back` vitest
case.

---

## D2 — JOIN onto `users.display_name` (single canonical column)

**Decision.** The aggregator LEFT JOINs onto `users` and projects
`display_name`. No alternative column considered.

**Why.** The `users` table (migration `20260511000012_users_sessions_api_keys.sql`,
slice 034) carries exactly one human-readable column:

```sql
display_name TEXT NOT NULL DEFAULT ''
```

There is no `full_name`, no `given_name`/`family_name` split, no
`preferred_name`. `email` was rejected because (a) it is PII the audit
log doesn't already expose and (b) the column is intentionally separate
from `display_name` in the slice-034 schema. The whole point of the
slice is "render a human name"; `display_name` is the canonical answer.

---

## D3 — JOIN guard: regex-pre-filter on actor_id before `::uuid` cast

**Decision.** The JOIN's `ON` clause guards the UUID cast with a regex
predicate:

```sql
LEFT JOIN users u
  ON u.tenant_id = unified.tenant_id
 AND unified.actor_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND u.id = unified.actor_id::uuid
```

**Why.** Across the nine audit-log kinds the unified read produces, only
SOME branches' `actor_id` projections are UUID-shaped:

| Kind             | actor_id source             | UUID-shaped? |
| ---------------- | --------------------------- | ------------ |
| decision         | `user_id` (varies)          | Sometimes    |
| evidence         | `credential_id` (`key_xxx`) | NO           |
| exception        | `actor` (free string)       | Sometimes    |
| sample           | `actor` (free string)       | Sometimes    |
| audit_period     | `actor` (free string)       | Sometimes    |
| feature_flag     | `actor` (free string)       | Sometimes    |
| me               | `user_id::text`             | YES          |
| walkthrough      | `actor` (free string)       | Sometimes    |
| aggregation_rule | `actor` (free string)       | Sometimes    |

A direct `actor_id::uuid` cast on the non-UUID branches would raise
inside Postgres (`invalid input syntax for type uuid: "key_seed"`) and
abort the whole query.

Alternatives rejected:

- **`uuid_or_null()` helper function** — would require a new migration
  for the helper, and adds a SQL-side dependency the rest of the codebase
  doesn't carry. The inline regex is self-contained.
- **Pre-filter in the per-kind UNION branch SELECTs** — would duplicate
  the regex nine times and tightly couple the kind branches to the
  cross-kind JOIN. The outer-CTE-then-LEFT-JOIN structure isolates the
  resolve to ONE place.
- **`CASE WHEN actor_id ~ uuid_regex THEN actor_id::uuid ELSE NULL END`
  as a derived column** — adds an explicit column to the inner CTE and
  another `interface{}` typing risk on sqlc regen. The inline
  ON-clause predicate has the same effect without the extra column.

The integration test
`TestSlice129_ActorNameNilWhenNoUserRow` exercises this directly — the
decision-kind seed uses `user_id='seeder'` (literal string), and the
test asserts the row's `actor_name=nil` rather than failing the request.

---

## D4 — Two-leg tenant-scope guard: `ON u.tenant_id = unified.tenant_id` + RLS

**Decision.** The JOIN's `ON` clause carries an EXPLICIT predicate
`u.tenant_id = unified.tenant_id` even though Postgres RLS on `users`
already enforces tenant scope at the row level.

**Why.** Defense-in-depth. The slice doc's threat model row I
(information disclosure) calls out cross-tenant display-name leakage as
the canonical risk. Two independent barriers:

1. **RLS on `users`** (load-bearing): the `tenant_read` policy on
   `users` denies any row whose `tenant_id <> current_setting('app.current_tenant')`.
   This is the contract the slice doc treats as primary.
2. **Explicit ON-clause tenant predicate** (defense-in-depth): the JOIN
   itself only considers rows where the `users.tenant_id` equals the
   unified row's `tenant_id`. If RLS were ever bypassed (e.g. accidental
   `SET LOCAL ROLE atlas_service_account` somewhere upstream — a
   slice-024 hazard), the explicit predicate still scopes the JOIN.

The integration test
`TestSlice129_ActorNameRLSIsolatedAcrossTenants` exercises this: it
seeds a user row ONLY in Tenant B (display="leaked-secret"), then seeds
an audit row in Tenant A keyed to that user's UUID, and asserts Tenant
A's response carries `actor_name=null` for that row. If either barrier
were broken, the test would observe "leaked-secret" instead.

(The slice doc originally suggested seeding the SAME UUID under both
tenants — that's impossible because `users.id` is a global PRIMARY KEY,
so the same literal can't exist twice. The "Tenant A audit row keyed to
Tenant B's user UUID" formulation tests the same property with the same
discriminating power.)

---

## D5 — Slice 109 hand-narrows restored after `sqlc generate`

**Observation.** Running `just sqlc-generate` after editing
`internal/db/queries/unified_audit_log.sql` regenerated three files
beyond `unified_audit_log.sql.go`:

- `internal/db/dbx/policies.sql.go` — `AckDenominator`/`AckNumerator`
  collapsed back to `interface{}` (slice 109 D4 surface 1).
- `internal/db/dbx/scf_anchors.sql.go` — `StateResult`/`StateFreshnessStatus`
  collapsed back to non-nullable (slice 109 D4 surface 2 × 2 row types).
- `internal/db/dbx/querier.go` — comment block update propagated.

The three hand-narrow surfaces are documented in
`docs/audit-log/109-sqlc-toolchain-pin-decisions.md` D4 as
"preserved post-regen" with in-place annotations. Per that decisions
log: a future sqlc regen that picks them up SHOULD see the annotation in
the diff and re-apply (or remove if the underlying sqlc bug is fixed).

**Action.** Re-applied all three hand-narrows in this PR. Net diff:

- The slice 129 SQL change adds ONE new field (`actor_name *string`) to
  `ListUnifiedAuditLogRow` — sqlc handled this cleanly.
- The three slice-109 annotations are restored bit-for-bit.

No behavior change to slice 109 callers. Policy integration tests
(`./internal/api/policies/`) pass under the restored narrows.

---

## D6 — Backend extension only; no new endpoint, no role-gate change

**Decision.** The slice extends the EXISTING `GET /v1/admin/audit-log/unified`
endpoint in place. No new endpoint. The OPA role gate (admin OR auditor
OR grc_engineer) is unchanged.

**Why.** Aligns with anti-criterion P0-A3 ("Does NOT break callers of
the existing wire shape. The `actor_name` field is purely additive").
Older clients that ignore unknown fields continue to work; newer clients
that read `actor_name` get the resolved value.

---

## D7 — Frontend renderer extracted to `lib/api/` for vitest reach

**Decision.** The actor-cell render logic (`renderActorLabel` +
`truncateActorId`) was extracted from `web/app/audit-log/page-client.tsx`
into `web/lib/api/audit-log.ts`. The page imports the helper.

**Why.** Vitest at `web/lib/api/audit-log.test.ts` runs in node (no
JSDOM, no React render) per `web/vitest.config.ts`. The shape-level
contract — "prefer actor_name; fall back to truncated actor_id; tolerate
undefined for P0-A6 graceful-degrade" — is pure data → string, so it can
live in a non-JSX module and be tested without a render harness. This
keeps the test surface in vitest (CI gate `Frontend · vitest`) rather
than forcing the assertion into Playwright (slower, less granular).

The page-client retains a comment pointing at the helper's new location
so the call site is discoverable.

---

## Anti-criteria honored

- **P0-A1**: LEFT JOIN is tenant-scoped both by RLS on `users` (load-bearing)
  AND by the explicit `ON u.tenant_id = unified.tenant_id` predicate
  (defense-in-depth). `TestSlice129_ActorNameRLSIsolatedAcrossTenants`
  is the load-bearing test.
- **P0-A2**: Wire shape tolerates `null`. The `actor_name` Go field is
  `*string` (not `string`), the JSON tag has no `omitempty`, and the
  integration test `TestSlice129_ActorNameNilWhenNoUserRow` asserts the
  body literally contains `"actor_name":null`.
- **P0-A3**: No new endpoint. The slice-124 wire shape gains ONE
  additive field.
- **P0-A4**: All queries continue to run as `atlas_app` via the existing
  `tenancy.ApplyTenant` transaction setup. No `atlas_migrate` involvement.
- **P0-A5**: Integration-test fixtures use neutral identifiers
  (`test-example.test`, `test-tenant-a.test`, `test-tenant-b.test`). No
  vendor-prefixed tokens (no AWS `AKIA*`, no GitHub `ghp_*`).
- **P0-A6**: Frontend `renderActorLabel` treats `actor_name === undefined`
  identically to `null` — the page renders correctly when served by an
  older backend that doesn't carry the field. Locked by the vitest case
  `falls back to truncated actor_id when actor_name is undefined`.

---

## Spillovers filed

None. The slice landed in scope.
