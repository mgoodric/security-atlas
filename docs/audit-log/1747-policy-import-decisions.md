# #1747 — policy library import with historical lifecycle — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1747
**Shape:** `atlas-cli policy import <dir>` → admin-only
`POST /v1/admin/policies/import` → `policy.Store.Import`. Each Markdown file
with YAML front matter becomes one `published` policy with
`source_attribution = imported`, its historical approver and date, effective
date, next review date and source URL, plus one `policy_audit_log` row.

## Defects found on the way

### The second-and-later publish path could never succeed

`Store.Publish` for an approved row with `predecessor_id` set inserted a
**new** row with the same `predecessor_id`. The partial UNIQUE index
`policies_predecessor_unique_when_set` rejects that second successor, so the
path always failed. Nothing created a draft with a predecessor, so nothing
reached it. The slice-023 test helper `forkAndPublishV2` already worked around
it with direct SQL and a comment naming the drift. Acceptance criterion 6
(the next version of an imported policy publishes through the normal
workflow) needs this path, so it is fixed here:

- `POST /v1/policies` accepts `predecessor_id`. The predecessor must be
  `published` (409 otherwise). A predecessor with a successor already is a
  409 (`policies_predecessor_unique_when_set`), and an unknown one is a 400.
- Publish of such a row supersedes the predecessor and publishes the row
  **in place** (`PublishApprovedPolicy`), keeping the chain linear. The
  unused `InsertPublishedPolicy` query is removed.
- The new version must differ from the predecessor's (`ErrInvalidVersion`).
  The published version is the version the draft was created with.
  `new_version` stays required on the wire, as before.

- `detection_tier_actual`: `manual_review` (found reading the store for this
  ticket; the slice-023 comment had recorded it as known drift).
- `detection_tier_target`: `integration`. Covered now by
  `TestImport_NextVersionUsesNormalWorkflow` and
  `TestImportHTTP_NextVersionThroughWorkflow`.

### The policies CSV export queried columns that no longer exist

`GET /v1/admin/policies/export` selected `owner`, `approver`,
`acknowledgment_required_role` and an integer `version`. The slice-022
migration dropped or renamed all four. The unit tests inject a fake source,
so the real query was never run. The query now reads `owner_role`,
`approver_role`, `acknowledgment_required_roles` and a text `version`. The CSV
header names are unchanged, so existing consumers keep their columns, and the
seven import columns are appended.

- `detection_tier_actual`: `manual_review`.
- `detection_tier_target`: `integration`. Covered now by
  `TestImportHTTP_AdminImportsTwoFiles`, which reads the real CSV.

## Decisions

### D1 — the import endpoint lives at `/v1/admin/policies/import`. **Confidence: HIGH.**

It sits beside `/v1/admin/policies/export` and takes the `adminBearer` tier
in the OpenAPI inventory. The handler checks `cred.IsAdmin` before reading
the body, and the authz layer denies non-admins on `/v1/admin/` as well.

### D2 — the server parses the files; the CLI only reads them. **Confidence: HIGH.**

The request body is `{files: [{name, content}], partial}`. The front-matter
parser (`internal/policy/importfile`) runs server-side, so the API and the CLI
validate identically and the API is usable without the CLI. Limits: 16 MiB
per request, 1,000 files.

### D3 — unknown front-matter keys fail the file. **Confidence: MEDIUM.**

`yaml.v3` `KnownFields(true)`. A misspelt `aproved_at` would otherwise be
dropped silently and then reported as "missing approved_at". Failing on the
typo names it.

### D4 — missing `owner_role` / `approver_role` default to `unassigned`. **Confidence: MEDIUM.**

Both columns are NOT NULL and non-empty. The ticket makes them optional on
import, and an existing library often has no role mapping in the tool yet.
`unassigned` is literal and visible; inventing a plausible role would not be.

### D5 — `linked_controls` accepts a control UUID or a bundle ID. **Confidence: MEDIUM.**

The ticket says "control bundle IDs or organisation control IDs". A value
that parses as a UUID is looked up as `controls.id`; anything else as the
active version of `controls.bundle_id`. An unresolved reference fails the
file. Two references to the same control collapse to one link.

### D6 — duplicates are skipped, not failed. **Confidence: HIGH.**

A title and version already in the tenant, in any state, is `skipped` with a
reason, and so is a repeat within one import. Skips do not block the import
under the default all-or-nothing rule; re-running an import is safe.

### D7 — all-or-nothing reports the valid files as `not_imported`. **Confidence: HIGH.**

When a file fails and `partial` is false, nothing is written, the response is
422, and each valid file is reported `not_imported` with the reason. The
operator sees what `--partial` would import.

### D8 — the importer is the user behind the credential. **Confidence: HIGH.**

`imported_by`, `created_by`, `published_by` and the audit `actor` take
`cred.UserID`, falling back to `cred.ID` for an API key. That is the rule the
policies export meta-audit uses. A JWT credential's `ID` is the token id,
which names no person.

### D9 — `approved_by` stays NULL on imported rows. **Confidence: HIGH.**

`approved_by` holds the credential that approved in the tool. The historical
approver is a free-text name in `historical_approver_name`, as the ticket
requires. A CHECK ties `source_attribution = imported` to the presence of the
historical approver, approval date, importer and import time.

### D10 — `source_url` must be an absolute http(s) URL. **Confidence: HIGH.**

The detail page renders it as a link. The parser rejects any other scheme
(`javascript:`, `data:`), and the page re-checks before rendering an `href`.

### D11 — `policy_audit_log` has no FK to `policies`. **Confidence: MEDIUM.**

The log is append-only: SELECT and INSERT grants only, plus a trigger that
refuses UPDATE and DELETE. That makes it a ledger to the tenant purge
classifier. An FK to `policies` would block deleting a policy, including
test cleanup, behind a log row that can never be removed.

### D12 — the list flags any policy with no linked controls. **Confidence: HIGH.**

The API already returns the `orphan_policy` warning on every such row. The
list now shows it as "No linked controls" next to an "Imported" marker. The
flag is not limited to imports: an orphan draft is the same gap, and drafts
still cannot be published without controls.

### D13 — free-text import fields are capped at 500 characters. **Confidence: HIGH.**

From review. The title, historical approver name and source file name are
capped at 500 in the parser and the store (`policy.MaxImportFieldLen`). The
migration adds the same cap as CHECKs on `policies.historical_approver_name`
and on `policy_audit_log.source_file` and `historical_approver_name`. The
title CHECK is not added to `policies` itself, so existing rows with long
titles cannot fail the migration.

### D14 — a next-version draft cannot be discarded; the 409 says so. **Confidence: MEDIUM.**

From review. A draft created with `predecessor_id` holds the single successor
slot. The API has no delete or discard route for a policy, so an abandoned
draft blocks another one. The 409 message (`ErrPredecessorTaken`) tells the
caller to continue the existing successor through submit, approve and publish,
and that drafts cannot be discarded through the API. Adding a discard route is
a separate change.

## Out of scope (per the ticket)

Editing `next_review_at` and overdue views (#1806), segregation of duties
(#1807), Confluence/SharePoint sync (#1808), acknowledgment history, and any
format other than Markdown with front matter.
