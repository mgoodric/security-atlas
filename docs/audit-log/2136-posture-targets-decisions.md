# Issue #2136 — Posture targets per framework

An organisation sets a posture target per framework version; the dashboard's
posture tiles colour coverage against it and offer "Set target".

- `detection_tier_actual`: playwright
- `detection_tier_target`: unit

The first Playwright run set 0 / 0 / 0 on a 0.0% tile and expected green; the
shared rule reads a value at critical as red (D3). The spec's fixture now
seeds 50% coverage and walks red, amber and green. A unit case in
`posture-targets.test.ts` would have caught the expectation; one is added.

## D1 — Keyed by framework version, not framework

**Decision.** One row per (tenant, framework version):
`framework_posture_targets` (migration `20261211000000`).

**Why.** The tile, the coverage number and the activated scope are all per
framework version. A target on the framework would have to say which
version's coverage it grades, and a new version is a new requirement set, so
carrying a target over silently could grade it against a number set for the
old one. Re-setting it on a new version is one dialog.

## D2 — "GRC lead" is grc_engineer; admin and grc_engineer set

**Decision.** `PUT` is admitted to admin and grc_engineer, at OPA and again in
the handler (`hasTargetAdmin`, the shape of the risk-taxonomy guard). Every
signed-in role reads.

**Why.** There is no GRC-lead role; the five canonical roles are the universe.
The dispatcher mapped it as for #2118: grc_engineer + admin. The routes live
under `/v1/framework-scopes/`, whose OPA resource (`framework-scopes`) is
already readable by every role and writable only by grc_engineer and admin,
so no rego change is needed. A target is framework-scope configuration in all
but name. The integration test drives viewer, auditor and control_owner
through the full chain and gets 403 for each.

## D3 — All four fields required; thresholds ordered by direction

**Decision.** Target, warning and critical are required, 0..100, at most two
decimals. Order by direction: higher is better needs critical ≤ warning ≤
target; lower is better needs target ≤ warning ≤ critical; closer is better
needs |warning − target| ≤ |critical − target|. The handler checks it, the
table has the same rules as CHECK constraints, and the dialog checks it first.

**Why.** The tile's colour rule (`thresholdBadgeColor`, shared with metric
tiles) reads all four. `metric_targets` allows nulls, which leaves the rule
to guess bands; a posture target is set by a person in one dialog, so
asking for all four costs nothing. Equal bounds are allowed. The shared rule
checks critical first and counts a value AT the critical threshold as
critical, so 90 / 90 / 90 means "above 90 is on target, 90 or below is
critical".

## D4 — A version must have an activated scope (409)

**Decision.** A set on a version with no activated scope for the tenant is
refused with 409; a version the tenant cannot see (another tenant's, or none)
is 404.

**Why.** Without an activated scope the tile shows "Scope not set" and no
percentage (#2114), so there is nothing to grade. 409 says the request is
well-formed but the state is wrong, and the message says what to do.

## D5 — Its own audit table, in the unified log as `posture_target`

**Decision.** `framework_posture_target_audit_log`, append-only (RLS SELECT +
INSERT, and a trigger denying UPDATE and DELETE), one row per set with before
(null the first time) and after, written in the same transaction. The unified
log reads it as kind `posture_target`, target `framework_version`; its payload
is gated on the `framework-scopes` read (`kindSourceResource`).

**Why.** `framework_scope_workflow_log` requires a scope id and a state
transition, which a target set does not have; bending it would make its
`before`/`after` mean two different things. `taxonomy_audit_log` is the
shape copied.

## D6 — Tile colour and copy

**Decision.** With a target the coverage number takes the band's colour and
the line under it names the band: "On target · target 90%", "Warning …",
"Critical …". The 90-day trend is toned by the target's direction
(`deltaTone`). Without a target nothing changes from #2095: neutral, "No
target set". "Set target" (or "Edit target") is a button on the tile, shown
when `/v1/me` says admin or grc_engineer.

**Why.** Colour is never the only cue (the band is written out). The
button sits on the tile because the parent spec puts the action there; a
viewer sees no button rather than one that 403s.
