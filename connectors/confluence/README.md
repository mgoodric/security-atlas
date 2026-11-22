# connectors/confluence

The Confluence policy connector (#2856, spec #2813). It keeps the
security-atlas policy library in step with a Confluence Cloud space, with
Confluence as the system of record.

Each run:

1. reads every page carrying one label in one space, with its body in
   storage format, using a Confluence credential that stays with the
   connector;
2. reads each page's **Page Properties** table and converts the rest of
   the body to Markdown (`internal/storagemd`);
3. pushes the complete set to `POST /v1/policies:sync` in one request,
   authenticated with a `policy-sync` API key;
4. prints the platform's per-page report, plus anything the connector
   found itself.

security-atlas never calls Confluence and holds no Confluence token. The
platform stays push-only.

**All or nothing.** The platform reads a page absent from a push as a page
that lost its label. So a run that can't read the full set (a Confluence
error on any request, an unreadable response, more than 1000 labelled
pages) pushes nothing and exits non-zero. A run that finds no labelled
pages at all also pushes nothing and exits non-zero, because a misspelt
space or label looks the same as an empty space.

## Configuration

| Setting              | Flag                 | Environment                      | Notes                                                     |
| -------------------- | -------------------- | -------------------------------- | --------------------------------------------------------- |
| Site                 | `--site`             | `CONFLUENCE_SITE`                | `https://<org>.atlassian.net`. https only.                |
| Space                | `--space`            | `CONFLUENCE_SPACE`               | Space key, e.g. `POL`.                                    |
| Label                | `--label`            | `CONFLUENCE_LABEL`               | Defaults to `policy`.                                     |
| Confluence email     | `--confluence-email` | `CONFLUENCE_EMAIL`               | Atlassian account that minted the API token.              |
| Confluence API token | (none)               | `CONFLUENCE_API_TOKEN`           | Environment only.                                         |
| security-atlas URL   | `--atlas-url`        | `SECURITY_ATLAS_URL`             | The platform's HTTP base URL. https, or http on loopback. |
| Policy-sync key      | (none)               | `SECURITY_ATLAS_POLICY_SYNC_KEY` | Environment only.                                         |
| Run timeout          | `--timeout`          |                                  | Defaults to `10m`.                                        |
| Skipped-page limit   | `--max-skipped`      |                                  | Pages the converter may refuse before exit 3. Default 0.  |

The two secrets have no flags. They are read from the environment only,
so they never appear in shell history or a process listing. Resolve them at
run time, for example with 1Password:

```bash
export CONFLUENCE_API_TOKEN="$(op read 'op://<vault>/<item>/credential')"
export SECURITY_ATLAS_POLICY_SYNC_KEY="$(op read 'op://<vault>/<item>/credential')"
atlas-confluence run --site https://acme.atlassian.net --space POL --label policy \
  --atlas-url https://atlas.example.com
```

Build it with `go build ./connectors/confluence/cmd/atlas-confluence`.

### The Confluence credential

