# coverage honours SoA exclusions — decisions log (#1788)

Second slice of the Statement of Applicability (#1744), after the decision
storage in #1787. Framework coverage now leaves out the requirements that the
caller tenant's activated framework-scope revision excludes for that version.

detection_tier_actual: none
detection_tier_target: integration

No bug surfaced. The new integration tests were run against the code before
the change and failed as expected (coverage 62.5 instead of 66.67, band
`uncovered` instead of `excluded`), then passed with it.

## Decisions

**D1: One filter in `FrameworkPosture`, applied to the requirement set.** The
`version_reqs` CTE drops excluded requirements. Every number the query
derives (coverage, the freshness composite, both halves of the trend) then
works from the same set. The dashboard API, the dashboard export
(`LivePanelSource.Snapshot`) and the MCP `list_framework_posture` tool (a
passthrough to `/v1/frameworks/posture`) all read this one query, so they
can't diverge. The integration test still checks all three.

**D2: The trend uses today's exclusions for both halves.** The 90-day-ago
coverage is computed over the same applicable set as today's. Taking the
exclusions in force 90 days ago would make an exclusion show up as a jump in
the trend, which the ticket rules out. In the fixture, a trend that mixed
the two bases would read 41.67 instead of 33.33.

**D3: Excluded requirements leave the freshness composite too.** The ticket
names coverage % and the trend. The composite counts the controls behind the
version's requirements. Keeping controls behind excluded requirements in it
would grade the tenant on requirements it has declared out of scope. The
filter applies to the composite as well, because it sits on the shared
requirement set.

**D4: "Activated" means `state = 'activated'` now, not an `as_of` read.**
The posture read is a current-state view. At most one revision per (tenant,
version) is activated (partial unique index), and its decisions are frozen
(#1787 D3), so the read is unambiguous. Draft, review and approved
revisions have no effect. Undecided requirements stay in the denominator.

**D5: Per-requirement coverage reports `confidence_band: "excluded"` and an
`exclusion` object.** `GET /v1/requirements/{id}/coverage` sets
`coverage_strength` to 0, `confidence_band` to the new `excluded` value, and
adds `exclusion` {framework_scope_id, justification, implementation_ref,
decided_at}. The field is `null` for applicable and undecided requirements.
`excluded` is not a coverage bucket, so `classifyBand` never returns it. The
web `RequirementConfidenceBand` union and response type gain the value and
the field. Anchors and controls are still returned, so an operator can see
what sits behind an excluded requirement. `/v1/anchors/{id}/requirements`
and `/v1/controls/{id}/coverage` compute no per-requirement coverage, so
they are unchanged.

**D6: Board-pack coverage is unchanged.** It is control-based, as the ticket
says. No board code is touched.
