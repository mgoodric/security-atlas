# 2837 — SharePoint policy sync on the policy-sync contract: decisions log

Issue: mgoodric/security-atlas#2837, a follow-up to the Confluence policy
sync spec #2813 (grill decision Q3, 2026-11-20). Builds on the contract
#2849-#2856 shipped, with #2877 (property drift) and #3000 (unreadable
pages).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — Auth: an Entra app registration, client credentials, secret in the environment

The connector authenticates to Microsoft Graph as an app (client
credentials grant, `POST login.microsoftonline.com/{tenant}/oauth2/v2.0/token`
with `scope=https://graph.microsoft.com/.default`), not as a user. A
scheduled sync has no user to sign in, and an app's permissions are
granted and revoked by a tenant admin in one place.

- The client secret is read from `SHAREPOINT_CLIENT_SECRET` only. It has
  no flag, so it never reaches a process listing or shell history. The
  tenant and client ids are not secrets and may be flags.
- A client secret, not a certificate. A certificate credential needs a
  signed JWT assertion; a secret is what every other connector here
  uses, and 1Password resolves it the same way. A certificate is a later
  option if an adopter's tenant forbids secrets.
- Permissions: Microsoft documents `Sites.Read.All` (site, list item) and
  `Files.Read.All` (driveItem) as the least-privileged application
  permissions for the endpoints used. `Sites.Selected` (a per-site grant)
  would be narrower; it has not been tried with this connector and is
  named in the README as untested, not recommended.
- Hosts are fixed and checked on every request: the secret goes only to
  `login.microsoftonline.com`, the bearer token only to
  `graph.microsoft.com` over https. A next link off Graph is refused
  before it is requested; a redirect that changes host or leaves https is
  refused (`policysync.CheckRedirect`, the guard the Confluence connector
  already used). The file content comes from the driveItem's
  `@microsoft.graph.downloadUrl`, which Microsoft documents as needing no
  authentication, so it is fetched **without** the token, and only from
  an https `*.sharepoint.com` host. This follows the 1Password
  connector's fixed-host allowlist (#3004).
- National clouds (GCC High, DoD, 21Vianet) use other login and Graph
  hosts. Not supported: adding them is a host table and a flag, done
  when an adopter needs it.

### D2 — Selection: one site, one document library, every file in it

The Confluence selection is one space plus one label. The SharePoint
equivalent chosen is one site plus one **document library**: every file
in the library is a policy.

- A library is how SharePoint users already group a document set, it has
  its own columns (D3), and permissions can be set on it. A label
  equivalent (a Yes/No "Is policy" column, or a folder) would add a
  second thing to keep in step with no gain for a library kept for
  policies.
- The library is matched by display name, ignoring case, among the
  site's lists whose template is `documentLibrary`. None, or more than
  one, is an error (nothing pushed).
- A file that is not a `.docx` is **in** the set and is pushed
  `unreadable` with its reason, rather than left out. Leaving it out
  would make a policy whose file was replaced by a PDF read as `missing`
  ("lost its label"), which is the wrong message; `unreadable` holds the
  policy at its last version and says why, every run, and counts toward
  `--max-skipped` (exit 3).
- Folders are left out (driveItem with a `folder` facet, or no `file`
  facet).
- The source sent is `site` = the site URL, lower-cased, and `space` =
  the library's display name as Graph returns it. SharePoint URLs ignore
  case, so a change of case in `--site` or `--library` must not read as a
  new source whose policies duplicate the old one's.
- A document's id is its **list item id**, which survives a rename or a
  move within the library. Its title is the `Title` column, else the file
  name without `.docx`. Its URL is the driveItem's `webUrl`.

### D3 — Properties: library columns

The required fields (version, approver, approval date, effective date)
and the optional ones live in library columns, the SharePoint counterpart
of Confluence's Page Properties table.

- Column names are matched without regard to case, spaces or a trailing
  colon, after decoding SharePoint's `_xHHHH_` escapes, so the display
  name (`Approved by`), the encoded internal name (`Approved_x0020_by`)
  and a run-together internal name (`ApprovedBy`) all match. The
  platform's property names also match.
- **`Policy version`, not `Version`.** SharePoint's own `Version` column
  (`_UIVersionString`) is the file's edit count and changes on every
  save; reading it as the policy version would publish a new version per
  save. A column literally named `version` (the property name) is also
  accepted.
- **Unknown columns are ignored**, unlike Confluence, where an unknown
  Page Properties key is passed through so the platform refuses it. Every
  library carries dozens of SharePoint system columns, so passing unknown
  columns through would skip every document. A misspelt _required_ column
  is still reported, as missing.
- **Dates.** A Date-only column is stored as midnight in the site's time
  zone, returned as an instant. The connector reads it back as a date in
  `--time-zone` (default UTC). With the wrong zone a date can read as the
  day before; the README says to set it to the site's zone. Text that is
  not an RFC 3339 instant (for example a typed `2025-03-01`) is passed
  through for the platform to validate.
