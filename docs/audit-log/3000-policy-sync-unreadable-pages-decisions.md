# 3000 — Policy sync: unreadable pages: decisions log

Issue: mgoodric/security-atlas#3000, from #2954 (PR #2996, D2), under the
Confluence policy sync spec (#2813). Builds on #2853 (missing pages) and
#2943 (the converter's depth and size limits).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — the wire shape: two optional fields on a page entry

A page entry gains `unreadable: true` and `unreadable_reason`. An unreadable
entry needs an `id` and a reason. It must not carry `body_md` or
`properties`; either one skips the page, so a confused pusher hears about
it. `url` and `title` are optional. A reason sent without `unreadable: true`
skips the page too, so a typo in the flag doesn't push a page with no body.

The response gains an `unreadable` count, an `unreadable` result status and
`unreadable_since` on that result.

`POST /v1/policies:sync` is a REST request with no version of its own. It is
not a schema-registry evidence kind, so the #2981 per-version rules have no
version to bump here. Under the same principle the change is additive: both
fields are optional, a pusher that never sends them sees no change in
behaviour, and the new response fields are extra keys a client already
ignores. If a pusher sends `unreadable` to a platform older than this
change, that platform ignores the unknown key. It then reads the entry as a
page with null properties and skips it as `missing page properties`. That
still counts as present, so the policy is not marked missing.

### D2 — an unreadable page leaves its policy exactly as it was

Its body, `synced_body_sha256`, properties, version, body drift and property
drift are all left unchanged. No row is written, no version is created and
no `synced` audit row is added. The only writes are:

- clearing `sync_missing_since`, as any present page does (#2853 D3);
- setting `sync_unreadable_since` and `sync_unreadable_reason`.

The integration test checks the body, both hashes, the version, the status,
the policy count and the audit-row count before and after.

### D3 — a page with no policy yet gets none (no QUESTION needed)

A never-seen page that arrives unreadable has no body to publish. A policy
can't be created without one, and an empty placeholder policy would be
fabricated coverage. So the result is `unreadable` with no `policy_id`, the
reason gets "no policy exists for this page yet, so none was created; it
syncs once its body can be read" appended, and nothing is written. That is
not a product choice. The only other option, creating a placeholder,
breaks the constitutional rule against fabricating coverage. The page also
does not take over an imported policy with its title (#2854). A takeover
compares and publishes a body, and there isn't one.

### D4 — the state lives on the policy row, like missing

`sync_unreadable_since` and `sync_unreadable_reason` sit on the published
synced row, as `sync_missing_since` does (#2853 D1). The needs-attention
list reads live rows. The run report alone can't feed a dashboard item, so
the state has to be stored. A CHECK sets both columns together, allows
them only on synced rows, and holds the reason to 1–500 characters. The
down migration drops both without a guard, because the next run rebuilds
the state.

The time records the first time a run carried the page unreadable. A later
run with the same reason writes nothing, and `updated_at` does not move. A
new reason replaces the stored one and keeps the time. The state clears on
every published synced row of the source whose page the run did not carry
unreadable. A page carried readable is in step again. A page not carried at
all is missing, which takes over from unreadable. A version bump that
supersedes the row clears it on the superseded row too.

### D5 — the reason is bounded and stored as plain text

The reason is the pusher's text, so the platform normalises it before
storing it:

- invalid UTF-8 becomes U+FFFD;
- control characters (an ANSI escape's ESC, NUL, tabs, newlines) become
  spaces, and runs of whitespace collapse to one;
- anything over 500 characters (runes, not bytes) is cut to 499 plus an
  ellipsis;
- an empty result skips the page.

Truncating is kinder than refusing: a long converter error still keeps the
page present. Markup is not stripped. The reason is shown as text, and
JSON encoding and React's text rendering escape it. The integration test
round-trips a `<script>` fragment unchanged as data. Stripping control
characters also keeps an escape sequence out of the connector's terminal
report.

### D6 — a page id carried twice

If one push carries the same id both readable and unreadable, the readable
entry wins and the unreadable one is skipped as "appears more than once".
If the id appears unreadable twice, the first entry counts and the second
is skipped. This follows the rule for readable duplicates.

### D7 — needs-attention: a new `source_unreadable` band, ranked 4th

The band comes right after `source_missing` and before `inconclusive`.
Both bands are about a synced policy whose source needs fixing. A missing
source ranks higher, because nothing at all is arriving for it. The label
is "Unreadable at source", the action is "Fix source page", and the owner
is the policy's owner role, as for missing. The band count grows from
seven to eight. The web band label and badge maps gain the new key.

### D8 — connector: send converter-refused pages, keep the exit contract

`policysync.Build` now pushes a converter-refused page as unreadable, with
`body not converted: <converter error>` as its reason. The connector no
longer prints its own `skipped by connector` line. The platform's
`unreadable ... reason=` line replaces it, and the totals end in
`unreadable=N` (the platform's count) in place of `skipped_by_connector=N`.

Two things are deliberately unchanged:

- **`--max-skipped` and exit 3** still count the pages the converter
  refused. The connector counts them itself, so the check doesn't depend on
  the platform's version. An unreadable policy is frozen at its last good
  body, which still needs a human, so a scheduler should still alert.
- **A run with no readable page pushes nothing** and exits 1, as before. A
  converter that refuses every body is a connector fault. Pushing every
  page unreadable would bury that fault under a needs-attention item per
  policy.

### D9 — authorization unchanged

The unreadable path sits behind the same gate as the rest of the sync. The
policy-sync gate in `internal/api` admits only a policy-sync key, the
handler checks `PolicySyncOnly()` again, and every query is scoped to the
caller's tenant (RLS) and the run's source. The #2873 owned-kind guard is
on the generic evidence push and is not touched.
