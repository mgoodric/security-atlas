# 2853 — Policy sync: missing pages: decisions log

Issue: mgoodric/security-atlas#2853, under the Confluence policy sync spec
(#2813). Builds on #2850 (the sync endpoint) and #2851 (versions and drift).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — missing is one column on the policy, not a table

`sync_missing_since` sits on the published synced row, as drift does
(#2851 D4). It is current state about one row: set by one run, cleared by a
later one. A CHECK allows it only on synced rows. The down migration drops it
without a guard, because the next run rebuilds it from the pages it carries.

### D2 — the time is the first sighting

A page absent from several runs in a row keeps the time it was first found
missing, and later runs do not rewrite the row. "Missing since" is the fact
the security leader needs, and `updated_at` only moves on a change, the same
rule as `SetSyncedPolicyDrift`.

### D3 — a page the run carries is present, even when skipped

A page that arrives with a missing or invalid property is reported skipped,
but it is still labelled in the space, so it is not missing. The same holds
for a page skipped because an in-tool draft holds its next-version slot.
"Missing" means only "not in the push".

The connector can't tell a removed label from a deleted page: it only sees
the labelled set. The report's reason names both causes.

### D4 — reappearing clears the state, whatever the page now holds

The run clears `sync_missing_since` on every page it carries before it looks
at versions and bodies, so a reappearing page's result (unchanged, drift or
new version) reports the cleared row. A new version's row starts with no
missing state, and `SupersedeSyncedPolicy` clears it on the row it
supersedes, as it clears drift.

### D5 — an empty push marks every page of the source missing

An empty `pages` list is a valid full set. The spec has the connector push
nothing at all when a run fails before it has the complete set, so an
outage can't arrive as an empty push. Marking pages missing changes no
policy, so even a mistaken empty push leaves every policy in force.

### D6 — scope: the run's source, the caller's tenant

The mark and clear queries filter on tenant, site and space and run under
the tenant's RLS context. A run for space OPS never touches a policy synced
from SEC, and the same page id in another space is another page.

### D7 — a new needs-attention band, `source_missing`

No existing band fits. "Failing" is for controls, "overdue" and "due soon"
are dated by a due date, and "ownership gap" is hidden from `for=me`. The new
band ranks after approvals and before overdue items: an authoritative source
that has disappeared needs a decision, but nothing is past due. The item has
type `policy`, so the existing `?type=policy` filter and the Policies module
gate apply. It is owned by the policy's owner role, so `for=me` shows it to
holders of that role, as for a policy review. It is dated by `since`, the
first sighting.

### D8 — no audit row

Marking or clearing missing writes no version and changes no published text,
and every run's response reports it. No `synced` audit row is written, as for
drift (#2851 D6).

### D9 — wire shape

- On the policy: `sync_missing_since`, omitted when the page was in the
  latest run.
- In the run report: a `missing` count, and one result per missing page after
  the pages of the push, ordered by page id, with `status: "missing"`, the
  policy's id, title and version, a reason, and `missing_since`.
