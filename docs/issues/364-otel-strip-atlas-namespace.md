# 364 — Strip `atlas` Prometheus namespace from OTel Collector; enable resource→label conversion

**Cluster:** infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

The OTel Collector in `deploy/observability/` was scaffolded when the only producer was `security-atlas` itself. Its Prometheus exporter has `namespace: atlas` hardcoded (`deploy/observability/otel-collector-config.yaml:56`), so every metric series gets prefixed with `atlas_*`.

A second producer (Claude Code on the Mac Studio, `service.name=claude-code`) is now sending **traces** to the collector. Traces flow cleanly to Tempo because the traces pipeline doesn't touch the Prometheus exporter. If Claude Code **metrics** are ever turned on, they'd land in Prometheus as `atlas_claude_code_token_usage_total` — semantically wrong (Claude Code is not atlas) and the prefix stops being informative once the collector serves multiple producers.

**The fix.** Remove the namespace prefix and enable `resource_to_telemetry_conversion`, so every metric series gets the OTel resource attributes (`service.name`, `host.name`, `deployment.environment`) as Prometheus labels. Producers are then distinguished by `{service_name="..."}` filters rather than by metric-name prefix. This is producer-agnostic — atlas's Go SDK (slice 121) does NOT need any code changes. The metric names are determined at export time by the collector, not at generation time by the SDK.

**Scope discipline:** this slice is the **collector + dashboard config edit** only. The actual Unraid deploy (`rsync` to `/mnt/user/appdata/observability/` + `docker compose restart otel-collector` + Grafana panel verification) is post-merge maintainer action, NOT a CI gate for this slice. The slice's `## Deploy procedure` section documents what the maintainer runs; the slice merges when the file edits land + CI is green.

**Trigger:** Surfaced 2026-05-28 during the observability-refactor work session when Claude Code's OTel emit started flowing to the shared collector. Full blast-radius audit lives at `~/.claude/MEMORY/WORK/20260528-082000_otel-namespace-and-traces/PRD.md` — 16 `atlas_*` references identified across 4 files. No alert rules exist. No Go code changes required.

## Threat model

Collector-config edit; no auth surface, no user input handling, no new endpoints. STRIDE pass:

- **S (Spoofing):** CLEAN. No authentication boundary added or modified.
- **T (Tampering):** YAML config edits + JSON dashboard edits. Risk = malformed config → collector crash on restart. **Mitigation:** pre-deploy YAML/JSON lint in CI; AC-9 below requires `otelcol validate` (or equivalent) to pass against the edited config before merge. Roll-back is fast (<5 min) per the user's audit.
- **R (Repudiation):** CLEAN. No audit-log writes.
- **I (Information disclosure):** New label `service_name="security-atlas"` (and `host_name`, `deployment_environment` from `resource_to_telemetry_conversion`) becomes a Prometheus label. Label NAMES are not sensitive; label VALUES are public branding strings (`security-atlas`, `claude-code`). No tenant-scoped data crosses the collector boundary; no PII; no internal identifiers. CLEAN.
- **D (Denial of service):** `resource_to_telemetry_conversion` expands label cardinality (adds 3-5 resource attrs as labels per series). Back-of-envelope: ~50 atlas metric series × ~5 producers × ~5 resource attrs ≈ 1,250 active series — well within a single-host Prometheus's working range (millions). Future producers with high-cardinality attrs (e.g. `host_name` exploding across a fleet) would need a `metric_relabel_configs` drop rule, but is out-of-scope for v0. CLEAN.
- **E (Elevation of privilege):** CLEAN. No role check added or modified.

**Threat-model verdict:** CLEAN with one mitigation requirement (AC-9 config validation).

## Acceptance criteria

### Code edits

- [ ] **AC-1.** `deploy/observability/otel-collector-config.yaml` Prometheus exporter block has `namespace: atlas` removed and `resource_to_telemetry_conversion.enabled: true` added. Comment block above the exporter explains the multi-producer rationale.
- [ ] **AC-2.** `deploy/observability/grafana-datasources.yaml` lines 68 and 70 (`tracesToMetrics` queries) updated: - Strip `atlas_` prefix from `atlas_http_server_duration_count` - Scope each query to `service_name="security-atlas"` - Preserve the existing `$$__tags` selector inside curly braces
- [ ] **AC-3.** `deploy/observability/dashboards/security-atlas-overview.json` — 9 panel queries (lines 100, 118, 123, 128, 145, 161, 187, 192, 208) rewritten from `atlas_<metric>` to `<metric>{service_name="security-atlas"}`. Preserve any existing label selectors (e.g. `http_status_code=~"5.."`, `type="heap"`) by merging the new `service_name` filter into them, NOT replacing them.
- [ ] **AC-4.** `deploy/observability/prometheus.yml` two atlas references updated: - Lines 27-28: explanatory comment updated to remove the obsolete "with the `atlas_` namespace prefix" claim. Replace with the multi-producer narrative. - Line 32: the dormant `metric_relabel_configs` comment example (`atlas_http_server_duration_seconds → atlas_http_server_duration`) updated to use post-namespace-strip metric names for accuracy. The relabel rule itself remains commented out (no behavior change).
- [ ] **AC-5.** `deploy/observability/README.md` lines 104 and 176 — two example curl commands rewritten from `atlas_http_server_duration_count` to `http_server_duration_count{service_name="security-atlas"}` (URL-encoded curly braces for shell-safe `curl` invocation).

