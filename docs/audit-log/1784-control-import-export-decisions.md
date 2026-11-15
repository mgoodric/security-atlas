# Issue #1784 — Control import: export approved controls as bundles

Part of #1734 (bring-your-own controls). Builds on
`1782-control-import-approval-decisions.md` and
`1783-control-import-approve-clear-decisions.md`.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — The export is the stored manifest, byte for byte

**Decision.** Each bundle's `control.yaml` is the `bundle_manifest_yaml` of
the active version of the control with the approved row's bundle ID. The
export does not rebuild YAML from the control's columns.

**Why.** Re-uploading must be a no-op, not a version bump. The control store
treats an upload as a no-op only when the manifest hash matches the stored
one, and that hash is the sha256 of the manifest bytes. Re-marshalling, even
with the same library, could reorder or reformat the bytes, and the upload
would then update the control in place. The stored manifest was itself built
with `yaml.Marshal` at approval (`bundleForRow`), so the organisation's title
and description were never put into YAML by string-building. It already
declares `scf_anchor_id`, `scf_release` and `external_refs`, so a re-upload
stays pinned to its release and is never re-bound (#1999).

## D2 — Active version by bundle ID, not the control ID the row recorded

**Decision.** The export follows the row's `bundle_id` to the control's
active version (`superseded_by IS NULL`). A row whose control has no active
version is left out.

**Why.** Teams export so they can keep the controls in version control. If
someone edits the control after approval (a description fix, a new version),
the export should match what the platform now holds, or the next upload from
the checkout would undo the edit.

## D3 — Archive shape: gzip-tar, `<bundle_id>/control.yaml`, deterministic

**Decision.** `GET /v1/controls/imports/{id}/export` returns
`application/gzip` with one directory per bundle and only `control.yaml` in
it. Modes and timestamps are fixed, so the same bundles always produce the
same bytes. `X-Bundle-Count` carries the count. A batch with nothing approved
returns an empty archive, not an error.

**Why.** Gzip-tar is what `controls upload` and the control-bundle parser
already handle. A `description.md` is not stored, so there is none to export.

## D4 — Caps and path safety on both ends

**Decision.**

- **Total size cap.** Both the server writing the archive and the CLI reading
  it cap the total manifest bytes at `MaxExportBytes` (32 MiB). Over the cap,
  the API returns 413.
- **Per-manifest cap.** The CLI refuses any manifest over the parser's 1 MiB
  `control.yaml` cap.
- **Entry names.** Every entry name is checked against the bundle ID pattern,
  on write and on read. The pattern excludes `/`, a leading `.` and so `..`.
- **Entry types.** The CLI accepts only regular `<bundle_id>/control.yaml`
  files and their directories. A symlink, a hardlink, any other file and a
  repeated bundle are refused.
- **Writes stay inside `--out`.** Writes go through `os.Root` on the `--out`
  directory, so a symlink already in that directory cannot redirect a write
  outside it.

**Why.** Bundle IDs are validated at upload (#2255), but the archive crosses
a trust boundary, and the CLI should not depend on the server for path
safety.

## D5 — Re-export replaces a bundle's `control.yaml`

**Decision.** If `--out` already holds a bundle directory of the same ID, the
CLI replaces its `control.yaml`. It never touches other files there.

**Why.** Exporting into a checkout to pick up changes is the version-control
workflow the ticket asks for. Refusing to overwrite would force users to
delete the directory first.

## D6 — `controls validate <dir>` validates a directory of bundles

**Decision.** If the directory has no `control.yaml` of its own, `controls
validate` validates every directory directly under it that has one, in name
order. A failure names the bundle that failed. A directory with no bundles
at all is an error.

**Why.** The ticket's check is that `controls validate` passes on the export
directory. Validating one directory per bundle by hand would make that check
a loop the user has to write.

## D7 — Access

**Decision.** The route uses the same router group and middleware as
`GET /v1/controls/imports/{id}`; the route-middleware golden records the same
gating (8). The read is tenant-scoped through RLS, and another tenant's batch
returns 404.

**Why.** The authz policy derives the resource from the path's `controls`
segment and the action from the method, so a GET export has the same role
guard as viewing the batch.
