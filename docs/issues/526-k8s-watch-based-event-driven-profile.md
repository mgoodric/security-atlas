# 526 — Kubernetes connector: watch-based event-driven profile (audit log)

**Cluster:** Connectors
**Estimate:** L (3-5d)
**Type:** JUDGMENT (profile shape + dedup + stable-field choices)
**Status:** `blocked` (depends on #487 — base Kubernetes connector — merged first)

## Narrative

Slice 487 shipped the base Kubernetes connector on the **pull** profile only —
each invocation is one bounded read-and-push pass, operator-scheduled (recommended
24h), named honestly. This slice adds the **event-driven (subscribe) profile**:
react to RBAC / workload changes as they happen by consuming the Kubernetes audit
log (or a `watch` against the same read-only API surfaces), so a binding edit or a
privileged-pod rollout produces evidence in near-real-time instead of waiting for
the next pull.

This is the slice-487 pattern extended with a second profile: the connector
registers `profiles_supported=[pull, subscribe]`; the platform-side wire stays
push (invariant #3) regardless. The subscribe path emits the SAME two evidence
kinds (`k8s.rbac_binding.v1`, `k8s.workload_security_context.v1`) — no new schema —
so a downstream evaluator does not care which profile produced a record.

**Honest-interval discipline.** Even the subscribe profile names its mechanism
honestly: it is "event-driven via the Kubernetes audit log / watch", NOT
"continuous monitoring" marketing. Where the audit log is unavailable (managed
clusters that do not expose it), the connector falls back to a short `watch`
re-list and says so.

**Scope discipline.** Same two evidence kinds, same read-only ClusterRole (a
`watch` verb may be added alongside `get,list` — documented; still no write, no
`secrets`). It does NOT add new evidence surfaces (those are 523 / 524 / 525). It
does NOT introduce an inbound platform API (invariant #3).

## Threat model

Inherits the slice-487 connector-family threat model; the new surface is the
audit-log consumption path.

- **S — Spoofing.** Reuses the existing connector push credential; the cluster
  ServiceAccount may gain a `watch` verb on the existing resources (no new
  resource). The credential stays source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates it.
- **R — Repudiation.** Each event-driven record still carries a stable
  `actor_id` + `observed_at` (the event timestamp); register-per-run records the
  profile in use.
- **I — Information disclosure.** The audit-log consumer reads RBAC / workload
  change events — configuration only, never Secret / ConfigMap / env / log
  payloads. A test asserts the event-path records carry no payload material.
- **D — Denial of service.** A high-churn cluster could flood; the subscribe path
  must rate-limit / coalesce events into the same hour-windowed idempotency key
  (reusing slice 487's `idem`), so a burst of edits to one binding collapses.
- **E — Elevation of privilege.** Adds at most a `watch` verb on the existing
  resources; never `secrets`, never a write verb, never a wildcard.

## Acceptance criteria

- [ ] **AC-1.** A new subscribe code path under `connectors/k8s/` that consumes
      the Kubernetes audit log (or a watch) and emits the existing two kinds.
- [ ] **AC-2.** `register` advertises `profiles_supported=[pull, subscribe]`; the
      platform-side wire stays push (invariant #3).
- [ ] **AC-3.** Event-driven records reuse the slice-487 evidence kinds + schemas
      (no new schema) and the slice-487 `idem` hour-window dedup.
- [ ] **AC-4.** The mechanism is documented honestly (audit-log / watch, with the
      managed-cluster fallback) — NOT "continuous monitoring."
- [ ] **AC-5.** Records push through the existing `IngestEvidence` API with a
      sha256 content-hash.
- [ ] **AC-6.** Tests: a faked audit-log / watch stream drives the subscribe path
      to a push round-trip; no-leak test; no-token-log test; burst-coalesce test.
- [ ] **AC-7.** README + decisions log + changelog updated.

## Constitutional invariants honored

- **#3 — Single canonical inbound API.** Push-only platform wire even on the
  subscribe profile.
- **Anti-pattern — no closed proprietary endpoint agent.** Read-only Kubernetes
  API / audit log.
- **Evidence integrity.** sha256 content-hash per record.
- **Anti-pattern: honest intervals.** The subscribe profile names its mechanism
  honestly, not "continuous monitoring."

## Dependencies

- **#487** (base Kubernetes connector) — the collector pattern, evidence kinds,
  `idem`, and ClusterRole base.

## Anti-criteria (P0 — block merge)

- Does NOT widen the platform-side wire — push only.
- Does NOT add new evidence kinds — reuses the slice-487 two.
- Does NOT add write verbs / `secrets` / wildcards to the ClusterRole.
- Does NOT label the subscribe profile "continuous monitoring."