Basic auth with an Atlassian account email and an
[API token](https://id.atlassian.com/manage-profile/security/api-tokens).
The token carries that account's permissions, so mint it from an account
that can only **view** the policy space. The connector issues GET requests
only, and sends the credential only to the configured site. It ignores the
host of any link in a Confluence response, and refuses any redirect that
changes host or leaves https, for the Confluence read and the platform push
alike.

Pages are found with CQL content search,
`GET /wiki/rest/api/content/search?cql=type = page AND space = "<space>" AND label = "<label>"&expand=body.storage`,
following `_links.next` until the last page.

## Minting the policy-sync key

A `policy-sync` key can call `POST /v1/policies:sync` and nothing else.
Every other route refuses it, and the sync endpoint refuses every other
key, admin keys included (#2849). An admin mints one through the normal
key-issuing route:

```bash
curl -sS -X POST https://atlas.example.com/v1/admin/credentials \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"purpose": "policy-sync", "label": "confluence policy sync"}'
```

The response's `bearer_token` is shown once. Store it in your secret
manager. A policy-sync key cannot also carry `is_admin`, `is_approver`,
`owner_roles` or `allowed_kinds`; the route refuses the request with 400 if
it asks for them. Rotating the key keeps its purpose.

## Scheduling

Run the connector on your own scheduler (cron, a systemd timer, a CI
schedule), as with the other connectors. **Daily is recommended.** Nothing
on the platform schedules it.

Running it more often, retrying a failed run, or running two at once is
safe. An unchanged page is a no-op, so a repeated run never creates a
duplicate version.

```cron
# 06:15 every day
15 6 * * * /usr/local/bin/atlas-confluence run >> /var/log/atlas-confluence.log 2>&1
```

| Exit | Meaning                                                                                                                                                                                                                                                                 |
| ---- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0    | The push was accepted. The report says what happened to each page.                                                                                                                                                                                                      |
| 1    | Nothing was pushed (a Confluence error, no labelled pages, no convertible body), or the platform refused the push. A `422` refusal of an empty push is printed as `platform refused an empty push`.                                                                     |
| 3    | The push was accepted, but needs attention: more pages were `skipped by connector` than `--max-skipped` allows, or the platform returned a `warning:` line (for example, more than half of the source's policies missing from the push). The report is printed in full. |

Alert on any non-zero status. Exit 3 exists so that a converter regression
that refuses many pages, but not all of them, does not exit 0 while the
platform quietly marks those policies `missing`. `--max-skipped` (default 0) is how many converter skips a run tolerates before it exits 3; raise it
only for a page you have accepted cannot be converted.

## The Page Properties template

Each policy page carries a **Page Properties** macro whose table holds the
policy's metadata. One property per row: the key in the first cell, the
value in the second. A table whose first row is all header cells, with a
second row of values, also works. Only the first Page Properties macro on
a page is read. It is left out of the synced body. Any further Page
Properties macro is reported as an unsupported macro.

| Key (first cell)          | Required | Value                                                                                                               |
| ------------------------- | -------- | ------------------------------------------------------------------------------------------------------------------- |
| `Version`                 | yes      | The policy version. Bumping it creates the policy's next version in security-atlas.                                 |
| `Approved by`             | yes      | Approver's name, typed as text. A user @mention has no name in storage format and reads as empty.                   |
| `Approval date`           | yes      | `YYYY-MM-DD`, typed or from the date picker.                                                                        |
| `Effective date`          | yes      | `YYYY-MM-DD`.                                                                                                       |
| `Next review date`        | no       | `YYYY-MM-DD`. Drives the review calendar and needs-attention items.                                                 |
| `Owner role`              | no       | Defaults to `unassigned`.                                                                                           |
| `Approver role`           | no       | Defaults to `unassigned`.                                                                                           |
| `Acknowledgment audience` | no       | One or more of `everyone`, `admin`, `grc_engineer`, `control_owner`, `auditor`, `viewer`, comma- or line-separated. |

Keys are matched without regard to case or extra spaces, and a trailing
colon is ignored. Each key may also be written as the platform's property
name (`version`, `approved_by_name`, `approved_at`, `effective_date`,
`next_review_at`, `owner_role`, `approver_role`,
`acknowledgment_required_roles`). Any other key is passed through
unchanged, and the platform skips the page with `unknown field`, so a
misspelt key is reported, not ignored. An empty value counts as not set.

The page title is the policy title, and the page URL is its source link.
Control links and acknowledgements are kept in security-atlas, not in the
page.

## The report

One line per page, then the totals:

```text
page 101 "Access Control Policy": created version=1.0 policy=<id>
page 102 "Incident Response Policy": unsupported macros: toc x1
page 103 "Vendor Policy": skipped reason=missing required page property "effective_date"
warning: <a run-level note from the platform, if any>
sync: created=1 new_version=0 taken_over=0 unchanged=0 drift=0 skipped=1 missing=0 skipped_by_connector=0
```

| Outcome                | Meaning                                                                                                                                                                                                                                                                        |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `created`              | A new page became a published, synced policy. The page's approver and approval date are kept as the historical approver.                                                                                                                                                       |
| `new_version`          | The page's `Version` changed. A new version was published and the previous one superseded (`predecessor=`), so staff are asked to acknowledge again.                                                                                                                           |
| `taken_over`           | The page's first sync matched a published, imported policy by title. With the same version, the policy is now managed by the page. With a different version, the page's version became its next version.                                                                       |
| `unchanged`            | Nothing changed since the last sync.                                                                                                                                                                                                                                           |
| `drift`                | The body changed but `Version` did not. Nothing was written, and the policy shows the drift. Bump the version to publish the edit, or revert it.                                                                                                                               |
| `skipped`              | The platform refused the page, with the reason: missing properties, a missing or invalid required property, an unknown key, an unrecognised audience, or an empty body. Nothing was written for that page.                                                                     |
| `missing`              | A page synced before is not in this run: it lost its label, moved, or was deleted. The policy stays published, and gets a needs-attention item.                                                                                                                                |
| `unsupported macros`   | Printed by the connector. These macros or elements have no Markdown form, so the synced text shows a visible marker where each one was. The page is still synced. Replace the macro, or accept the marker.                                                                     |
| `skipped by connector` | Printed by the connector. The body could not be converted (unparseable, larger than 8 MiB, or nested more than 512 levels deep), so the page was left out of the push. If it was synced before, the platform reports it `missing`. More than `--max-skipped` of these exits 3. |
| `warning:`             | Printed from the platform's `warnings`: a run-level note, such as more than half of the source's published synced policies missing from this push. Any warning exits 3.                                                                                                        |

Synced policies are read-only in security-atlas. Edits happen in
Confluence.

## Tests

```bash
go test -race ./connectors/confluence/...
```

`cmd/atlas-confluence/run_test.go` replays Confluence Cloud search
responses through `httptest` and stands an `httptest` server in for the
platform endpoint. It covers a full set paginated over several responses,
a page with no properties, unsupported macros, a Confluence error
mid-read (no push, non-zero exit), a refused push, an empty set and an
unconvertible body (exit 3, and exit 0 within `--max-skipped`), a platform
warning (exit 3) and a 422 empty-push refusal.

### Live smoke run (not yet done)

The replayed responses follow Atlassian's documented `content/search`
shape. They have not yet been checked against a real Confluence Cloud
site (#2954). To do that, run the connector against a test space with
a read-only token and a scratch security-atlas, then record the result
in `docs/audit-log/2954-confluence-skip-exit-decisions.md`:

```bash
export CONFLUENCE_EMAIL='<account email>'
export CONFLUENCE_API_TOKEN="$(op read 'op://<vault>/<item>/credential')"
export SECURITY_ATLAS_POLICY_SYNC_KEY="$(op read 'op://<vault>/<item>/credential')"
go run ./connectors/confluence/cmd/atlas-confluence run \
  --site https://<org>.atlassian.net --space <KEY> --label policy \
  --atlas-url <scratch atlas URL>; echo "exit=$?"
```

Look for every labelled page in the report, each with a title and a URL
under `/wiki/spaces/<KEY>/pages/`, and for exit 0. With more than 50
labelled pages (the request's `limit`), the run also exercises
`_links.next`.
