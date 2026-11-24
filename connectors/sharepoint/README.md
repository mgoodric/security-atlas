# connectors/sharepoint

The SharePoint policy connector (#2837, follow-up to the Confluence sync
spec #2813). It keeps the security-atlas policy library in step with one
SharePoint Online document library, with SharePoint as the system of
record. It is the Confluence connector's counterpart
([`connectors/confluence`](../confluence/README.md)) on the same
policy-sync contract: the platform side is unchanged.

Each run:

1. gets an app-only token for Microsoft Graph with an Entra app
   registration's client credentials, which stay with the connector;
2. reads every item in one document library, with its column values, and
   downloads each `.docx`;
3. reads each document's policy properties from its **library columns**
   and converts the document body to Markdown (`internal/docxmd`);
4. pushes the complete set to `POST /v1/policies:sync` in one request,
   authenticated with a `policy-sync` API key;
5. prints the platform's per-document report, plus anything the connector
   found itself.

security-atlas never calls Microsoft Graph and holds no Microsoft
credential. The platform stays push-only.

**All or nothing.** The platform reads a document absent from a push as
one that left the library. So a run that can't read the full library (a
Graph error on any request, a failed download, an unreadable response,
more than 1000 documents) pushes nothing and exits non-zero. A run that
finds no documents at all also pushes nothing and exits non-zero, because
a wrong library looks the same as an empty one.

## Configuration

| Setting            | Flag            | Environment                      | Notes                                                                         |
| ------------------ | --------------- | -------------------------------- | ----------------------------------------------------------------------------- |
| Site               | `--site`        | `SHAREPOINT_SITE`                | `https://<tenant>.sharepoint.com/sites/<site>`. https and `*.sharepoint.com`. |
| Library            | `--library`     | `SHAREPOINT_LIBRARY`             | The document library's display name, e.g. `Policies`. Case is ignored.        |
| Tenant id          | `--tenant-id`   | `SHAREPOINT_TENANT_ID`           | Entra directory (tenant) id, or a verified domain.                            |
| Client id          | `--client-id`   | `SHAREPOINT_CLIENT_ID`           | The app registration's application (client) id.                               |
| Client secret      | (none)          | `SHAREPOINT_CLIENT_SECRET`       | Environment only.                                                             |
| Time zone          | `--time-zone`   | `SHAREPOINT_TIME_ZONE`           | The site's IANA time zone, e.g. `Europe/Berlin`. Defaults to `UTC`.           |
| security-atlas URL | `--atlas-url`   | `SECURITY_ATLAS_URL`             | The platform's HTTP base URL. https, or http on loopback.                     |
| Policy-sync key    | (none)          | `SECURITY_ATLAS_POLICY_SYNC_KEY` | Environment only.                                                             |
| Run timeout        | `--timeout`     |                                  | Defaults to `10m`.                                                            |
| Skipped limit      | `--max-skipped` |                                  | Documents the converter may refuse before exit 3. Default 0.                  |

The two secrets have no flags. They are read from the environment only,
so they never appear in shell history or a process listing. Resolve them
at run time, for example with 1Password:

```bash
export SHAREPOINT_CLIENT_SECRET="$(op read 'op://<vault>/<item>/credential')"
export SECURITY_ATLAS_POLICY_SYNC_KEY="$(op read 'op://<vault>/<item>/credential')"
atlas-sharepoint run --site https://contoso.sharepoint.com/sites/Policies --library Policies \
  --tenant-id <directory id> --client-id <application id> --time-zone Europe/Berlin \
  --atlas-url https://atlas.example.com
```

Build it with `go build ./connectors/sharepoint/cmd/atlas-sharepoint`.

### The Entra app registration

Register an app in Microsoft Entra ID, add a client secret, and grant it
**application** permissions for Microsoft Graph (admin consent required).
Microsoft documents `Sites.Read.All` as the least-privileged application
permission for the site and list-item reads, and `Files.Read.All` for the
driveItem read. Grant only read permissions: the connector never writes.

`Sites.Read.All` reads every site in the tenant. To limit the app to the
policy site, Microsoft offers `Sites.Selected` with a per-site grant;
that route has **not** been tried with this connector yet (see the
decisions log).