- **Values.** Text and Choice give strings; a Number gives its shortest
  decimal form (`1.0` reads `1`, so the README asks for a text version
  column); a multi-value Choice gives a list, used for the audience. A
  Person or Lookup column (an object, or a `...LookupId`) is not read and
  is named in the report as `columns not read`: Graph returns an id, not
  the approver's name.
- An empty column is left out, so an empty required column is reported
  missing by the platform.

### D4 — Bodies: .docx only, converted in-process

`internal/docxmd` converts a `.docx` (Office Open XML, transitional or
strict: elements are matched by local name) to Markdown with
`archive/zip` and `encoding/xml`. No new dependency.

- Converted: Title and Heading 1-6 styles (by style **name**, so a
  localised style id still maps), paragraphs, bold, italic,
  strikethrough, hard breaks, bulleted and numbered lists (from the
  paragraph's numbering or its list style, nested, never indented more
  than one level below the previous item so no item reads as a code
  block), external `http`, `https` and `mailto` links (any other scheme
  renders as text), GFM tables (first row as header; merged cells keep
  the column count; cell paragraphs and breaks joined with `<br>`, as
  storagemd does). Tracked changes read as accepted. Field codes,
  bookmarks, headers, footers and comments are not body text.
- Not converted, each leaving an escaped visible marker
  `[Unsupported Word content: <kind>]` and counted per document in the
  report: images (`drawing`, `pict`), embedded objects, equations,
  footnote and endnote references, nested tables. The document still
  syncs, as a Confluence page with an unsupported macro does.
- Refused (pushed `unreadable`, as #3000): not a zip or not a `.docx`,
  a file over 16 MiB, any XML part over 8 MiB (bounded by reading, not by
  trusting the zip header), nesting over 512 levels.
- **SharePoint pages** (`.aspx` site pages) are not supported. The ticket
  allowed "if supported"; they are not files in a document library, and
  their canvas content is a separate Graph surface. A library holding
  pages would report them as `not a .docx document (.aspx)`.

### D5 — The shared client, and no change to the sync contract

The request and report shapes, the push, the report lines and the URL
and redirect guards moved from the Confluence connector into
`connectors/shared/policysync`, which both connectors use. The Confluence
connector keeps its API through type aliases and thin wrappers; its tests
are unchanged and pass. Without this the push and its guards would exist
twice.

**The sync endpoint's contract is unchanged** (AC 5): same request
(`source.site`, `source.space`, `pages[]`), same report, same key
purpose. The only edits under `internal/api/openapi/routes.go` are
description text: the sync route now says "a policy connector
(Confluence, SharePoint)" instead of "the Confluence policy connector",
and `GET /v1/policies/{id}` documents the new `sync_source` field.

### D6 — Naming the source on the badge and the 409

#2813 hard-coded "Confluence" in the 409 (`managed in Confluence`), the
badge ("Managed in Confluence") and the approval line ("Approved in
Confluence by"). AC 3 asks to generalise it from the policy's source.

- The sync contract carries no source kind, and D5 keeps it unchanged.
  The kind is read from the source the policy already stores,
  `sync_site`: a `*.sharepoint.com` site is SharePoint, anything else
  Confluence (`policy.SyncSourceName`). Confluence sites have no single
  host suffix (custom domains exist), so Confluence is the default rather
  than a match.
- The store's refusal is now a `*policy.ManagedBySyncError` naming the
  source; `errors.Is(err, policy.ErrManagedBySync)` still matches, so
  every existing caller and test is unaffected. The handler's 409 says
  `managed in <source>: ...`.
- `GET /v1/policies/{id}` (and every response built from a policy row)
  carries `sync_source` on a synced policy. The web badge, the panel
  sentence and the approval line use it, falling back to Confluence for a
  response without it. The `?include=ack_rate` list shape does not carry
  it: no list view shows the badge, and adding it there means a sqlc
  query change for no reader.

### D7 — What was not checked live

The Graph responses replayed in the tests follow Microsoft's documented
examples. Not confirmed from the documentation read, and so open until a
live smoke run (README, "Live smoke run"):

- whether `fields` is keyed by column internal name (assumed; display
  names also match, D3);
- the JSON shape of Date, multi-value Choice and Person columns in
  `fields`;
- that list items include files inside folders of the library;
- whether a download URL can redirect across hosts (the redirect guard
  would refuse it and the run would push nothing);
- `Sites.Selected` as a narrower permission.

The `.docx` fixtures are hand-written OOXML parts in the shapes Word
writes (kept unzipped under `internal/docxmd/testdata`, zipped by the
test). No document saved by Word itself was available here; a live run
on real policy documents is the check.
