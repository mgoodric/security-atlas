# Slice 654 — Validate schema `x-default-scf-anchors` against the bundled catalog — decisions log

JUDGMENT slice. Claude made the subjective calls below (the remap target per
dangling anchor + the guard's catalog source + the no-semver-bump call), recorded
them here, and shipped when CI was green (per the JUDGMENT-slice process; this does
NOT touch the product's runtime AI-assist boundary).

- detection_tier_actual: unit
- detection_tier_target: unit

(The dangling-anchor defects were caught by the new AC-1 unit guard added in this
slice — the cheapest tier that could catch them, and the tier that _should_ have
caught them all along. They had been latent on `main` because no tier asserted the
invariant. `target == actual == unit` is the correct, non-gap outcome: the guard is
now the permanent net.)

## Scope note — the guard found FAR more than the spec's 4 references

The slice spec named 3 schemas / 4 dangling references (`MON-02`, `IRO-02` ×2,
`MON-02` again). The AC-1 guard — run as the authoritative scan per the
spillover-as-slice directive ("let your own guard enumerate the full set… fix every
flagged schema… they're the same class") — surfaced **18 schemas** carrying
**12 distinct dangling anchor codes**. All 18 are the identical class of bug (an
`x-default-scf-anchors` hint pointing at an anchor absent from the bundled SCF
catalog fixture), so all 18 are fixed in this PR. No spillover slice was filed —
nothing of a _different_ out-of-scope class surfaced.

Full dangling set (anchor → schema(s) it appeared in):

| Dangling anchor | Appeared in                                                                                                                                                                                     |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `IAC-10`        | `1password.org_policy`                                                                                                                                                                          |
| `IAC-17`        | `access_review.completion`                                                                                                                                                                      |
| `IAC-18`        | `access_review.completion`                                                                                                                                                                      |
| `DCH-06`        | `aws.s3.bucket_encryption_state`                                                                                                                                                                |
| `IAC-22`        | `azure.entra_role_assignment`, `github.scim_user`, `grafana.access_config`, `hris.manager_hierarchy`, `hris.worker_lifecycle`, `k8s.rbac_binding`, `okta.app_assignment`, `okta.user_lifecycle` |
| `IAC-09`        | `github.scim_user`, `hris.manager_hierarchy`, `hris.worker_lifecycle`, `okta.user_lifecycle`                                                                                                    |
| `MON-02`        | `github.audit_event`, `pagerduty.response_metrics`                                                                                                                                              |
| `TDA-06`        | `github.repo_protection`                                                                                                                                                                        |
| `IRO-02`        | `pagerduty.incident_summary`, `pagerduty.response_metrics`                                                                                                                                      |
| `IRO-07`        | `pagerduty.oncall_coverage`                                                                                                                                                                     |
| `IRO-13`        | `pagerduty.postmortem_summary`                                                                                                                                                                  |
| `TDA-09`        | `sast.scan_result`                                                                                                                                                                              |

## Decisions made

### D1 — Guard surface + helper shape. (confidence: high)

The guard is a fast Go unit test (`internal/api/schemaregistry/default_anchor_catalog_test.go`,
no Postgres, no build tag — the pure-Go-pre-DB convention). It reads the schema set
via the same embedded-FS loader the atlas server boots with
(`LoadPlatformSchemas(PlatformSchemasFS())`) — not a hand-rolled parse — so it
tracks the real registration path, mirroring the slice-068 drift guard
(`internal/control/evidence_kind_drift_test.go`). The catalog-existence check is
factored into a small testable helper `anchorExistsInCatalog(anchor, catalog) bool`
plus `danglingAnchors([]string, catalog) []string`; the negative sub-test feeds a
deliberately-dangling `ZZZ-99` (plus the historical real-but-absent `MON-02` /
`IRO-02`) and asserts they are rejected, so the positive test can never pass
vacuously. The bundled catalog is parsed through the canonical `scfimport.Load`
parser (same shape the seed/import path consumes), located via a
walk-up-to-`go.mod` helper copied from the slice-068 guard.

### D2 — Remap targets per dangling anchor (the central JUDGMENT). (confidence: medium-high)

Every dangling anchor is remapped to a present, semantically-closest catalog
anchor, OR dropped when a co-listed anchor already present in the catalog covers
the same concept. Every remap target was grep-verified present in
`migrations/fixtures/scf-sample.json`. Results de-duplicated; every resulting list
is non-empty.

| Schema                           | Was                       | Now                | Rationale                                                                                                   |
| -------------------------------- | ------------------------- | ------------------ | ----------------------------------------------------------------------------------------------------------- |
| `1password.org_policy`           | `[IAC-10]`                | `[IAC-01]`         | IAC-10 (account mgmt) absent; a password-manager org-policy IS the I&A policy posture → IAC-01 (I&A Policy) |
| `access_review.completion`       | `[IAC-17, IAC-18]`        | `[IAC-15]`         | IAC-17/18 absent; the access-review concept is exactly IAC-15 (Account Review), present                     |
| `aws.s3.bucket_encryption_state` | `[CRY-04, DCH-06]`        | `[CRY-04]`         | DCH-06 absent; CRY-04 (Encryption At Rest, present + co-listed) is the correct anchor → drop DCH-06         |
| `azure.entra_role_assignment`    | `[IAC-21, IAC-22]`        | `[IAC-21]`         | IAC-22 absent; IAC-21 (Privileged Account Management, present + co-listed) covers role assignment → drop    |
| `github.audit_event`             | `[MON-01, MON-02]`        | `[MON-01]`         | MON-02 absent; MON-01 (Continuous Monitoring, present + co-listed) stays → drop MON-02 (spec-directed)      |
| `github.repo_protection`         | `[TDA-06, CHG-02]`        | `[CHG-02]`         | TDA-06 absent; branch-protection IS change control → CHG-02 (Change Control, present + co-listed); drop     |
| `github.scim_user`               | `[IAC-22, IAC-09]`        | `[IAC-07]`         | both absent; SCIM provisioning IS user lifecycle → IAC-07 (User Provisioning & Lifecycle, present)          |
| `grafana.access_config`          | `[IAC-06, IAC-22]`        | `[IAC-06]`         | IAC-22 absent; IAC-06 (MFA/Authenticator Mgmt, present + co-listed) stays → drop IAC-22                     |
| `hris.manager_hierarchy`         | `[IAC-22, IAC-09]`        | `[IAC-07]`         | both absent; HRIS-derived account hierarchy feeds provisioning/lifecycle → IAC-07 (present)                 |
| `hris.worker_lifecycle`          | `[IAC-22, IAC-09, HRS-04] | `[IAC-07, HRS-04]` | IAC-22/09 absent → IAC-07 (lifecycle, present); HRS-04 (Security Awareness Training, present) stays         |
| `k8s.rbac_binding`               | `[IAC-21, IAC-22]`        | `[IAC-21]`         | IAC-22 absent; IAC-21 (Privileged Account Management, present + co-listed) covers RBAC → drop               |
| `okta.app_assignment`            | `[IAC-21, IAC-22]`        | `[IAC-21]`         | IAC-22 absent; IAC-21 (present + co-listed) covers app entitlement → drop                                   |
| `okta.user_lifecycle`            | `[IAC-22, IAC-09, HRS-04] | `[IAC-07, HRS-04]` | IAC-22/09 absent → IAC-07 (lifecycle, present); HRS-04 (present) stays                                      |
| `pagerduty.incident_summary`     | `[IRO-02, IRO-09]`        | `[IRO-09]`         | IRO-02 absent; IRO-09 (Incident Reporting, present + co-listed) stays → drop (spec-directed; 636/535 prec.) |
| `pagerduty.oncall_coverage`      | `[IRO-04, IRO-07]`        | `[IRO-04]`         | IRO-07 absent; staffed on-call proves the IR plan is operational → IRO-04 (Incident Response Plan, present) |
| `pagerduty.postmortem_summary`   | `[IRO-13, IRO-09]`        | `[IRO-09]`         | IRO-13 (root-cause) absent; IRO-09 (Incident Reporting, present + co-listed) is the closest present anchor  |
| `pagerduty.response_metrics`     | `[IRO-02, MON-02]`        | `[IRO-09, MON-01]` | both absent; IRO-02→IRO-09 (present), MON-02→MON-01 (present) — spec-directed remap                         |
| `sast.scan_result`               | `[VPM-04, TDA-09]`        | `[VPM-04, TDA-01]` | TDA-09 absent; SAST is secure-dev → TDA-01 (Technology Development & Acquisition, present); VPM-04 stays    |

Companion prose: five schema `description` fields cited a now-removed anchor by
name (e.g. "SCF IRO-02 Incident Handling"). Those prose citations were updated to
name the present remap target and note the fixture-absence parenthetically — the
same pattern slice 535 already used for `monitoring.alert_firing` (whose description
already documents "IRO-02 is absent from the bundled SCF catalog fixture, so IRO-09…
is used"). This keeps each schema internally consistent: the description no longer
advertises an anchor the deployment's bundled catalog lacks. No evidence-record
DATA field (`properties`, `required`, `type`, `additionalProperties`) was touched —
only the `x-default-scf-anchors` hint and the prose that documents it (anti-criterion
P0 honored).

### D3 — Guard's catalog source: the bundled seed fixture, NOT a fuller catalog (AC-3). (confidence: high)

The guard validates against `migrations/fixtures/scf-sample.json` — the
bundled seed/test catalog (62 anchors). This is the catalog a fresh deploy and the
test suite actually carry, so it is the correct existence reference: a hint that
resolves against a _hypothetical_ fuller catalog but not against the _shipped_ one
is exactly the silent coverage-suggestion gap this slice closes. The alternative
long-term fix — EXPANDING the bundled sample to carry IAC-09/IAC-22/IRO-02/MON-02/
etc. — is a separate **catalog-governance** call (it changes what every deployment
seeds, touches `scfimport` invariants, and needs the SCF-redistribution legal review
that is still an open question). It is deliberately OUT of scope here and was NOT
done (anti-criterion P0: "does NOT expand the SCF catalog fixture as a side effect").
Recorded for the maintainer's revisit list.

### D4 — No semver bump (AC-4). (confidence: high)

The schemas stay at `1.0.0`. Confirmed via `service.go`
`ImportPlatformSchemas`: the global-row dedup key is `(kind, semver)` only
(`GetEvidenceKindSchemaGlobal` by `Kind` + `Semver`); `default_scf_anchors` is a
stored COLUMN, not part of the schema's immutable identity, and is never hashed into
a version key. No evidence record consumes the hint (it is an author-time mapping
DEFAULT an operator approves once, not record data). Therefore changing the hint
list on an existing `1.0.0` is not an incompatible schema change and requires no
semver bump. On a fresh deploy the corrected anchors are simply what gets inserted;
an already-seeded deployment keeps its stored row until re-seeded (idempotent import
skips existing `(kind, semver)`), which is acceptable because the hint is advisory
and re-checked by the maintainer at approval time.

### D5 — OQ #16/#17 consistency: this guard is EXISTENCE, the maintainer review is ACCURACY. (confidence: high)

OQ #16/#17 (resolved 2026-05-20, `Plans/canvas/11-open-questions.md`) makes the
maintainer's manual review "specifically scrutinize `x-default-scf-anchors`
ACCURACY (the load-bearing manual checkpoint)." This guard does NOT replace that and
does NOT weaken it. The two are complementary and orthogonal:

- **This guard (mechanical):** does each anchor EXIST in the bundled catalog? A pure
  set-membership check — catches the dangling / non-existent class.
- **Maintainer review (semantic):** is this the RIGHT anchor for this evidence kind?
  A judgment the guard cannot make.

The guard catches precisely the class the manual checkpoint demonstrably missed —
18 schemas shipped on `main` with dangling anchors despite the manual-review
checkpoint existing. Existence is mechanizable; accuracy is not. The manual
ACCURACY checkpoint remains the load-bearing review; this guard is the cheap
mechanical floor beneath it.

## Revisit once in use

- **D2 remap accuracy (medium-high confidence — top of the list).** Each remap is a
  best-reasoned semantic-closest call against the 62-anchor sample fixture, not the
  full ~1,400-anchor SCF catalog. Once a deployment imports the real SCF catalog,
  the maintainer should re-evaluate whether the _originally-intended_ anchors
  (IAC-09 User Account Management, IAC-22 Least Privilege, IRO-02 Incident Handling,
  IRO-07 Incident Response Team, IRO-13 Root-Cause Analysis, IAC-17/18 access-review
  variants, DCH-06, TDA-06/09) are the better hint — at which point AC-3's
  catalog-expansion option becomes the cleaner fix and several of the "drop" /
  "collapse to IAC-07" decisions may be re-expanded.
- **D3 catalog-expansion option.** If/when the bundled sample is expanded (or the
  full SCF catalog becomes the seed), revisit whether the guard should validate
  against the fuller catalog and whether the dropped anchors should be restored.
- **`access_review.completion` → IAC-15.** IAC-15 (Account Review) is the closest
  present anchor, but the original IAC-17/IAC-18 pair suggests the author wanted a
  finer access-recertification distinction; re-check once those anchors exist.
- **`1password.org_policy` → IAC-01.** A password-manager org policy could map to a
  more specific credential-management anchor (e.g. IAC-10 family) once present.

## Confidence summary

| Decision | Confidence  |
| -------- | ----------- |
| D1       | high        |
| D2       | medium-high |
| D3       | high        |
| D4       | high        |
| D5       | high        |
