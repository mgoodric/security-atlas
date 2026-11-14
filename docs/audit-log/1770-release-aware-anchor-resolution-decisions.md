# Issue #1770 — Release-aware anchor resolution (expand step)

Part of #1718 (release-aware SCF anchors). The SCF reuses codes between
releases, so a bare code such as `IAC-06` is ambiguous once two releases are
loaded. This step adds one resolver, `internal/scfrelease`, and threads an
optional release through bundle upload and the anchor reads. Nothing that
works today stops working.

- `detection_tier_actual`: integration
- `detection_tier_target`: unit

The bug found during the work: resolving an anchor UUID first required its
version to be listed under the `scf` framework slug. ucfcoverage's integration
tests seed anchors under synthetic frameworks, and two of them went 404. A
unit case on the resolver now covers it (`TestResolve_UUIDOutsideSCFSlug`).

## D1 — Requirement coverage stays unpinned without `?scf_release`

**Decision.** On `GET /v1/requirements/{id}/coverage`:

- `?scf_release=X` pins the traversal to release X's anchors and echoes `X`.
- An unknown X is a 404 naming it.
- With no parameter the traversal stays unpinned, across every release, as
  before #1770, and the response echoes `scf_release: null`.

The anchor reads (`GET /v1/anchors/{id}`, `/v1/anchors/{id}/requirements`) do
default to the current release, as the ticket says.

**Why.** The ticket asks for both "default to the current release" and
"nothing breaks yet". Coverage is where the two collide. Crosswalk edges point
at the anchors that were current when the crosswalk was imported. A new SCF
release import makes different anchor rows current. Until #1971 re-binds the
edges, a current-release default would show every requirement with no anchors
and no controls. An existing test, `TestCoverage_RejectedEdgeEarnsNoCredit`,
failed for exactly this reason. Echoing `null` rather than the current label
keeps the response honest about what it read.

The dispatcher made this call on the PR's behalf. The current-release default
is the contract step: #2203, blocked by #1971.

## D2 — Errors: distinct with a release, unchanged without

**Decision.** `scfrelease.Resolve` has two named failures:

- `ErrUnknownRelease`: no SCF release carries the label.
- `ErrCodeNotInRelease`: the release exists but lacks the code, or the
  anchor UUID given belongs to another release.

Bundle upload surfaces them as `control.ErrSCFReleaseUnknown` and
`control.ErrSCFCodeNotInRelease`, and the HTTP layer returns 404 with the
message. A bundle with no `scf_release` that misses still gets the pre-#1770
`ErrSCFAnchorUnknown`, so existing callers and tests see no change.

**Why 404 for both.** It matches the existing unknown-anchor mapping ("the
thing you referenced isn't there"). The messages, not the status, tell the
two apart: each names the release, and the code error names the code too.

## D3 — A UUID with no release resolves in its own release

**Decision.** `scf_anchor_id` given as a UUID, with no `scf_release`, binds
that exact anchor in whichever release holds it. With a release named, the
UUID must belong to that release.

**Why.** Machine-written bundles use UUIDs, and a UUID is already
unambiguous. Forcing it through the current release would re-bind or refuse
bundles that work today.

## D4 — `controls validate` reads catalog files, not the platform

**Decision.** `atlas-cli controls validate` always prints the declared
`scf_release`, or a deprecation warning when there is none. Given
`--scf-catalog <file>` (repeatable; the same JSON `catalog import-scf` loads),
it also flags the bundle's code when its meaning changed in a later release.
"Later" means by `release_date`, then by flag order.

The classes come from `scfrelease.Classify`, which reads the later release's
legacy SCF IDs (#1769):

| Class        | Meaning                                    |
| ------------ | ------------------------------------------ |
| `renamed`    | same code and lineage, new title           |
| `split`      | two or more later codes descend from it    |
| `reused`     | the code now descends from another control |
| `renumbered` | the code is gone, with one successor       |
| `withdrawn`  | the code is gone, with no successor        |

Flags are warnings: validation still exits 0.

**Why.** `validate` is documented as local-only (no network, no auth), and
CI runs it on community bundles that have no platform to ask. The catalog
files are exactly the releases an operator loads. Failing on a flag would
turn every SCF release into a red CI run for bundles nobody has migrated yet.
Migrating them is the contract step's job.

## D5 — Integration fixtures do not wipe the global catalog

**Decision.** `scfreleasetest.LoadAB` demotes whatever SCF release is current
and imports releases A and B through the real importer. Its cleanup deletes
only those two releases and the controls bound to them, then restores the
previous current release and `latest_version_id`.

**Why.** A first version wiped the catalog the way scfimport's tests do. In
shard order, another package leaves `framework_scopes` rows on the shared
versions, and the wipe failed on their foreign key. Touching only rows the
helper created keeps the tests order-independent.
