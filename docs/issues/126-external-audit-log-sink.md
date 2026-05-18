# 126 — External audit-log sink (tamper-evident retention outside the app)

**Cluster:** Infra (observability)
**Estimate:** 1.5d
**Type:** JUDGMENT
**Status:** `in-review`

## Narrative

Filed 2026-05-17 via `/idea-to-slice` as a spillover from slice 124 (unified audit-log aggregation API). The maintainer's feature ask was "every audit event visible in the app + written to an external sink for tamper-evident retention outside the app." Slice 124 ships the backend aggregation; slice 125 ships the in-app view; this slice ships the load-bearing tamper-evidence piece — pushing audit-log writes to a sink that the in-app admin cannot tamper with from inside the app.

This is a JUDGMENT slice. The engineer picks ONE of four sink mechanisms after weighing tradeoffs documented below, and records the rationale in `docs/audit-log/126-external-audit-log-sink-decisions.md`. Maintainer's lean (filed 2026-05-17) is **option (c) — OTel logs exporter via OTel Collector** because it composes with slice 121's atlas OTel SDK adoption; the engineer can disagree with documented justification.

### Sink mechanism options (engineer picks ONE)

| Option                                            | Mechanism                                                                                                       | Pros                                                                                                    | Cons                                                                                    |
| ------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| (a) JSONL to disk                                 | Each audit-log write emits one JSONL line to `/var/log/security-atlas/audit-log.jsonl` (rotation via logrotate) | Simplest; no new infra; readable                                                                        | In-app admin with shell access can tamper; not tamper-evident on its own                |
| (b) Syslog UDP/TCP                                | Each write emits to a maintainer-configured rsyslog/journald collector                                          | Common ops choice; zero new infra; centralizes                                                          | Operator must configure sink; UDP can drop                                              |
| (c) **OTel logs → Collector** (maintainer's lean) | Each write emits an OTel log record via the SDK; OTel Collector routes to the maintainer's choice of backend    | Composes with slice 121; uniform with traces+metrics; tenant_id as resource attribute is first-class    | Requires slice 121 merged (it's `ready`); OTel Collector adds an operational dependency |
| (d) S3-compatible cosigned object                 | Each write triggers a per-tenant per-day cosigned object append to S3                                           | Most paranoid; reuses audit-export-bundle infra (slice 030's pattern); cryptographically tamper-evident | Cost per write; latency on the audit-write path; complex to set up                      |

### Why "external" matters

The constitutional principle is: a malicious in-app admin can read + write the in-app audit log (slice 036's RLS allows tenant-write for admins). The external sink closes the loop by ensuring a copy lives in a place the admin cannot touch from within the app. Cryptographic tamper-evidence (option d) is the gold standard; the other options trade off how much the operator's broader infra (rsyslog, OTel Collector, etc.) is trusted as the tamper-resistant boundary.

## Threat model

| STRIDE                       | Threat                                                                                                                 | Mitigation                                                                                                                                                                                                             |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | Sink target receives forged audit records pretending to come from atlas                                                | AC: each record includes the atlas instance ID + a per-deployment HMAC (or cosign for option d). Receiver verifies.                                                                                                    |
| **T** Tampering              | **The whole point of the slice.** In-app admin tampers with in-app audit log; external sink must retain unaltered copy | AC: every audit-log write fans out to BOTH the in-app table AND the external sink. Per-record integrity proof (option-specific). Fan-out is asynchronous but at-least-once — backpressure logged, not silently dropped |
| **R** Repudiation            | Sink writes fail silently → repudiation gap                                                                            | AC: backpressure path emits ERROR to OTel + writes to a local fallback ledger (`audit_sink_failures` table — append-only) for ops investigation                                                                        |
| **I** Information disclosure | Sink leaks tenant A's rows to tenant B's downstream consumer                                                           | AC: per-tenant routing — sink config supports per-tenant destination OR includes `tenant_id` as a top-level attribute that downstream consumers can split on                                                           |
| **D** Denial of service      | Synchronous sink writes block the audit-write critical path                                                            | AC: fan-out is asynchronous via a buffered channel + dedicated writer goroutine; bounded buffer (default 10K records); on overflow, ERROR + fallback-table write (NOT drop)                                            |
| **E** Elevation of privilege | n/a — sink writer runs as service account; no new caller surface                                                       | n/a                                                                                                                                                                                                                    |

## Acceptance criteria

- [ ] AC-1: Engineer picks ONE sink mechanism from options (a)–(d) and records the choice + rationale in `docs/audit-log/126-external-audit-log-sink-decisions.md` BEFORE implementation. All four options + maintainer's lean are documented in the decisions log even for the unchosen ones.
- [ ] AC-2: New Go package `internal/audit/sink/` exposes `Emit(ctx, entry)` that fans out audit-log writes to the chosen sink. Package boundary is type-safe: `Entry` is the same canonical shape as slice 124's `unifiedlog.Entry`.
- [ ] AC-3: Every domain's audit-log INSERT site (in each of the 9 per-domain audit packages) calls `sink.Emit(ctx, entry)` AFTER the in-app INSERT succeeds. The sink Emit is non-blocking — buffered channel.
- [ ] AC-4: Sink writer goroutine handles backpressure: buffered channel (default 10000 records), on overflow ERROR log + INSERT to new `audit_sink_failures` table (append-only, four-policy RLS). NEVER silent-drop.
- [ ] AC-5: Per-record integrity proof — chosen mechanism-specific (HMAC for a/b/c, cosign for d). Receiver-side verification documented in the slice's README addition.
- [ ] AC-6: Configuration: `OTEL_AUDIT_SINK_*` (or analogous) env vars control sink target. Defaults: option-specific. Sink is opt-in at deployment (no-op when env unset, similar to slice 121's pattern).
- [ ] AC-7: Integration test — emit 100 records, assert all 100 reach the sink (mock per-mechanism) within 5s. For option (c), use an OTel Collector with in-memory exporter.
- [ ] AC-8: Backpressure integration test — fill buffer with 10001 records in tight loop, assert 10000 emitted + 1 written to `audit_sink_failures` table, no panic.
- [ ] AC-9: New migration `migrations/sql/_NNN_audit_sink_failures.sql` creates the fallback table with slice 036's four-policy append-only RLS pattern.
- [ ] AC-10: CONTRIBUTING.md or `docs/observability.md` updated with the sink configuration guide for operators.

## Dependencies

- **124** (unified audit-log aggregation API) — must merge first; this slice reuses the canonical `Entry` schema
- **121** (atlas OTel SDK) — IF maintainer's lean (option c) is picked; HARD dep in that case
- **117** (StepSecurity Harden-Runner) — soft synergy; the sink target is an outbound destination that Harden-Runner will surface in audit mode

## Anti-criteria (P0)

- **P0-A1**: Does NOT silent-drop on backpressure. Every record either makes the sink OR lands in `audit_sink_failures`.
- **P0-A2**: Does NOT block the in-app INSERT on the sink emit. Sink failure must not break the in-app write path.
- **P0-A3**: Does NOT use a sink mechanism that requires a credential the deployment doesn't already have (e.g., don't introduce a new cloud account for option d unless the existing S3 store works).
- **P0-A4**: Does NOT use vendor-prefixed test fixture tokens.
- **P0-A5**: Does NOT auto-pick a sink mechanism without the decisions log entry (AC-1 is load-bearing).

## Notes

- The unique value of this slice is the OUT-OF-APP property. The in-app `me_audit_log` (slice 108) meta-audit + slice 124's aggregator are the in-app side; this slice closes the loop by ensuring a copy lives somewhere the in-app admin cannot reach.
- For option (c) — composes with slice 121's `OTEL_EXPORTER_OTLP_ENDPOINT` configuration. The sink doesn't need new endpoints; it adds a logs exporter to the existing OTel SDK setup.
- For option (d) — the cosign-signed audit-export bundle infra (already used for audit-period close) is the template. Reuse the cosign keypair management.
- The receiver-side verification (AC-5) is what makes "tamper-evident" real. Document it; operators will need to know how to verify.
