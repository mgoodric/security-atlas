# Issue #1958 — `external_refs`-only control re-uploads update in place

Build-time calls made fixing
[#1958](https://github.com/mgoodric/security-atlas/issues/1958), raised by the
#1923 (#1737) review. #1908 made a description-only change to `control.yaml`
update the active control in place. Adding or editing `external_refs` still
changed what the comparison saw, so it superseded the control and left owners,
evidence, exceptions and links on the old version. Refs are the field
adopters are most likely to backfill onto controls that already have
evidence.

`detection_tier_actual = manual_review` (the #1923 and #1911 delta reviews) ·
`detection_tier_target = integration` (now covered by
`TestUpload_ExternalRefsOnlyChangeUpdatesInPlace` and
`TestUpload_ConcurrentInPlaceUploadsKeepEveryRevision`).

## D1 — `external_refs` joins `description` as in-place metadata

**Decision.** The comparison in `control.Store.Upload` now removes every key
in `inPlaceManifestKeys` (`description`, `external_refs`) from both decoded
manifests before comparing (`manifestsEqualIgnoringMetadata`, renamed from
`manifestsEqualIgnoringDescription`). The in-place query is renamed
`UpdateControlMetadataInPlace` and also writes `external_refs`, so the stored
refs always match the stored manifest.

**Why refs qualify.** Refs are trace metadata: they are shown in the control
header and matched by search, and nothing in evaluation, scope, or framework
satisfaction reads them. Changing them does not change what the control is or
how it is evaluated, which is the test #1908 applied to prose.

**History.** An in-place refs change writes a `control_manifest_revisions`
row exactly as a description change does, so the prior refs survive
byte-exact in the prior manifest. No new table or migration.

**A refs change together with any other change still supersedes.** The
comparison only ignores the two keys; a `title` or query change alongside a
refs edit is a real version.

## D2 — The upload locks the active row before deciding

**Problem (from the #1911 delta review).** `GetActiveControlByBundleID` took
no row lock. Two concurrent in-place uploads both read v1, both copied v1 into
`control_manifest_revisions`, and the second UPDATE overwrote the first one's
text, which then appeared nowhere.

**Decision.** `Upload` now reads the predecessor with a new
`LockActiveControlByBundleID` query (`… FOR UPDATE`), at the start of the
transaction rather than only on the in-place branch. A second upload of the
same bundle waits on the lock; under READ COMMITTED it then sees the row the
first one committed, so every no-op / in-place / supersede decision is made
against current state, and each in-place update copies the manifest the
previous one wrote. If the first upload superseded the row, the re-check finds
no active row in the second's snapshot and the outcome is the same as before
this change (a unique-index conflict on the insert); that path is unchanged.

**Why lock at the lookup, not just before the revision insert.** Locking
later would leave the no-op and in-place decisions made against an unlocked
read, so the second upload could compare against stale bytes. Locking at the
lookup costs nothing extra: uploads of one bundle are rare and short.

`GetActiveControlByBundleID` is unchanged because `internal/policy/import.go`
also uses it as a plain read.

**Test.** The concurrency test starts eight in-place uploads of one control
at once and asserts that the revision rows plus the active row hold each of
the nine manifests exactly once. With `FOR UPDATE` removed it failed 3 of 3
local runs (six distinct manifests, two seen twice); with it, it passes.