### Documentation

- [ ] **AC-6.** Slice ships a `## Deploy procedure` block in `docs/audit-log/364-otel-strip-atlas-namespace-decisions.md` that documents the post-merge maintainer steps verbatim: 1. `rsync` updated `deploy/observability/` to `/mnt/user/appdata/observability/` on Unraid (192.168.1.246) 2. `ssh root@192.168.1.246 'docker compose -f /mnt/user/appdata/observability/docker-compose.yml restart otel-collector'` 3. Health: `curl http://192.168.1.246:13133/` returns 200 4. New label surfaces: `curl 'http://192.168.1.246:9090/api/v1/query?query=http_server_duration_count{service_name="security-atlas"}'` → non-empty `result` 5. Old name absent: same URL with `query=atlas_http_server_duration_count` → empty `result` 6. Grafana panel render-check (path TBD by AC-7)
- [ ] **AC-7.** Decisions log captures the dashboard-provenance investigation result. Engineer SSHes to Unraid (or notes the SSH is gated to maintainer-only), runs `ls /mnt/user/appdata/Grafana/provisioning/dashboards/`, and documents which of 4 outcomes applies (auto-provisioning / different path / manual UI import / not actually loaded), plus the reload mechanism for each. This is informational — it does NOT block the slice merge. If the engineer cannot SSH from the worktree environment, documents the open question for the maintainer to resolve at deploy time.
- [ ] **AC-8.** `CHANGELOG.md` Unreleased `### Changed` bullet added: "OTel Collector: stripped `atlas` namespace; producers now distinguished by `service_name` label. Migration: dashboards and PromQL queries that reference `atlas_*` must be updated to `<metric>{service_name="security-atlas"}` after deploy."

### Verification

