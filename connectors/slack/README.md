# Slack connector

The slice-443 Slack connector. A SaaS startup runs its operations in Slack,
so "who has workspace access," "what the admin audit log shows," and "what the
message-retention setting is" are recurring SOC 2 / ISO access-control and
data-retention evidence demands. This connector collects exactly those three
metadata surfaces and pushes them to the platform — and **never reads message
content**.

It follows the slice-004 AWS connector pattern: register-per-run, stable
`actor_id`, hour-truncated `observed_at`, scope minimums, vendor-native auth.

| Kind                          | Profile | Source                          | Default SCF anchors |
| ----------------------------- | ------- | ------------------------------- | ------------------- |
| `slack.workspace_member.v1`   | pull    | `users.list` (admin users)      | `IAC-01`, `IAC-06`  |
| `slack.admin_audit_event.v1`  | pull    | audit-logs API `/audit/v1/logs` | `MON-01`            |
| `slack.retention_settings.v1` | pull    | `team.preferences.list`         | `DCH-01`, `DCH-03`  |

- **workspace_member** — one record per member: stable id, public handle,
  admin/owner role flags, deactivation state, and 2FA-enforcement. Verdict:
  `pass` (2FA enrolled or correctly deprovisioned), `fail` (no 2FA),
  `inconclusive` (bot account — 2FA not applicable).
- **admin_audit_event** — one record per admin audit-log entry: action, actor
  id + email, entity type, and event timestamp. Observational evidence
  (`RESULT_UNSPECIFIED`), not a pass/fail check.
- **retention_settings** — one record per workspace: message- and
  file-retention durations and the policy-enabled flag. Verdict: `pass`
  (finite enabled policy), `fail` (retain-forever / no policy).

## What this connector does NOT collect (threat-model I — primary risk)

Slack holds extremely sensitive message content. **The connector never reads
it.** No message bodies, no DMs, no channel history, no thread replies, no file
contents. The evidence structs physically have no field that could hold any of
these, and two build-failing tests enforce it:

- a reflection guard (`slackcollect_test.go::TestNoMessageContentField`) fails
  the build if a content-bearing field is ever added to an evidence struct;
- a payload guard (`cmd_run_test.go::TestDoRun_NoMessageContentInPayload`)
  fails the build if a pushed record's payload ever carries a content key.

The required Slack scopes deliberately exclude every message-read scope.

## Auth — least-privilege read-only Slack OAuth token

The connector authenticates to Slack with a **read-only** OAuth token scoped
to the minimum below. The token is source-side (invariant #3): it stays with
the connector process, is carried only on the outbound Slack `Authorization`
header, and is **never logged** or transmitted to the platform (a
`String()`/`GoString()` redaction guard + `TestTokenNeverLogged` enforce this).

| Slack scope                | Why                                              |
| -------------------------- | ------------------------------------------------ |
| `admin.users:read`         | workspace member roster (access evidence)        |
| `auditlogs:read`           | admin audit-log entries (admin-action evidence)  |
| `admin.conversations:read` | retention-settings posture (data-retention)      |
| `admin.teams:read`         | resolve the workspace/team id for record scoping |

**Banned scopes — never grant these.** Any message-read scope defeats the
over-collection boundary and is rejected as a misconfiguration:
`channels:history`, `groups:history`, `im:history`, `mpim:history`,
`search:read`. Grant read-only admin/audit scopes only; never a write or
admin-mutation scope.

> Audit-logs API access requires an Enterprise Grid workspace; on non-Grid
> plans the audit surface returns no entries and the member + retention
> surfaces still collect.

## Pull profile and interval (named honestly)

`register` announces `profiles_supported=[pull]`. `profiles_supported`
describes how the connector retrieves data **from Slack** — a scheduled poll.
The **platform-side wire is always push** (invariant #3): every record is
pushed to the single `EvidenceIngestService.Push` API.

Recommended cadence: **daily**, run by the operator's job runner. This is a
scheduled pull, named honestly — it is **not** event-driven and **not**
"continuous monitoring". A real-time audit-log-streaming profile is a
follow-on slice.

## Subcommands

```sh
# Build the binary (justfile target).
just connector-build-slack   # produces ./bin/slack-connector

# Announce this connector instance to the platform.
slack-connector register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Read the roster + admin audit-log + retention settings and push evidence.
slack-connector run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --slack-token "$SLACK_TOKEN"
```

| Flag                     | Subcommand | Required | Default                       | Notes                                       |
| ------------------------ | ---------- | -------- | ----------------------------- | ------------------------------------------- |
| `--endpoint`             | both       | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                      |
| `--token`                | both       | yes      | env `SECURITY_ATLAS_TOKEN`    | platform bearer token                       |
| `--insecure`             | both       | no       | `false`                       | disables TLS; loopback endpoints only       |
| `--slack-token`          | `run`      | yes      | env `SLACK_TOKEN`             | least-privilege read-only Slack OAuth token |
| `--member-control-id`    | `run`      | no       | `scf:IAC-01`                  | control id on member records                |
| `--audit-control-id`     | `run`      | no       | `scf:MON-01`                  | control id on audit records                 |
| `--retention-control-id` | `run`      | no       | `scf:DCH-01`                  | control id on the retention record          |

## Scope minimums

Every emitted record sets the minimum scope dimension the slice-004
connector-pattern convention requires:

| Scope key          | Value shape       | Source                      |
| ------------------ | ----------------- | --------------------------- |
| `tenant_workspace` | `slack:<TEAM_ID>` | `team.info` (the workspace) |

`source_attribution.actor_id` follows the cross-connector convention
`connector:<vendor>:<service>@<version>` — `connector:slack:members@<version>`,
`connector:slack:auditlogs@<version>`, `connector:slack:retention@<version>` —
so the three kinds carry distinct, traceable actor ids.

## Idempotency

- **Member / retention** records: `idempotency_key = sha256(anchor | hour_truncated_observed_at)`
  (anchor = user id, or `retention:<team_id>`). Two runs within the same hour
  collapse to one ledger row; a run crossing an hour boundary writes a fresh
  record.
- **Audit** records: `idempotency_key = sha256(entry_id | date_create)` — the
  Slack audit entry id is immutable, so the same entry observed across runs
  always dedups.

`source_attribution.session_id` is left empty on purpose: a per-call UUID would
change the record's canonical hash between dedup retries.

## Anti-criteria (P0)

- Reads message content / DMs / channel history → REJECTED. Membership / admin
  / retention metadata only; structurally enforced (no content field exists).
- Requires a message-read or write/admin Slack scope → REJECTED. Read-only
  least-privilege scopes only.
- Logs or transmits the Slack token to the platform → REJECTED. Token is
  source-side, header-only, redacted in every `fmt` verb.
- Widens the platform-side wire → REJECTED. Push only (invariant #3).
- Labels the pull profile "continuous monitoring" → REJECTED. Honest interval.

## Tests

```sh
go test ./connectors/slack/...
```

Unit tests fake the Slack surfaces (no live Slack) and pin the access /
retention verdicts, pagination + the `MaxPages` denial-of-service bound, the
idempotency hour-window behavior, token redaction, scope discipline, the
collect→push round-trip, and the two over-collection guards.
