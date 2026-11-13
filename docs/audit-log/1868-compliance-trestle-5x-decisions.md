# Issue #1868 — compliance-trestle 4.0.x → 5.2.1 in the OSCAL bridge

Build-time calls made while assessing Dependabot
[#1679](https://github.com/mgoodric/security-atlas/pull/1679), which widened
`compliance-trestle` in `oscal-bridge/pyproject.toml` from `<4.3.0` to
`<5.3.0`. Part of the [#1767](https://github.com/mgoodric/security-atlas/issues/1767)
dependency sweep.

`detection_tier_actual = unit` (the bridge's pytest suite caught both breaks
below on the first run against 5.2.1) · `detection_tier_target = unit`.

## What changed upstream (4.0.3 → 5.2.1)

| Release | Relevant to the bridge                                                                                                    |
| ------- | ------------------------------------------------------------------------------------------------------------------------- |
| 4.1.0   | SSP-generate fix only. Nothing the bridge calls.                                                                          |
| 4.2.0   | No functional notes.                                                                                                      |
| 5.0.0   | "Pydantic v2 mode": the generated OSCAL models become native pydantic v2. Scalar datatypes become `RootModel`s (`root=`). |
| 5.1.0   | Minimum pydantic 2.11; adds Python 3.13/3.14.                                                                             |
| 5.2.0   | Pydantic v1 → v2 follow-up fixes; strict empty-list handling.                                                             |
| 5.2.1   | Pins pydantic to 2.14.3 exactly; fixes a path traversal in trestle's own import output path (not a path the bridge uses). |

No release in the range touches `ProfileResolver`'s signature, the fetcher
cache, or `trestle://` href handling. The bridge's sandbox tests
(`test_import_profile.py`: external href rejected without a fetch, unknown href
is a structured error, cycle and depth caps, chained profiles) all pass
unchanged on 5.2.1.

`trestle.oscal.OSCAL_VERSION` is `1.2.1` on both 4.0.3 and 5.2.1, so the
model set the bridge validates against did not move. The bridge keeps stamping
the `1.1.2` it commits to (canvas §3.4).

## D1 — Take 5.2.1 rather than hold `<4.3.0`

**Decision.** Move to `compliance-trestle>=5.2.1,<5.3.0`.

**Why.** The port is three lookups and one unwrapping helper, all covered by
the existing suite plus three new assertions. Holding 4.x would leave the
bridge on the last pydantic-v1-style trestle line while upstream fixes land
only on 5.x (including 5.2.1's path-traversal fix). The floor is 5.2.1, the
version the suite ran against, not 5.0.0: nothing below 5.2.1 was tested.

## D2 — `model_fields[...].annotation` replaces `__fields__[...].type_`

**Decision.** The three module-level enum lookups in `serializer.py` read the
pydantic v2 `FieldInfo.annotation`.

**Why.** `FieldInfo` has no `type_`, so 4.x-style lookups raise
`AttributeError` at import time and every test errors at collection. The
annotation resolves to the same enum classes
(`OperationalStateValidValues`, `SystemComponentOperationalStateValidValues`),
so the slice-030 reason for resolving them through the field, not by name,
still holds. No 4.x/5.x dual path: the pin excludes 4.x.

## D3 — One `_scalar_str` helper for RootModel scalars

**Decision.** `_scalar_str` unwraps `.root` before `str()`, and is used at the
four places the bridge reads a scalar off a parsed model: `oscal_version` in
catalog, profile and component-definition import, and a component's `type`.

**Why.** In 5.x, `metadata.oscal_version` is an `OscalVersion` RootModel and a
component's `type` is a `StringDatatype` RootModel. `str()` on either gives the
model repr (`root=StringDatatype(root='1.1.2')`), which would have reached the
Go side as the stored OSCAL version and component type. The other scalars the
bridge reads (control `id`/`title`, part `prose`, requirement `control_id`,
`uuid`, `description`) are still plain `str` on 5.2.1 (checked against the
fixtures), so they are left alone.

## D4 — The resolved profile's `oscal_version` is asserted by shape, not value

**Decision.** The new profile-import assertion checks that `oscal_version` is a
plain `N.N.N` string, not that it equals `1.1.2`.

**Why.** `ProfileResolver` stamps the resolved catalog with trestle's own
`OSCAL_VERSION` (`1.2.1`), not the profile's declared `1.1.2`. That is the
same on 4.0.3 (verified by running main's serializer under 4.0.3), so it is
existing behaviour, not something this upgrade changed, and changing what the
bridge reports there is out of scope for a dependency bump. Pinning `1.2.1`
in a test would freeze that behaviour; the shape check catches the RootModel
repr regression without doing so.

## Not verified here

The Go-side bridge integration tests (`internal/oscal/**/integration_test.go`)
start the real bridge but need Postgres, and skip in CI because
`OSCAL_BRIDGE_PYTHON` is not set there. They were not run for this change; the
bridge's own pytest suite (unit + in-process gRPC) is the coverage.

New transitive dependencies from trestle 5.x: `rfc8785`, `securesystemslib`.

## Follow-ups landed in #1875

**D4 superseded.** The profile-import path now reports the entry profile's
declared `metadata.oscal-version`, not the resolver's stamp, and both the
bridge test and the Go `profileimport` integration test assert `1.1.2`.

**`_scalar_str` unwraps `Enum.value`.** `DefinedComponent.type` is
`StringDatatype | DefinedComponentTypeValidValues`. Parsed JSON lands on the
RootModel, but a model built in code holds the plain Enum member, whose
`str()` is `DefinedComponentTypeValidValues.software`.

**The Go bridge tests now run in CI.** The "Not verified here" gap above is
closed: integration legs B3 and B5 sync the bridge env, set
`OSCAL_BRIDGE_PYTHON`, and set `ATLAS_REQUIRE_OSCAL_BRIDGE=1`, which turns
the bridge helper's skip into a failure (`internal/oscal/bridgetest`). The
bridge CI job also runs its pytest suite on Python 3.12.

**trestle 5.2.1 hard-pins its runtime dependencies.** Every runtime
`Requires-Dist` in the 5.2.1 wheel is an exact `==` pin (`cryptography==50.0.2`,
`requests==2.34.2`, `pydantic[email]==2.14.3`, `paramiko==5.1.0`, and the
rest; read from the installed wheel's `METADATA`). A Dependabot bump of any of
them in the bridge environment cannot resolve against trestle until trestle
publishes a release that moves the pin. Close such PRs as blocked upstream,
rather than trying to override the resolver.
