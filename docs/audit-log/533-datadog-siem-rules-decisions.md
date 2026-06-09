# 533 — Datadog Cloud-SIEM detection-rule evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls for
slice 533 — the sibling-kind-vs-reuse decision, the evidence-kind shape (what
the `datadog.siem_rule.v1` payload carries and how the Datadog read is
structured), the scope-minimum + structural over-collection-guard mechanism, the
stable-field choices (idempotency key, actor_id, scope, default SCF anchors), and
the anchor choice. It does NOT block merge; the maintainer iterates
post-deployment from the "Revisit once in use" notes.

Parent: slice 488 (`docs/audit-log/488-monitoring-connectors-decisions.md`).
Slice 488 shipped the base Datadog connector with one evidence surface
(`monitoring.alert_config.v1` — operational monitor inventory) and explicitly
deferred Cloud-SIEM detection rules as a follow-on (P0-488-7). This slice ships
that surface.

## D1 — Sibling kind `datadog.siem_rule.v1`, NOT reuse of `monitoring.alert_config.v1`

- **Options considered:** (a) reuse the shared `monitoring.alert_config.v1` kind
  (treat a detection rule as just another "rule" with `source_vendor=datadog`);
  (b) a new sibling kind `datadog.siem_rule.v1`.
- **Chosen:** (b), the sibling kind. This is the exact JUDGMENT the slice flagged
  and that slice-488 D1 reserved: _"if a follow-on vendor surface (Datadog SIEM
  rules) introduces an alert model whose auditable fields don't fit
  `{rule, type, enabled, targets}`, split THAT surface into its own kind rather
  than widening this one into a lowest-common-denominator blob."_
- **Rationale (the lossy test, applied):** a Cloud-SIEM detection rule carries
  two audit-relevant fields the operational alert-config shape has no slot for:

  1. **`severity`** — the highest case severity the rule can produce
     (info/low/medium/high/critical). This is the load-bearing field for the
     SOC 2 CC7.2/CC7.3 + ISO A.12 "threat-detection rules are configured AND
     appropriately tiered" evidence question. `monitoring.alert_config.v1` has no
     severity field, and bolting one onto the shared kind would force the Grafana
     connector and the Datadog _monitor_ surface to carry a field that is
     meaningless for them.
  2. **`detection_class`** — log vs signal*correlation vs threshold. A detection
     rule's \_class* is the thing an auditor reads to confirm the rule actually
     detects (a correlation rule chaining signals is a categorically different
     control than a monitor firing on a metric threshold). The alert-config
     `rule_type` field is a vendor-native operational type string
     (`metric alert` / `log alert`), a different semantic axis.

  Forcing both into the shared kind fails the slice-488 lossy test: there ARE
  fields one surface has that the shared shape cannot represent at its altitude.
  Conversely the two surfaces answer _different_ control questions — MON-01
  "monitoring is configured" vs THR-01 "threat-detection rules are configured" —
  so a single evaluator-facing kind would conflate two distinct evidence
  families. The split keeps each kind focused, exactly the slice-488 D1
  guidance.

- **Reconsidered honestly:** could the shared kind absorb this with two optional
  fields (`severity`, `detection_class`)? It could _physically_, but that
  re-introduces the lowest-common-denominator blob D1 warns against and couples
  three connectors (Datadog monitors, Datadog SIEM, Grafana) to one schema's
  churn. The split is the cleaner, lower-coupling call. **Confidence: high** —
  this is the textbook case D1 was written for.
- **Revisit once in use:** if a _second_ SIEM vendor (e.g. a future Splunk/Sentinel
  connector) lands a detection-rule surface with the identical
  `{rule_id, name, detection_class, enabled, severity, targets}` tuple, promote
  `datadog.siem_rule.v1` to a shared `siem.detection_rule.v1` with a
  `source_vendor` discriminator — the same shared-shape move slice 488 made for
  monitors. Do NOT pre-build that abstraction now (one vendor = no shared shape
  to factor).

