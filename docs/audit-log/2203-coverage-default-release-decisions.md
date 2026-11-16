# Issue #2203 — Requirement coverage pins to the current SCF release by default

The contract step of #1770. Before this, `GET /v1/requirements/{id}/coverage`
pinned anchors only when `?scf_release=` was given. With no parameter it walked
every release's anchors and echoed `scf_release: null` (#1770 D1). It now pins
to the current SCF release and echoes that release's label.

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

Two tests encoded the old contract and failed on the switch. Both were updated
to the new one rather than treated as bugs:

- `TestCoverage_RejectedEdgeEarnsNoCredit` seeded its anchor under a synthetic
  framework. It now seeds the anchor in the current SCF release.
- `TestRequirementCoverage_SCFRelease` expected null and both releases'
  anchors. It now expects the current release and one anchor.

## D1 — Default to the current release and accept the drop for legacy-pinned edges

**Decision (option a).** With no `?scf_release`, requirement coverage pins to
the current SCF release. When no release is current (an empty catalog), the
read stays unpinned and echoes null rather than failing.

**Measured, not assumed.** `TestRequirementCoverage_DefaultPinAcrossReleaseUpgrade`
runs the upgrade end to end against real Postgres:

1. Import a crosswalk in release A.
2. Import release B.
3. Run `RebindControls`.
4. Re-import the crosswalk.

R-SAME maps to IAC-06, which keeps its title in B, so its control is re-bound.
R-RENAMED maps to GOV-01, which is retitled in B, so its control is flagged
and stays on A.

| After the upgrade                                       | Unpinned (old default) | Pinned to current (new default) |
| ------------------------------------------------------- | ---------------------- | ------------------------------- |
| Crosswalk re-imported with no `scf_release` (boot path) | R-SAME                 | R-SAME                          |
| Crosswalk that declares `scf_release: A` on purpose     | R-RENAMED              | none                            |

Before the upgrade, both requirements were covered.

**Why it is safe on the boot path.** Every bundled crosswalk declares no
`scf_release`, so `bootstrap.sh` re-imports it in the current release. That
moves its edges to B's anchors and retires the A edges (#1971). The flagged
control loses its credit under both defaults: its edge moved and the control
did not. The loss comes from the re-bind, not the pin. It is surfaced through
needs-reanchoring (#2285).

**What the pin costs.** Edges left on a legacy release on purpose drop out of
the default read:

- edges from a crosswalk that declares an old `scf_release`;
- a tenant overlay edge to a legacy anchor.

`?scf_release=<old>` still reads them. The old unpinned read was already
half-broken for these crosswalks. R-SAME lost its credit there because its
control was re-bound to B and its edge stayed on A.

**Why not (b), falling back to the edge's own release for flagged anchors.**
It would bring back the mix of releases in one read that the pin exists to
remove. It also does not rescue R-SAME in the legacy-pinned case: that
control moved to B, so falling back to A misses it too.

**Why not (c), keeping the unpinned default until remap proposals land
(#1775).** The measured boot-path numbers do not depend on #1775. Waiting
would only defer the release echo that callers need, with no change in
coverage.

Matt may override this before merge. The dispatcher took it as a routine call
on the measured recommendation.
