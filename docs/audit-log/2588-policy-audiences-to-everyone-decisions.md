# 2588 — Policy audiences: stock templates to Everyone; generic free-text audiences mapped: decisions log

Issue: mgoodric/security-atlas#2588, Matt's decisions (2026-11-17) from the
review of #2580 (#2524). Follows #2524's D6, which left the existing data and
three stock templates as they were.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced; this is the follow-up
  #2524 D6 deferred.
- **detection_tier_target:** none.

## Decisions

### D1 — the three team-name templates target only `everyone`

Change Management, Incident Response and Vendor Management now carry
`acknowledgment_required_roles: [everyone]`, the same as the two templates
#2580 switched. The team names are dropped, not kept beside `everyone`:
`everyone` already covers them, and a kept team name would be flagged
"unrecognised audience" on every new tenant. `TestLoadFromFS_RealStockBundle`
pins all five templates to `[everyone]`.

### D2 — exact, case-sensitive match on the four values

The migration maps `employee`, `all`, `all_staff` and `contractor` exactly, as
`scripts/dry-run-2524-policy-audiences.sql` reports them. `Employee` or
`all staff` are left and stay flagged. The counts Matt approves are then the
rows the migration changes, with no widening he did not see.

### D3 — every policy row, whatever its status

Drafts, published and superseded rows are all rewritten. The dry run counts
every row, so restricting by status would make its counts disagree with what
the migration does. A superseded row's audience no longer drives any pending
list, so rewriting it changes nothing a member sees; the audit row keeps what
it was.

### D4 — order kept, repeats dropped

Each value is mapped in place and the first occurrence wins, so
`{employee, contractor}` becomes `{everyone}`, `{engineer, all}` becomes
`{engineer, everyone}` and `{all_staff, everyone, viewer}` becomes
`{everyone, viewer}`.

### D5 — audit in `policy_audit_log`, new action and two array columns

One `audience_mapped` row per changed policy, written in the same statement as
the update, actor `system:migration-2588`, with `policy_title` /
`policy_version` and new `previous_audience` / `new_audience` `TEXT[]` columns.
Typed array columns, rather than the array squeezed into the `TEXT`
`previous_state` / `new_state` (which carry policy status), keep the before and
after values queryable. `policy_audit_log` stays append-only.

### D6 — re-runnable, a no-op when nothing matches

The DDL is `DROP CONSTRAINT IF EXISTS` / `ADD COLUMN IF NOT EXISTS`, and the
update matches only rows that still carry a generic value. After one run none
does, so a second run changes no row and writes no audit row.
`TestPolicyAudienceGenericToEveryoneMigration` applies it twice and checks
exactly one audit row per changed policy.

### D7 — the down migration does not restore the free-text values

It reverses the schema only, and refuses while any `audience_mapped` row or
audience value exists in the log (the #2009 rule for audit evidence). The
`previous_audience` column is the record of what each policy carried.

### D8 — production gate

The migration runs on every deployment that applies it, so it merges only on
Matt's go after the dry run's counts for each deployment with real data. The
PR says so; nothing in the migration itself waits for the go.