## D2 — Default SCF anchors `[MON-01, THR-01]`

- **Chosen:** `["MON-01", "THR-01"]`, as the slice directed. MON-01 ("Continuous
  Monitoring") anchors the "detection is configured and operating" half (it is
  the SCF crosswalk target for SOC 2 CC7.2/CC7.3, and the existing monitoring
  evidence family already anchors there — `monitoring.alert_config.v1` uses
  `[MON-01]`, keeping the family consistent). THR-01 ("Threat Intelligence /
  Threat-Detection Program") is the on-point second anchor that distinguishes a
  _detection_ rule from an operational monitor — it is precisely why this kind is
  a sibling rather than a reuse (D1).
- **Catalog-gap caveat (honest):** the bundled SCF sample catalog seeded today
  (`migrations/fixtures/scf-sample.json` / `internal/api/scfseed`) does NOT yet
  contain a `THR-01` anchor row (it has the MON and MON-08 anomalous-behavior
  anchors but no THR domain). `x-default-scf-anchors` is **advisory
  connector-side default metadata** (slice-488 D2 established this — the
  evidence_kind drift guard validates `x-evidence-kind` bijection, NOT the anchor
  list against the catalog), so a not-yet-seeded anchor does NOT break any test
  and the schema-drift bijection passes. I followed the slice's explicit
  `[MON-01, THR-01]` directive rather than substituting; the seeded on-point
  alternative for the detection half would be **MON-08** ("Anomalous Behavior
  Detection") if a maintainer prefers an anchor that resolves in today's sample
  catalog. Filed as spillover **slice 635** (seed the THR domain / remap the
  advisory anchor) so the advisory metadata resolves against the catalog when the
  full SCF import lands. **Confidence: medium** on THR-01 specifically (directive
  followed, catalog gap noted); **high** on MON-01.

## D3 — Over-collection guard: structural (the struct cannot hold the excluded data)

- **Mechanism:** the collector's secret-free structs (`siemrules.RawRule`,
  `siemrules.Rule`) have NO field capable of holding a firing signal, the
  detection query's matched raw log samples, a matched-event payload, the secret
  webhook URL behind a notification, an integration token, a recipient email, or
  the raw detection query. The Datadog v2 client (`apiRule`) decodes ONLY
  `id / attributes.name / attributes.type / attributes.isEnabled` plus, per case,
  the `status` (severity) and the `notifications` handle list — `json.Decode`
  discards every other JSON key (the `queries`, the case `condition`/`filter`,
  the `options.notificationWebhook`, any signals) because there is no struct
  field to receive them. If the struct physically cannot hold the excluded data,
  that IS the guard.
- **Pinned by a reflection test.** `TestStructuralOverCollectionGuard`
  (`siemrules_test.go`) reflects over `RawRule`, `Rule`, and `Target` and fails
  if any field is added outside an allow-list OR if any field name contains a
  banned over-collection substring (`signal`, `sample`, `event`, `payload`,
  `query`, `log`, `secret`, `token`, `url`, `webhook`, `condition`, `filter`,
  …). This mirrors the slice-488 / 520 / 614 structural-guard pattern. A
  client-level test (`TestClient_ListRules_DecodesSecretFreeFields`) additionally
  feeds a payload embedding a detection query + a case condition + a webhook URL
  and asserts none survive into `RawRule`. A builder-level test
  (`TestBuild_PayloadIsConfigOnly`) + a bufconn integration test
  (`TestSIEMEmittedRecords_NoSecretsOrPII`) assert no banned substring reaches an
  emitted record's payload, and that an `@user@example.com` recipient mention is
  dropped (PII never becomes a target).

## D4 — Scope minimum + read-only / GET-only / no-new-wire-surface

- **Source scope minimum:** read-only `security_monitoring_rules_read` (the new
  `datadogauth.RequiredSIEMScope`), documented in `atlas-datadog permissions` and
  the README as the exact minimum, with an explicit ban on
  `security_monitoring_rules_write` / admin. The source keys stay source-side
  (read from env, never a flag), are redacted on every `Credential` format path
  (slice-488 `TestCredential_NeverLogged` covers both surfaces since the
  credential is shared), and never enter a record.
- **GET-only:** the client issues only `http.MethodGet` against
  `/api/v2/security_monitoring/rules`; `TestClient_ListRules_DecodesSecretFreeFields`
  asserts the method is GET.
- **No new platform-side wire surface (invariant #3):** the SIEM records flow
  through the same single `EvidenceIngestService.Push` RPC as every other kind.
  `profiles_supported` stays `[pull]`. No proto change, no new endpoint.

## D5 — Stable-field choices

- **`actor_id`:** `connector:datadog:siemrules@<version>` via the existing
  `actorID("siemrules")` helper — the cross-connector
  `connector:<vendor>:<service>@<version>` convention, distinct from the monitor
  surface's `connector:datadog:monitors@<version>` so the two surfaces are
  attributable apart.
- **Idempotency key:** `sha256("datadog.siem_rule|<rule_id>|<UTC-hour>")` — the
  rule_id + hour-truncated `observed_at` collapse same-rule re-runs within the
  hour to one ledger row, mirroring the monitoring family's `idem.AlertConfigKey`
  shape but scoped to this kind (a per-kind prefix avoids any collision with the
  alert-config keyspace).
- **`observed_at` granularity:** truncated to the UTC hour (same as the monitor
  surface), documented as the dedup window.
- **`severity` = highest case status.** A Datadog rule has N cases each with its
  own status; the audit-relevant signal is "how serious can this rule's signals
  get", so the client reports the _highest_ case severity across the rule's
  cases (`severityRank` ordering info<low<medium<high<critical). Empty defaults
  to `info`.
- **`detection_class` normalization.** `log_detection`→`log`,
  `signal_correlation`/`correlation`→`signal_correlation`,
  `threshold`/`anomaly_detection`/`impossible_travel`/`new_value`→`threshold`; an
  unrecognized-but-non-empty class is preserved verbatim (descriptive string, not
  strictly enumerated) so a new Datadog rule kind does not force a schema bump.
- **`Result` = INCONCLUSIVE.** The connector reports descriptive configuration;
  the platform evaluator owns the pass/fail per (control, scope) — same posture
  as every config-inventory connector.

## D6 — Bounded read (DoS guard)

- The Datadog v2 rules API is cursor-paginated. The client reads a bounded
  cursor loop with a hard per-run page cap (`maxPages=50` × `pageSize=100` ⇒
  5,000 rules) and a 60s run-wide timeout. A source that never terminates its
  cursor stops with `ErrRuleCapExceeded` (covered by
  `TestClient_ListRules_CapTerminates`) rather than reading unbounded —
  paginate-up-to-the-cap-then-report-honestly, the same DoS posture as the Azure
  firewall surface. The monitor surface (slice 488) reads a bounded first page;
  this surface paginates because a mature SIEM estate can carry many hundreds of
  detection rules.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** unit. One build-time issue surfaced during the slice
  and was caught at the unit tier by `go test` before push: extending `doRun` to
  also collect SIEM rules made the existing slice-488 monitor-path seam tests
  fail (`TestDoRun_PushSuccess` hit a live `/api/v2/security_monitoring/rules`
  endpoint → HTTP 401), because the SIEM collector was not seamed. Caught
  immediately by the existing cmd test suite; fixed in the SAME change by adding a
  `siemCollect` seam (defaulting to a `noSIEM` no-op stub) so the monitor-path
  tests never reach a live SIEM endpoint.
- **detection_tier_target:** unit. The seam-coverage gap is exactly the kind of
  thing the cmd unit tier exists to catch, and it did — no escape to a higher
  tier. No integration/production escape.
- No defect escaped to `integration`, `playwright`, `manual_review`, or
  `production`.
