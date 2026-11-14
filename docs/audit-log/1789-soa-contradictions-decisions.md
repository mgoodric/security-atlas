# SoA contradiction flags — decisions log (#1789)

Third slice of the Statement of Applicability (#1744), after decision storage
(#1787) and coverage exclusions (#1788). An excluded requirement that still
has passing controls behind it is flagged on the decision list.

detection_tier_actual: none
detection_tier_target: integration

No bug surfaced. With the pass condition removed from the contradiction query
(the latest evaluation joined `ON TRUE`), the new integration tests fail
(R-05, whose only control fails, is flagged, and failing C1 doesn't clear
R-01). With the condition restored, they pass.

## Decisions

**D1: One query, `ListFrameworkScopeContradictions`, computed on read.** It
returns the requirement ids that the viewed revision excludes and that reach
an active tenant control whose latest evaluation passes. The domain list
marks them and counts them. Nothing is stored, so the flag follows
evaluations as they arrive, with no recompute step.

**D2: The crosswalk path is `effective_fw_to_scf_edges`.** This view is
already the one place edge eligibility is decided (#1796). It applies the
same `no_relationship` and `rejected` exclusions as `FrameworkPosture`. That
makes tenant overlay edges (#1735) the extension point the ticket asks for:
when #1735 adds its arm to the view, overlay edges count as a path here
without any change to this query. The query comment says so.

**D3: "Active control" means `lifecycle_state = 'active'` and not
superseded.** `FrameworkPosture` uses only `superseded_by IS NULL`, because
it measures coverage by any defined control. A contradiction is a claim that
a working control sits behind an exclusion, so a draft or retired control
does not count.

**D4: "Latest evaluation" is per control, across scope cells.** This is how
`FrameworkPosture` reads it, and the SoA is per framework version, not per
scope cell. Ties on `evaluated_at` break on `id` so the result is stable.

**D5: Filters are `?decision=applicable|excluded|undecided` and
`?contradiction=true|false`, on both list GETs.** Counts always describe the
whole revision, so a filtered view still shows how much of the SoA is left.
An unknown value is a 400. The filter is a pure function on `Applicability`
with its own unit test.

**D6: `as_of` reads flag against current evaluations.** The decisions are
those in force at `as_of` (frozen on activation), but the flag answers "would
this exclusion be contradicted now". Replaying evaluations as of a past date
is not in the ticket. The export ticket can revisit this if auditors need
point-in-time flags.
