# Jira / Linear ticket connector

One binary covers both Atlassian Jira Cloud and linear.app. Both emit the same evidence kind — `jira.ticket_evidence.v1` — because the canonical Ticket shape (key, project, summary, status, resolution, assignee, URL) is identical across platforms. The `--platform` flag on `run` selects which API the connector pulls from.

| Kind                      | Profile | Source                                                                |
| ------------------------- | ------- | --------------------------------------------------------------------- |
| `jira.ticket_evidence.v1` | pull    | Jira REST `GET /rest/api/3/search` _or_ Linear GraphQL `issues` query |

## Auth — least-privilege

| Platform | Mechanism                                     | Env vars                       | Documented scope                                                       |
| -------- | --------------------------------------------- | ------------------------------ | ---------------------------------------------------------------------- |
| Jira     | HTTP Basic (email + API token)                | `JIRA_EMAIL`, `JIRA_API_TOKEN` | Project permission: **Browse projects** (Read) on every project pulled |
| Linear   | Authorization header (raw API key, no Bearer) | `LINEAR_API_KEY`               | API key: **Read-only access** (Read)                                   |

Env vars are preferred over flags so secrets never appear in shell history. The CLI flags exist (`--jira-token`, `--linear-key`) for testing / CI shells with disabled history.

**Banned scopes / permissions:** any write, delete, manage, or admin grant. The `atlas-jira scopes` subcommand prints the canonical list at runtime; the `DocumentedScopes` test rejects banned keywords at the test level.

### Jira API tokens

Issued at <https://id.atlassian.com/manage-profile/security/api-tokens>. The token inherits the minting user's permissions, so the operator should mint the token from a user with **only** the _Browse projects_ permission on the projects the connector reads. Tokens cannot be scoped further — that's why we recommend a dedicated service account.

### Linear API keys

Issued at _Settings → API → Personal API keys_. Linear's role model attaches scopes to the key itself; pick **read-only** at issuance time.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-jira register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Print the canonical scope/permission list for both platforms.
atlas-jira scopes

# Pull Jira issues and push jira.ticket_evidence.v1 records.
JIRA_EMAIL=ops@example.com JIRA_API_TOKEN=... \
atlas-jira run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --platform jira \
  --jira-base-url https://acme.atlassian.net \
  --jql 'project = CR AND status changed AFTER -90d' \
  --environment prod

# Pull Linear issues and push jira.ticket_evidence.v1 records.
LINEAR_API_KEY=... \
atlas-jira run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --platform linear \
  --team-key ENG \
  --environment prod
```

## Idempotency

Every push carries an idempotency key derived as

```
sha256("jira.ticket_evidence" + ticket_id + hour-rfc3339)
```

where `ticket_id` is the bare ticket key (`PROJ-123`, `ENG-42`) and `hour-rfc3339` is the observation time truncated to the hour in UTC (`2026-05-11T14:00:00Z`).

Replays within the same hour dedupe at the ledger. The hour granularity makes a periodic `run` loop the equivalent of a low-frequency event stream — slice 049+ will add webhook receivers for true event-driven push, but slice 048 ships pull-only.

## Scope cell

Every record carries three scope dimensions:

| Key           | Value                                                           |
| ------------- | --------------------------------------------------------------- |
| `platform`    | `"jira"` or `"linear"` (distinguishes records on a shared kind) |
| `project`     | Jira project key (e.g. `PROJ`) or Linear team key (e.g. `ENG`)  |
| `environment` | Whatever `--environment` supplied — `prod`, `staging`, etc.     |

The `source_attribution.actor_id` follows the convention `connector:<platform>:tickets@<version>`.

## Payload shape

The connector emits exactly the fields the bundled `jira.ticket_evidence/1.0.0.json` schema declares — `additionalProperties: false`, so any extra field is a schema error:

| Field         | Source (Jira)                       | Source (Linear)                            |
| ------------- | ----------------------------------- | ------------------------------------------ |
| `ticket_key`  | `issue.key`                         | `issue.identifier`                         |
| `project_key` | `issue.fields.project.key`          | `issue.team.key`                           |
| `summary`     | `issue.fields.summary`              | `issue.title`                              |
| `status`      | `issue.fields.status.name`          | `issue.state.name`                         |
| `resolution`  | `issue.fields.resolution.name`      | (Linear has no separate resolution; empty) |
| `assignee`    | `issue.fields.assignee.displayName` | `issue.assignee.name`                      |
| `url`         | `<base>/browse/<key>`               | `issue.url`                                |

Fields with empty source values are dropped from the payload to keep the wire form minimal.

## Anti-criteria (P0)

- Requires admin Jira/Linear scope → REJECTED. Documented scopes are read-only; `DocumentedScopes` test enforces.
- Logs API token / key → REJECTED. `Credential.String()` redacts both the secret bytes and the email; tests pin verbatim.
- Pushes without `idempotency_key` derived from ticket id + hour → REJECTED. Tests pin the verbatim sha256 hash.
- Mutates Jira/Linear data → REJECTED. The connector calls only `GET /rest/api/3/search` and the Linear `issues` GraphQL query. No transition, no comment, no update.

## Limitations (deferred to slice 049+)

- **No webhook receiver.** Both Jira Cloud and Linear support webhooks; this slice ships pull only. The hour-granularity idempotency key already absorbs frequent `run` cycles without ledger pollution.
- **No OAuth.** Jira Cloud supports OAuth 2.0 (3LO); we ship API-token auth because a stateless connector binary can't host the callback handler. OAuth lands when a hosted variant materializes.
- **No state-transition history.** The current `jira.ticket_evidence.v1` schema records the _current_ status. Per-transition records (open → in-progress → done) would need an additional `ticket.change_event.v1` schema; that's a future slice.
- **No Jira Server / Data Center.** The REST API base URL is configurable, so it _probably_ works against on-prem, but the auth shape is Cloud-shaped (email + token).

## Tests

```sh
go test ./connectors/jira/...
```

Tests use `httptest.NewServer` to replay realistic Jira REST + Linear GraphQL responses. Integration tests round-trip through a `bufconn`-hosted platform Server to confirm the on-wire shape.
