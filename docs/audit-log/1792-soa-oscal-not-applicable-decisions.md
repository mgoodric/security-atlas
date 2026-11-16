# OSCAL SSP marks excluded requirements not-applicable — decisions log (#1792)

Sixth slice of the Statement of Applicability (#1744), after decision storage
(#1787), coverage exclusions (#1788), contradiction flags (#1789), bulk set and
CSV import (#1790) and the CSV/xlsx export (#1791). The audit-period OSCAL SSP
now carries the SoA in machine-readable form.

detection_tier_actual: none
detection_tier_target: none

No bug surfaced. Two of the spec's literal forms are not valid OSCAL; D1 and D2
record what was done instead.

## Decisions

**D1: The namespaced control-id joins with `_`, not `:`.** The spec's example
is `iso27001-2022:A.8.25`. OSCAL `control-id` is a token (NCName-shaped), so
trestle rejects the colon and the SSP would fail its round-trip validation.
The bridge emits `iso27001-2022_A.8.25` instead. The namespace is the framework
slug and version joined with `-`, as in the #1791 export filename. Case is
kept, since requirement codes are case-significant. Any other character
outside the token alphabet becomes `-`. The raw code also travels as a
`requirement-code` prop, so a reader never has to parse the control-id.

**D2: `atlas:framework` is a `framework` prop in the atlas namespace.** A prop
name is also a token, so `atlas:framework` is rejected as written. Every atlas
prop in the SSP is already written as `name` plus
`ns="https://security-atlas.dev/ns/oscal"`, which is how OSCAL namespaces a prop.
The `framework` prop uses that form, with the namespace as its value.

**D3: `implementation-status` is the OSCAL core prop.** It is written as
`{"name": "implementation-status", "value": "not-applicable"}` with no `ns`
(the OSCAL namespace), so that OSCAL tooling recognises it. The justification is
the implemented-requirement's `remarks`. These entries have no
`evaluation-result` and no statements. An exclusion is a scope decision, not an
implementation the platform evaluated.

**D4: The revision is the one in force at `frozen_at`, chosen like `as_of`.**
The new `ListExcludedRequirementsAsOfForExport` query picks the revision with
the predicate `GetFrameworkScopeAsOf` uses (`effective_from <= frozen_at`, not
superseded by then, latest `effective_from`). It does not take the
currently-activated revision: an export must reflect the freeze (invariant
#10), and decisions on activated or superseded revisions are immutable
(#1787 D3). So the rows read are exactly the ones in force then. A requirement
excluded only on a later revision is not in the SSP. It is scoped to the audit
period's own framework version.

**D5: The data travels in a new proto field.** `SspInput.excluded_requirements`
(field 10, new `ExcludedRequirement` message) sits apart from
`control_implementations`, following the slice-619 rule that a non-control
entry never shares the control field. It is an additive proto3 field: older
bridges ignore it, and no existing field number changed.

**D6: No migration.** All the data already exists (#1787). The query is
read-only and runs in the export's existing transaction under tenant RLS.
