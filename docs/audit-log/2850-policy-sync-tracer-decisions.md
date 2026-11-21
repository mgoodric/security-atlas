# 2850 — Tracer: POST /v1/policies:sync creates synced published policies: decisions log

Issue: mgoodric/security-atlas#2850, under the Confluence policy sync spec
(#2813). The policy-sync key purpose it accepts is #2849.

## Detection-tier classification

- **detection_tier_actual:** integration. Two bugs turned up while building,
  and the integration tier caught both. First, a new `policies.body_sha256`
  column made an existing test's join with `policy_audit_log.body_sha256`
  ambiguous (D3). Second, the `synced` audit rows left in a shared database
  broke the 20261273 migration test, which re-applies a narrower action
  CHECK (D8).
- **detection_tier_target:** integration. Both need real Postgres to show.

## Decisions

### D1 — the page id identifies a page across runs

The ticket lists each page's properties, body and URL. A page also needs a
stable identity, so an unchanged re-push can be recognised. A Confluence
page's title and URL slug both change on a rename. The payload therefore
carries the page `id` beside its `title` and `url`. A synced row is found by
(tenant, `sync_site`, `sync_space`, `sync_page_id`). The source is stored
as two columns because the spec names it as "site and space".

### D2 — payload shape and property names

```json
{
  "source": { "site": "acme.atlassian.net", "space": "SEC" },
  "pages": [
    {
      "id": "1001",
      "title": "Access Control Policy",
      "url": "https://acme.atlassian.net/wiki/spaces/SEC/pages/1001",
      "body_md": "# ...",
      "properties": {
        "version": "3.2",
        "approved_by_name": "Jane Doe",
        "approved_at": "2025-03-01",
        "effective_date": "2025-03-15",
        "next_review_at": "2026-03-01",
        "owner_role": "security_lead",
        "approver_role": "ciso",
        "acknowledgment_required_roles": ["everyone"]
      }
    }
  ]
}
```

The property keys are the import's front-matter keys. That gives the
connector (#2856) one vocabulary for a Page Properties table. `title`,
`source_url` and `linked_controls` are not properties. The title and URL are
the page's own, and control links stay in security-atlas (spec Q25).

`internal/policy/importfile` gains `FromPage`, which runs the same `build`
as `Parse`: required fields, date format, length caps, role defaults and
source URL check. Its errors name a "page property" and the "page body"
instead of front matter. The handler applies the import's audience-enum
check (#2586). Properties are decoded per page with unknown fields refused,
as an unknown front-matter key is refused. A misspelt property skips its
page and does not fail the run.

### D3 — `synced_body_sha256`, not `body_sha256`

The body hash first went on `policies` as `body_sha256`.
`editor_approval_2446_integration_test.go` joins `policies` with
`policy_audit_log`, which already has a `body_sha256`, and its unqualified
reference became ambiguous. The column is renamed `synced_body_sha256`. The
name says the column is set only on synced rows, and no join of the two
tables can be ambiguous again.

### D4 — a changed page is skipped until #2851

The tracer creates and recognises "unchanged". A page whose version or body
differs from its synced row needs #2851, which will chain a new version or
record drift. Until then such a page is reported `skipped` with a reason and
nothing is written. That fails safe: the text staff acknowledged is never
changed under them.

### D5 — invalid pages do not block valid ones

Import is all-or-nothing unless the caller asks for `partial`. Sync writes
every valid page and reports the invalid ones (spec: "a page with a missing
or invalid required property is reported and not synced"). An all-or-nothing
run would let one half-filled page hold back the whole library.

### D6 — authentication: the #2849 gate admits the key on this one path

`/v1/*` authenticates only JWTs. For `POST /v1/policies:sync`,
`refusePolicySyncKeys` becomes the authentication:

- it authenticates an `atlas_` bearer with `apikeystore.Authenticate`, which
  bumps `last_used_at`, and puts the credential on the context;
- a request with no API-key bearer gets 403: no header, a JWT, or a session
  cookie;
- a key that is not policy-sync gets 403, admin keys included;
- an unknown, revoked or expired key gets 401;
- a lookup failure gets 503.

The path is added to jwtmw's bypass list, so a session cookie riding along
cannot overwrite the key's credential. `requireCredential` still runs, so a
server without an API-key store answers 401. tenancymw, the module gate and
OPA then run as on every other route. The route is registered with a
literal path because `scripts/check-openapi-drift.sh` greps for literals.
The gate matches the same string through `policySyncPath`.

### D7 — OPA: a policy-sync key derives no role; one rule admits the route

Unbound API keys take the `grc_engineer` default in `derivedRolesFor`. A
policy-sync key would have inherited it, and with it every write the
grc_engineer rules allow. The HTTP gate stops that today, but OPA is meant
to be the second leg. A policy-sync key now derives no role. A new
`is_policy_sync_key` input attribute lets `system.rego` admit
`POST /v1/policies:sync` and nothing else. Removing the rule makes the sync
integration test fail with 403, so OPA is in the request path.

### D8 — the 20261273 migration test clears later audit actions

`TestPolicyAudienceGenericToEveryoneMigration` re-applies 20261273, which
narrows `policy_audit_log_action_check` to the actions it lists. After the
sync tests have written `synced` rows into the same database, the re-apply
fails with 23514. It is the #2865 hazard again, here for `policy_audit_log`.
The test now deletes, inside its rolled-back transaction, every row whose
action that CHECK does not list. The table is append-only, so it lifts the
trigger for the delete. CI did not show this, because each shard gets a
fresh database.

### D9 — overlapping runs are serialised per source

`Sync` takes `pg_advisory_xact_lock(13, hashtext(tenant|site|space))`
(registered as `advisorylock.PolicySyncSource`). A second run for the same
source waits, then finds its pages unchanged. The partial unique index
`idx_policies_synced_page` (one published synced row per page) is the
backstop.

### D10 — the audit row

A `synced` `policy_audit_log` row carries:

- `actor`: the sync key's id;
- `source_file`: the page id;
- `historical_approver_name`, `historical_approved_at` and `source_url`;
- `policy_title`, `policy_version` and `body_sha256`.

`created_by`, `published_by` and `synced_by` on the policy are also the key
id. `approved_by` stays NULL, as for an import: the historical approver is
free text, not a credential.

### D11 — out of scope here, owned by sibling tickets

- Version chaining and drift: #2851.
- 409 locking of synced policies, and the web badge: #2852.
- Missing pages: #2853. The sync accepts an empty `pages` list, because the
  full-set semantics make an empty set meaningful.
- Takeover of imported policies: #2854. Until it lands, a page whose title
  matches an imported policy creates a second policy.