The connector sends the client secret only to
`https://login.microsoftonline.com`, the bearer token only to
`https://graph.microsoft.com`, and downloads each file from the
pre-authenticated URL Graph returns, without the token, and only from a
`*.sharepoint.com` host. It refuses any next link or download URL on
another host, and any redirect that changes host or leaves https.
National clouds (GCC High, DoD, China) are not supported.

The Graph requests, all documented v1.0 endpoints:

| Purpose           | Request                                                                                                   |
| ----------------- | --------------------------------------------------------------------------------------------------------- |
| Token             | `POST login.microsoftonline.com/{tenant}/oauth2/v2.0/token` (client credentials)                          |
| The site          | `GET /sites/{hostname}:/{site path}`                                                                      |
| The library       | `GET /sites/{site-id}/lists`, matched by display name                                                     |
| Items and columns | `GET /sites/{site-id}/lists/{list-id}/items?expand=fields`, following `@odata.nextLink`                   |
| Each file         | `GET /sites/{site-id}/lists/{list-id}/items/{item-id}/driveItem`, then its `@microsoft.graph.downloadUrl` |

## Minting the policy-sync key

As for Confluence: an admin mints a key with
`"purpose": "policy-sync"` through `POST /v1/admin/credentials`. See
[the Confluence README](../confluence/README.md#minting-the-policy-sync-key).
One key can serve both connectors.

## Scheduling

Run the connector on your own scheduler (cron, a systemd timer, a CI
schedule), as with the other connectors. **Daily is recommended.**
Running it more often, retrying a failed run, or running two at once is
safe: an unchanged document is a no-op.

| Exit | Meaning                                                                                                                                                          |
| ---- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0    | The push was accepted. The report says what happened to each document.                                                                                           |
| 1    | Nothing was pushed (a Graph error, an empty library, no convertible document), or the platform refused the push.                                                 |
| 3    | The push was accepted, but more documents were sent `unreadable` than `--max-skipped` allows, or the platform returned a `warning:` line. The report is printed. |

Alert on any non-zero status.

## Selecting the policies

The library is the selection: **every file in it is a policy**. Folders
themselves are left out. Keep the policies in a library of their own.
This is the SharePoint equivalent of Confluence's one space plus one
label. Files in a folder of the library are expected to be included too,
since the connector reads the library's list items, but that has not yet
been checked on a live tenant.

A file that is not a `.docx` (a PDF, a legacy `.doc`, a spreadsheet) is
still part of the set: it is pushed `unreadable` with the reason `not a
.docx document (.pdf)`, so it is reported every run, its policy (if any)
is held at its last synced version, and it counts toward
`--max-skipped`. Move such files out of the library, or convert them.

## The library columns

Each document's policy metadata lives in the library's columns. Create
these columns on the library (Library settings, then Create column):

| Column                    | Type                           | Required | Value                                                                                     |
| ------------------------- | ------------------------------ | -------- | ----------------------------------------------------------------------------------------- |
| `Policy version`          | Single line of text            | yes      | The policy version. Changing it creates the policy's next version in security-atlas.      |
| `Approved by`             | Single line of text            | yes      | Approver's name, as text. A Person column is not read (see below).                        |
| `Approval date`           | Date only                      | yes      |                                                                                           |
| `Effective date`          | Date only                      | yes      |                                                                                           |
| `Next review date`        | Date only                      | no       | Drives the review calendar and needs-attention items.                                     |
| `Owner role`              | Single line of text, or Choice | no       | Defaults to `unassigned`.                                                                 |
| `Approver role`           | Single line of text, or Choice | no       | Defaults to `unassigned`.                                                                 |
| `Acknowledgment audience` | Choice (multiple), or text     | no       | One or more of `everyone`, `admin`, `grc_engineer`, `control_owner`, `auditor`, `viewer`. |

Notes:

- **Not "Version".** SharePoint's own `Version` column is the file's edit
  count, which changes on every save. The policy's version is the
  `Policy version` column. Use text, not Number: a Number column holding
  `1.0` reads as `1`.
- **Names.** Columns are matched by name without regard to case, spaces
  or a trailing colon, so `Approved by` and `ApprovedBy` both work. Each
  may also be named as the platform's property (`version`,
  `approved_by_name`, `approved_at`, `effective_date`, `next_review_at`,
  `owner_role`, `approver_role`, `acknowledgment_required_roles`). Any
  other column is ignored, since every library carries SharePoint's own
  columns. A misspelt **required** column is reported as missing.
