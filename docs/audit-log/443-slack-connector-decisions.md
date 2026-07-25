# Slice 443 — Slack connector — JUDGMENT decisions log

**Slice type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Parent:** docs/issues/443-slack-connector.md
**Pattern template:** slice 004 (AWS connector — LOCKED connector pattern)

- detection_tier_actual: unit
- detection_tier_target: unit

> No product-code bug surfaced. One test-only defect surfaced and was caught
> at the unit tier (the cheapest possible): the first cut of the two
> over-collection guards (`TestNoMessageContentField` +
> `TestDoRun_NoMessageContentInPayload`) used naive substring matching, which
> false-positived on legitimate metadata field names (`is_admin` contains
> "dm"; `messages_retention_days` contains "message"). Refined both guards to
> split identifiers into word tokens and match exact content words ("body",
> "text", "history", "dm", ...). The connector code itself was correct
> throughout; the guard was over-eager. `actual == target == unit` — caught
> where it should be, no escape.

---

## D1 — Three evidence kinds, not one (scope shape)

The AWS exemplar (slice 004) ships exactly one kind. Slice 443's spec calls for
three distinct evidence surfaces (members / admin audit-log / retention
settings), each with a different shape, lifecycle, and verdict semantics, so
each gets its own `.v1` kind rather than a polymorphic single kind with a
`surface` discriminator:

- `slack.workspace_member.v1` — access evidence, one record per member, pass/fail.
- `slack.admin_audit_event.v1` — admin-action evidence, one record per entry, observational (`RESULT_UNSPECIFIED`).
- `slack.retention_settings.v1` — data-retention evidence, one record per workspace, pass/fail.

**Why three kinds, not one:** a single kind would force a union payload schema
(`additionalProperties` chaos) and a meaningless shared verdict. Three kinds
keep each JSON Schema `additionalProperties: false` and let the evaluator treat
each surface with the right semantics. This mirrors the GitHub connector, which
ships `github.repo_protection.v1` + `github.audit_event.v1` + `github.scim_user.v1`
as three sibling kinds for the same reason.

## D2 — SCF anchor choices (`x-default-scf-anchors`, OQ #9 governance)

Every chosen anchor was verified present in the bundled SCF fixture
(`migrations/fixtures/scf-sample.json`) — the spec warns repeatedly that
candidate anchors absent from the fixture break the importer, so each was
grep-checked before selection.

| Kind                          | Anchors            | Rationale                                                                                                                            |
| ----------------------------- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------------ |
| `slack.workspace_member.v1`   | `IAC-01`, `IAC-06` | IAC-01 = Identity & Access Management (who has workspace access); IAC-06 = Authenticator Management (the 2FA-enforcement dimension). |
| `slack.admin_audit_event.v1`  | `MON-01`           | Continuous Monitoring / audit-logging — the SAME anchor `github.audit_event.v1` uses, for cross-connector consistency.               |
| `slack.retention_settings.v1` | `DCH-01`, `DCH-03` | DCH-01 = Data Protection; DCH-03 = Retention (the data-retention-duration posture itself).                                           |

**Considered and rejected:** a privacy anchor (`PRI-04`) for retention — the
retention setting is a data-handling/retention control, not a privacy-rights
control; DCH-03 is the precise anchor. Bot 2FA reported `inconclusive` (not
`fail`) so a workspace's service-account bots never drag its access posture to
fail — they authenticate via tokens, not 2FA.

## D3 — Scope minimum: `tenant_workspace = slack:<TEAM_ID>`

AWS scopes on `cloud_account` + `environment`. Slack has no cloud-account or
environment analog at the workspace grain; the single load-bearing scope
dimension is the **workspace (team) id**. Chosen key `tenant_workspace` with
value shape `slack:<TEAM_ID>` (vendor-prefixed, mirroring AWS's `aws:<ACCOUNT_ID>`).
The connector fails loudly (`slackauth.Resolve`) when the team id cannot be
resolved, rather than emitting an un-scoped record — the slice-004 "never emit
un-scoped records" invariant.

**Considered and rejected:** adding an `environment` dimension. A Slack
workspace is not partitioned by environment the way an AWS account is; forcing
an environment flag would be a false dimension. One honest scope dimension
beats two where one is invented.

## D4 — Stable optional fields + idempotency anchoring

Mirrors slice 004: hour-truncated `observed_at`, empty `session_id` (a per-call
UUID would break dedup-retry hash stability), `actor_id` =
`connector:slack:<service>@<version>`.

- **Member / retention** idempotency anchors on a stable per-entity id +
  hour-truncated observed_at (`idem.Key`) — two same-hour runs collapse.
- **Audit** idempotency anchors on the immutable Slack audit `entry_id` +
  `date_create` (`idem.EventKey`) — an audit entry is a point-in-time fact, not
  an hourly-resampled state, so it dedups on its own id across runs and hour
  boundaries. This is the one deliberate divergence from the AWS hour-window
  shape, and it is correct: re-observing the same historical login event must
  not write a new record every hour.

Per-`service` actor ids (`members` / `auditlogs` / `retention`) keep the three
kinds independently traceable to their source surface (repudiation —
threat-model R).

## D5 — Over-collection enforced structurally, not just documented (threat-model I)

The primary Slack risk is over-collection. Rather than rely on the scope
documentation alone, the boundary is enforced by code shape + tests:

1. The `slackapi` adapter calls ONLY `team.info`, `users.list`,
   `/audit/v1/logs`, and `team.preferences.list` — there is no call site for
   `conversations.history`, `search.messages`, or any message-reading endpoint.
2. The response decoders decode only metadata fields; a message body in a Slack
   response is never read into any struct field (`TestListMembers_MetadataOnly`
   proves an extra `last_message` field in the response is silently dropped).
3. `TestNoMessageContentField` (reflection) + `TestDoRun_NoMessageContentInPayload`
   (payload) fail the build if a content-bearing field/key is ever added.
4. The required-scope set excludes every message-read scope; `BannedScopes` +
   `TestScopeDisciplineExcludesMessageReads` document and assert it.

## D6 — Token handling: redacted-by-default (threat-model I / P0-443-4)

`slackauth.Token` is an opaque wrapper whose `String()` / `GoString()` return a
redaction marker, so any stray `%v`/`%s`/`%+v`/`%#v` on a token (the common
accidental-log shape) prints `Token(***redacted***)`, never the secret. The raw
value is reachable only via `Value()`, called at exactly one site: the outbound
Slack `Authorization` header. `TestTokenNeverLogged` enforces it across every
fmt verb and inside a wrapping struct. The token is never sent to the platform.

## Constitutional invariants honored

- **#3** — `profiles_supported=[pull]` describes source-side retrieval; the
  platform wire is push (`EvidenceIngestService.Push`). No platform-side wire
  change.
- **Evidence integrity** — sha256 content-hash is applied by the SDK push path;
  the connector supplies a deterministic idempotency key per record.
- **Anti-pattern: honest intervals** — README names the pull cadence (daily,
  scheduled) and explicitly disclaims "continuous monitoring" / event-driven.
- **Licensing** — OSS, in-tree, read-only API; no closed/proprietary collector.

## Follow-ons (NOT in this slice — P0-443-7)

Channel-membership granularity; SCIM provisioning-event evidence; an
event-driven profile via Slack audit-log streaming. None filed as spillover —
all are already named in the parent slice's "Follow-on slices" and roadmap;
filing duplicates would be noise.
