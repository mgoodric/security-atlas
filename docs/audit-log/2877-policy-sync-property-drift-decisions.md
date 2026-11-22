# 2877 — Policy sync: property-only page changes are drift: decisions log

Issue: mgoodric/security-atlas#2877, under the Confluence policy sync spec
(#2813). Found in #2851 (PR #2876): the sync compared only a page's
version and body hash, so a property-only change with no version bump was
reported `unchanged` and silently dropped. Spec story 16 (properties stay
in sync with the system of record) held only when the page was bumped.

This log is the #2813 spec's D-log entry for that gap.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The gap was found while
  building #2851, not by a test.
- **detection_tier_target:** integration. A sync integration test pushing
  a property-only change would have caught it.

## Decisions

### D1 — a property-only change is drift, not an in-place update (Matt, 2026-11-21)

The ticket offered two outcomes: update the current synced version in
place with an audit row, or report drift until the page is bumped. Matt
chose drift ([ticket comment](https://github.com/mgoodric/security-atlas/issues/2877#issuecomment-7063170291)):
the change is reported as `drift`, naming the changed keys, until the page
gets a new version, and nothing is applied in place.

This matches body drift (#2851, spec Q24): what staff acknowledged, and the
review date and audience the calendar and acknowledgement targeting read,
change only when the source publishes a new version. The cost is that a
review-date change in Confluence needs a version bump to land. The drift
notice says so.

### D2 — property drift is recorded separately from body drift

Two new columns, `sync_property_drift_detected_at` and
`sync_property_drift_keys`, beside #2851's `sync_drift_detected_at` and
`sync_drift_body_sha256`, rather than widening the body-drift columns. A
page can drift in either, both or neither, and each clears on its own: a
run that reverts only the body keeps the property drift
(`TestSync2877_BodyAndPropertyDriftAreSeparate`). The API carries it as a
sibling of `sync_drift`, `sync_property_drift { detected_at, keys }`, and
the run report as `property_drift_keys` and `property_drift_detected_at`
beside `drift_detected_at`.

The CHECK mirrors #2851's: synced rows only, both columns together, and at
least one key (an empty array would be "drift on nothing").

### D3 — which properties are compared

Every Page Properties field the sync stores on the row, named as the sync
request names it: `approved_by_name`, `approved_at`, `effective_date`,
`next_review_at`, `owner_role`, `approver_role`,
`acknowledgment_required_roles`, plus the page `title`.

- `version` is not a compared property: a version change is a bump.
- `source_url` is not compared. It is the page's own address, not part of
  the policy staff acknowledged, and a moved page should not need a bump.
  It is also left as the synced row has it; it is not updated in place.
- The page title is compared. It is the policy's title everywhere in the
  product, so renaming the page without a bump is drift like any other
  property change.

The comparison is on the values a synced insert would store from the same
page (`syncPropertyDrift`): dates by calendar day, the audience as a set,
so a page synced unchanged never reports drift.

### D4 — the keys are the current difference, the time is the first sighting

`sync_property_drift_keys` holds the keys that differ now, sorted. A later
run that narrows or widens the difference rewrites the keys and keeps
`sync_property_drift_detected_at`, as body drift keeps its first-seen time
when the page is edited twice. A run that sees the same keys writes
nothing (an `IS DISTINCT FROM` guard).

### D5 — an in-place takeover reports differing properties too

A first sync that takes over an imported policy in place (#2854) keeps the
imported row's properties, as it keeps its body. Page properties that
differ are recorded as property drift on the takeover, and the result's
reason says so. Without this, the takeover would report clean and the
page's next run would report drift on a page nobody edited.

This changed one #2854 test. `TestSync2854_TakeOverSameVersionInPlace`
pushed a page whose dates and audience never matched the imported fixture,
and expected the re-push to be `unchanged`. That only passed because
properties were not compared. The test now pushes the import's properties
(`importedPageProps`), so it still asserts an in-step takeover.
`TestSync2877_TakeOverInPlaceWithDifferentPropertiesIsDrift` covers the
mismatch.

### D6 — no audit row for drift

As for body drift, recording drift writes no `policy_audit_log` row. It is
derived state that the next run rebuilds from the page, and nothing in
force changed. The ticket's `synced_properties_changed` audit action was
for the in-place option, which D1 rejected.
