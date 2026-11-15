# 2114 — Posture says "Scope not set" — decisions log

Build-time calls made for issue #2114 (framework posture returns a "scope not
set" state, not a percentage, for a version with no activated framework scope).
Parent: #2071, finishing the record lifecycles. #2056 did the same for the
board pack; this reuses its `scope_activated` column on the shared
`FrameworkPosture` query.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced.)

## Decisions made

### D1 — An explicit `scope_set` flag plus null percentages, not zeros

`GET /v1/frameworks/posture` rows gain `scope_set` (bool) and
`framework_version_id`. When `scope_set` is false, `coverage_pct`,
`covered_by_exception_pct`, `freshness_composite` and `trend_delta_90d` are
JSON `null`. A zero would read as "nothing covered", which is a claim the
platform cannot make before anything has been decided in or out of scope. The
query still computes the numbers; the handler drops them.

### D2 — Freshness is nulled too

The ticket names the percentage. Freshness composite is also a percentage over
the same requirement set (it honours the activated scope's exclusions, #1788),
so it carries the same "nothing decided yet" problem. All four go null together;
a consumer never has to work out which half of a row is meaningful.

### D3 — The export and MCP read the same shape

The dashboard export's `framework_posture` panel gains the same two fields. JSON
carries `null`; CSV and XLSX leave the cells empty. The MCP
`list_framework_posture` tool is a passthrough of the endpoint, so it carries
the flag as-is; its description now says what `scope_set=false` means.

### D4 — Old snapshots still decode

`FrameworkPosturePanelRow`'s percentages became `*float64`. A snapshot written
before this change (plain numbers, no `scope_set` key) decodes with its numbers
intact; `scope_set` reads false there because the key is absent. Snapshots are
not stored server-side today, so this is a guard for downstream consumers who
keep the JSON, pinned by a unit test.

### D5 — The tile is the link

The dashboard tile's "Scope not set" is itself the link to
`/framework-scopes/<framework_version_id>`, the route the board pack already
uses. A second "set the scope" link on a tile this small would be the same
destination twice.