- **Dates** are stored by SharePoint as midnight in the site's time zone.
  Set `--time-zone` to the site's zone (Site settings, Regional settings)
  so the dates read back as the days entered. With the wrong zone a date
  can read as the day before.
- **Person and Lookup** columns are not read: Graph returns them as ids,
  not names. The report lists such a column as `columns not read`.
- The document's `Title` column is the policy title, or its file name
  without `.docx` when Title is empty. The document's web URL is its
  source link.

Control links and acknowledgements are kept in security-atlas, not in the
library.

## The body: .docx to Markdown

Headings (Word's built-in Heading 1-6 and Title styles), paragraphs, bold,
italic, strikethrough, line breaks, bulleted and numbered lists (nested),
external links, and tables convert. Tracked changes read as accepted.

Images, embedded objects, equations, footnote and endnote references, and
tables inside a table cell have no Markdown form. Each leaves a visible
marker such as `[Unsupported Word content: image]` in the synced text, and
the report lists them:

```text
document 7 "Network Security Policy": unsupported content: image x2, nested table x1
```

The document is still synced. Headers, footers and comments are not part
of the body and are left out.

A document the converter refuses (not a `.docx`, over 16 MiB, an XML part
over 8 MiB or 2^20 elements, nested more than 512 levels, or not a readable `.docx`) is
sent `unreadable`, as on Confluence.

## The report

One line per document, then the totals, as on Confluence:

```text
document 12 "Access Control Policy": created version=1.0 policy=<id>
document 13 "Vendor Policy": skipped reason=<the missing or invalid column>
document 14 "Old Policy": unreadable reason=body not converted: not a .docx document (.pdf); only Word .docx files convert
sync: created=1 new_version=0 taken_over=0 unchanged=0 drift=0 skipped=1 missing=0 unreadable=1
```

The outcomes (`created`, `new_version`, `taken_over`, `unchanged`,
`drift`, `skipped`, `missing`, `unreadable`, `warning:`) mean what they
mean on Confluence; see [its README](../confluence/README.md#the-report).
In security-atlas a synced SharePoint policy shows **Managed in
SharePoint**, and an in-tool edit of it is refused with `409 managed in
SharePoint`.

## Tests

```bash
go test -race ./connectors/sharepoint/... ./connectors/shared/...
```

`cmd/atlas-sharepoint/run_test.go` replays Microsoft Graph through
`httptest` (`internal/graph/graphtest`): the token endpoint, the site,
the lists (paginated), the items (paginated over several responses), each
item's driveItem and the files' download URLs, plus an `httptest` stand-in
for the platform endpoint. It asserts where every credential went, and
covers a Graph error mid-read, a next link, download URL or redirect off
the allowed hosts (no push, exit 1), an empty library, an unconvertible
library, a refused push, a platform warning and `--max-skipped`.
`internal/docxmd` is tested on `.docx` fixtures kept unzipped under
`testdata/` and zipped by the test.

### Live smoke run (not yet done)

The replayed responses follow Microsoft's documented shapes. They have not
been checked against a real tenant. In particular, the keys Graph uses for
column values in `fields` and the JSON shape of date and multi-choice
values are not stated in the pages read; the connector handles the
internal-name form (`Approved_x0020_by`) and ISO 8601 dates. To check, run
against a test library with a scratch security-atlas, then record the
result in `docs/audit-log/2837-sharepoint-policy-sync-decisions.md`:

```bash
export SHAREPOINT_CLIENT_SECRET="$(op read 'op://<vault>/<item>/credential')"
export SECURITY_ATLAS_POLICY_SYNC_KEY="$(op read 'op://<vault>/<item>/credential')"
go run ./connectors/sharepoint/cmd/atlas-sharepoint run \
  --site https://<tenant>.sharepoint.com/sites/<site> --library <library> \
  --tenant-id <directory id> --client-id <application id> --time-zone <zone> \
  --atlas-url <scratch atlas URL>; echo "exit=$?"
```
