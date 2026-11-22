# 2902 — Policy sync: refuse an empty push: decisions log

Issue: mgoodric/security-atlas#2902, a follow-up from #2853 (PR #2895).
`atlas-confluence run` already refuses to push an empty labelled set on the
connector side (#2947); this is the platform-side guard in
`POST /v1/policies:sync`, so every pusher is covered.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — `allow_empty: true` opt-in, not a `complete: true` marker on every push

The ticket offered two shapes. `allow_empty` was picked:

- It changes nothing for a push that carries pages, so every existing pusher
  (the Confluence connector, scripts written against #2850) keeps working.
  A `complete: true` marker required on every push would turn each of them
  into a 400 until they were updated.
- The failure it guards against is specific: nothing arrived. A non-empty
  push already carries its own evidence that the connector read the space.
- An operator who really emptied a space states it once, on that run.

### D2 — "empty" means no page at all, valid or invalid

A push whose pages are all skipped for bad properties still carried those
pages, so they count as present (#2853 D3) and nothing is marked missing for
them. Only `pages: []` (or omitted) is refused.

### D3 — refused only when the source has published synced policies

An empty push for a source with nothing synced marks nothing, so it is
accepted and returns an empty report. The count is read after
`LockPolicySyncSource`, so a concurrent run cannot change it between the
check and the writes. The refusal returns before any write, and the
transaction rolls back.

### D4 — 422, not 400

The request is well formed; it is refused because of the source's current
state. The message names `allow_empty`.

### D5 — the warning is "more than half missing after the run"

The denominator is the source's published synced policies at the start of
the run. The numerator is every policy the run reports missing, including one
an earlier run already marked, because that is what the report lists. Exactly
half does not warn. The warning goes in a top-level `warnings` array on the
report, omitted when empty, and writes nothing.