- [ ] **AC-9.** Edited `otel-collector-config.yaml` validates cleanly. Either: - Run `docker run --rm -v $(pwd):/cfg otel/opentelemetry-collector-contrib:0.130.0 validate --config=/cfg/deploy/observability/otel-collector-config.yaml` and assert exit 0 - OR validate the YAML structure passes the existing `actionlint`/`yamllint`-equivalent step (if one exists) - OR rely on the otel-collector docker image at deploy time (test-on-deploy with explicit roll-back commitment). The engineer makes the JUDGMENT call in D1.
- [ ] **AC-10.** Edited `security-atlas-overview.json` parses as valid JSON: `jq '.' deploy/observability/dashboards/security-atlas-overview.json > /dev/null` returns 0. This prevents a typo from breaking dashboard import.
- [ ] **AC-11.** Edited `grafana-datasources.yaml` and `prometheus.yml` parse as valid YAML: `python3 -c "import yaml; yaml.safe_load(open('<file>'))"` returns 0 for each.
- [ ] **AC-12.** `pre-commit run --files <all touched paths>` passes (prettier may reformat the JSON; that's expected — re-run per project convention).

## Constitutional invariants honored

- **Audit-period freezing (canvas §8.4):** unaffected — observability metrics are operational telemetry, not evidence-stream data, and never feed AuditPeriod evidence selection.
- **Tenant isolation (RLS, invariant #6):** unaffected — observability metrics are infrastructure-level (per-host, per-service), not tenant-scoped. No collector pipeline touches `app.current_tenant`.
- **Evidence SDK (canvas §4.1, invariant #3):** unaffected — the Prometheus exporter is for human-facing operator dashboards, not the evidence push surface.

## Canvas references

- `Plans/canvas/09-tech-stack.md` "Observability" row — OTEL native (traces + metrics + logs); default docker-compose bundles Prometheus + Grafana + Tempo + Loki.

## Dependencies

- **#121** (atlas-otel-sdk) — `merged`. The producer side. This slice changes the collector-side metric naming; the SDK side is invariant.

## Anti-criteria (P0 — block merge)

- **P0-364-1.** Does NOT modify any code in `internal/`, `cmd/`, `pkg/`, or `web/`. This is a deploy/observability-only slice. The atlas Go SDK and emit code are untouched (P0 confirms the producer-agnostic claim in the narrative).
- **P0-364-2.** Does NOT modify `deploy/observability/tempo.yaml` or `deploy/observability/docker-compose.yml`. Traces pipeline + container topology stay invariant.
- **P0-364-3.** Does NOT add or modify any Prometheus alert rule. The user's audit confirmed no alert rules currently reference `atlas_*`; this slice does not introduce alerts as a side effect.
- **P0-364-4.** Does NOT touch CLAUDE.md or `Plans/canvas/*`. (The observability stack choice is settled in the canvas; this slice is a parameter tweak, not a stack change.)
- **P0-364-5.** Does NOT change the `otel-collector` Prometheus scrape job name (`otel-collector-app-metrics`) or the scrape endpoint (`otel-collector:8889`). Job-name churn would break the Grafana data-source binding, which is out of scope.
- **P0-364-6.** Does NOT enable a `metric_relabel_configs` rule that wasn't already enabled before the slice. The dormant unit-suffix-strip example in `prometheus.yml` line 32 stays commented out; only its comment text updates for accuracy.
- **P0-364-7.** Does NOT bundle the Claude Code `OTEL_METRICS_EXPORTER` flip from `"none"` to `"otlp"`. That's a separate post-deploy `~/.claude/settings.json` edit, NOT a security-atlas-repo change.
- **P0-364-8.** Does NOT cause an alert-storm at deploy time. The collector restart drops ~1 second of in-flight spans; the maintainer schedules during a low-activity window per the user's coordination note.

## Skill mix

- YAML editing (collector config + Prometheus scrape config + Grafana data-source provisioning)
- JSON editing (Grafana dashboard panels — preserve panel structure, only rewrite `expr` strings)
- PromQL idiom (label-selector merging — keep `$__tags`, `http_status_code=~"5.."`, etc. when adding `service_name`)
- Markdown documentation (decisions log + CHANGELOG bullet)
- Optional: docker exec + `otelcol validate` if the engineer chooses AC-9 path (a) over (b) or (c)

## Notes for the implementing agent

### Phase-0 grill output (recorded here so engineer sees the design context)

- **Domain model:** `service_name` (snake_case Prometheus label) corresponds to `service.name` (dot-attribute) per the OTel-to-Prometheus translation convention. `resource_to_telemetry_conversion` performs the dot-to-underscore mapping; the engineer does not need to configure the mapping rules.
- **Scope:** single coherent slice (collector + dashboard + scrape + docs are interdependent — changing one without the others leaves Grafana broken or empty). Do NOT split per file.
- **Already-built check:** slice 121 (`atlas-otel-sdk`) is the SDK side. This slice is the collector side. They compose; no rework of 121 is needed.
- **Hidden finding:** `prometheus.yml` line 32 inside the dormant `metric_relabel_configs` block also references `atlas_*` metric names in an explanatory comment. Easy to miss in the 5-file audit because the relabel itself is commented out. AC-4 explicitly covers it.

### Phase-3 threat-model output (anti-criteria added above)

- AC-9 (config validation) is the load-bearing mitigation against a typo crashing the collector at deploy. The engineer picks the JUDGMENT call (a/b/c) and documents in D1.
- Cardinality math justifies `resource_to_telemetry_conversion=true` without a `metric_relabel_configs` cardinality guard at this scale. A future slice MAY add a guard when fleet-scale (host_name explosion) becomes a concern.

### Deploy procedure

The slice MERGES when CI is green and the 5 file edits are reviewed. The maintainer then runs:

```bash
# 1. Sync to Unraid
rsync -av deploy/observability/ root@192.168.1.246:/mnt/user/appdata/observability/

# 2. Restart the collector (drops ~1s of in-flight spans)
ssh root@192.168.1.246 'docker compose -f /mnt/user/appdata/observability/docker-compose.yml restart otel-collector'

# 3. Health
curl http://192.168.1.246:13133/    # → 200

# 4. New label surfaces
curl 'http://192.168.1.246:9090/api/v1/query?query=http_server_duration_count{service_name="security-atlas"}'
# → non-empty .data.result

# 5. Old name absent
curl 'http://192.168.1.246:9090/api/v1/query?query=atlas_http_server_duration_count'
# → empty .data.result

# 6. Grafana panel render check
# Open the security-atlas-overview dashboard in Grafana (path TBD by AC-7 investigation)
```

### Roll-back

If anything fails post-deploy (collector won't start, dashboards empty after AC-7 reload, etc.):

```bash
# In repo
git revert <slice-364-merge-commit>
git push

# On Unraid
ssh root@192.168.1.246
rsync -av <previous-deploy-observability> /mnt/user/appdata/observability/
docker compose -f /mnt/user/appdata/observability/docker-compose.yml restart otel-collector
```

Total roll-back time: <5 min. The change is producer-agnostic and reversible.

### Background references

- Blast-radius audit: `~/.claude/MEMORY/WORK/20260528-082000_otel-namespace-and-traces/PRD.md`
- Originating prompt: `~/.claude/MEMORY/WORK/20260528-082000_otel-namespace-and-traces/atlas-namespace-backlog-prompt.md`
- Slice 121 (atlas-otel-sdk): the SDK that emits the metrics this slice re-labels at the collector.
- New trace producer: `service.name=claude-code` (Claude Code on Mac Studio) — currently traces-only via `OTEL_METRICS_EXPORTER=none`. After this slice ships + deploys, the flip to `"otlp"` in `~/.claude/settings.json` is safe.
