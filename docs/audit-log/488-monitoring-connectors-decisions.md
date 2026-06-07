# 488 — Monitoring connectors (Datadog + Grafana): JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
shared-vs-split evidence-kind decision, the evidence-kind field shape, the
`x-default-scf-anchors`, the per-connector scope minimums, the stable-field
choices, and the dependency choice). It does NOT block merge — the maintainer
iterates post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the build. One build-time correction was made: the shared
helper packages were initially placed under `connectors/monitoring/internal/`,
which Go's internal-package rule forbids importing from sibling
`connectors/datadog` / `connectors/grafana`; they were moved to
`connectors/monitoring/{alertcfg,idem,monrecord}`. This was a packaging-layout
fix caught at compile time, not a defect in shipped behavior.)

## Decisions made

### D1 — Shared single evidence kind `monitoring.alert_config.v1` (NOT split per connector)

- **Options considered:** (a) one shared `monitoring.alert_config.v1` kind across
  both vendors; (b) split `datadog.monitor.v1` + `grafana.alert_rule.v1`.
- **Chosen:** (a), the shared kind. This is the explicit JUDGMENT call the slice
  flagged ("prefer one shared shape if the field set is genuinely identical;
  split only if the two vendors' alert models diverge enough to make a shared
  shape lossy").
- **Rationale:** both vendors' alert configuration reduces to the identical
  config-inventory tuple `{rule_id, rule_name, rule_type, enabled,
notification_targets[]}` where each target is `{target_kind, target_name}`.
  Datadog "monitor" and Grafana "alert rule" answer the exact same control
  question (CC7.2 / MON-01: monitoring is configured and routes to on-call). A
  `source_vendor` discriminator (`datadog` | `grafana`) preserves provenance
  without forking the schema. The lossy-test fails for the split option — there
  is no field one vendor has that the shared shape cannot represent at the
  config-inventory altitude — so the shared shape wins, and the platform
  evaluator sees one uniform monitoring evidence family instead of two
  near-duplicate schemas. The shared record builder lives once in
  `connectors/monitoring/monrecord`.
- **Confidence:** high.
- **Revisit once in use:** if a future vendor (or a v2 Datadog SIEM-rule /
  Grafana-RBAC follow-on) introduces an alert model whose auditable fields don't
  fit `{rule, type, enabled, targets}`, split THAT surface into its own kind
  rather than widening this one into a lowest-common-denominator blob. The
  follow-ons (slices 533–535) are filed precisely so this kind stays focused.

### D2 — `x-default-scf-anchors = ["MON-01"]`

- **Chosen:** `MON-01` (SCF "Continuous Monitoring": "Mechanisms exist to
  facilitate the implementation of enterprise-wide monitoring."). MON-01 is the
  on-point anchor for "monitors/alerts are configured" and is the SCF crosswalk
  target for SOC 2 CC7.2/CC7.3. The existing `github.audit_event.v1` kind already
  anchors to the MON family (`["MON-01","MON-02"]`), so this keeps the monitoring
  evidence family consistent.
- **Why a single anchor, not MON-01 + MON-08:** MON-08 ("Anomalous Behavior
  Detection") is about detection mechanisms, not about _whether monitoring is
  configured_; this evidence kind proves the latter, so a single, precise anchor
  reads cleaner than padding the list. (The schema's `x-default-scf-anchors` is
  advisory connector-side default metadata — the drift guard does not validate it
  against the catalog — so the maintainer can broaden it later without a schema
  bump.)
- **Confidence:** medium. **Revisit once in use (OQ #9 — load-bearing):** the
  maintainer should re-check anchor accuracy against the full SCF→SOC 2 STRM
  crosswalk and decide whether to add a CC7.3/CC7.4-aligned anchor.

### D3 — Per-connector scope minimums (read-only least privilege)

- **Datadog → an API key + an Application key scoped EXACTLY `monitors_read`.**
  The Application key carries Datadog's authorization scopes; `monitors_read` is
  the single read-only scope that lists monitors. The API key is org-level (no
  scope) and is required alongside the Application key. Documented as the minimum;
  a unit test pins `RequiredScope == "monitors_read"` and rejects write/admin.
- **Grafana → a service-account token with EXACTLY the `Viewer` role.** Viewer can
  list alert rules + contact points read-only; the connector has no write path. A
  unit test pins `RequiredRole == "Viewer"`.
- **Rationale:** threat-model E (elevation of privilege) — the documented minimum
  must be the smallest read-only grant, and the `permissions` subcommand surfaces
  it so an operator never over-grants "to be safe."
- **Confidence:** high.

### D4 — Stable field shapes + result semantics

- **Payload fields:** `source_vendor`, `rule_id`, `rule_name`, `rule_type`,
  `enabled` (required); `folder`, `notification_targets[]` (optional, omitted when
  empty — the slice-004 stable-optional-field convention). Each target is
  `{target_kind, target_name}`.
- **`rule_type` is a descriptive string, not an enum,** so a new vendor rule type
  (Datadog "log alert" / "trace-analytics alert"; Grafana "datasource" rule) does
  not require a schema bump.
- **`enabled`** is the normalized active state: Datadog = "not silenced", Grafana
  = "not paused".
- **Result is always `INCONCLUSIVE` (descriptive).** The connector reports the
  configuration (enabled state + routing); the platform evaluator owns the
  pass/fail call per (control, scope) — consistent with the slice-487 RBAC kind.
- **`observed_at`** is truncated to the UTC hour; the idempotency key is
  `sha256("monitoring.alert_config|<vendor>/<rule_id>|<hour>")` so same-rule
  re-runs within the hour collapse to one ledger row.
- **`actor_id`** follows `connector:<vendor>:<service>@<version>` —
  `connector:datadog:monitors@…` and `connector:grafana:alerts@…`.
- **Scope minimums:** `service` (constant `datadog` / `grafana`) + `environment`
  (required flag). `run` refuses to push an un-scoped record.
- **Confidence:** high.

### D5 — The over-collection guard (P0-488-3) — collect target NAMES, never secrets

- **The dominant risk** (threat-model I) is copying a secret-bearing notification
  config (webhook URL, integration token, recipient email PII) into an evidence
  record. The guard is enforced **by construction**, not only by test:
  - `alertcfg.Target` has only `{Kind, Name}` — there is no field to put a secret
    in.
  - Grafana's `ContactPoint` struct has **no `settings` field**; the client never
    decodes the provisioning API's `settings` blob (where the webhook URL / token
    / recipient address live), so secrets never enter memory as connector data.
  - Datadog's client never decodes the monitor `query` / `options` blob (the
    query can embed sensitive tag values); it reads only id/name/type/message/
    enabled, and the message is used **only** to parse `@handle` mentions.
  - The Datadog handle parser **drops `@user@example.com` email-recipient
    mentions** — a raw recipient email is PII, not a routing handle.
- **Tests** additionally assert (belt-and-braces) that no secret URL / token /
  recipient email substring reaches an emitted payload, and that neither
  connector's credential reveals its key/token on any format path (AC-10 / AC-11).
- **Confidence:** high.

### D6 — Dependency choice: thin read-only HTTP clients (no vendor SDKs)

- **Chosen:** hand-rolled thin `net/http` clients for both vendors (mirroring
  slice 486/487's avoidance of heavy cloud SDKs / `client-go`). No new module
  dependency is added; `go mod tidy` is clean.
- **Rationale:** the connectors issue a handful of read-only GETs against stable
  vendor API surfaces. Pulling the official Datadog / Grafana Go SDKs would add
  large transitive dependency trees for no benefit and would make the secret-free
  decode boundary harder to audit (the SDKs decode the full response including the
  secret-bearing fields). A struct with only the secret-free fields is the
  clearest possible over-collection guard.
- **Confidence:** high.

### D7 — Shared helper packages are NON-internal under `connectors/monitoring/`

- **Chosen:** `connectors/monitoring/{alertcfg,idem,monrecord}` (importable by
  both sibling connectors), not `connectors/monitoring/internal/...`.
- **Rationale:** Go's internal-package rule scopes `internal/` to the subtree
  rooted at its parent; `connectors/datadog` and `connectors/grafana` are siblings
  of `connectors/monitoring`, so they cannot import its `internal/`. Per-vendor
  packages that genuinely should stay private (`datadogauth`, `monitors`,
  `grafanaauth`, `alertrules`) remain under each connector's own `internal/`.
- **Confidence:** high.

## Revisit-once-in-use summary (for the maintainer)

1. **D2 / OQ #9 (load-bearing):** re-check `x-default-scf-anchors = [MON-01]`
   against the full SCF→SOC 2 STRM crosswalk; consider a CC7.3/CC7.4 anchor.
2. **D1:** if a follow-on vendor surface (Datadog SIEM rules, Grafana RBAC) has
   an alert model that doesn't fit `{rule, type, enabled, targets}`, give it its
   own kind rather than widening this one.
3. **Pagination:** v0 reads a bounded first page per endpoint (Datadog
   `page_size=1000`; Grafana provisioning returns all rules). A large org with
   thousands of monitors/rules needs cursor pagination — filed as a refinement.
