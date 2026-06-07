# 524 — Kubernetes connector: Pod-Security-Standards admission-config evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #487 — base Kubernetes connector — merged first)

## Narrative

Slice 487 shipped the base Kubernetes connector (RBAC + workload security-context).
That workload kind reports the _actual_ security context of running workloads;
this slice adds the _enforced_ side: **Pod-Security-Standards (PSS) admission
configuration** — which namespaces carry the `pod-security.kubernetes.io/enforce`,
`/audit`, `/warn` labels and at which level (`privileged` / `baseline` /
`restricted`), read read-only via the Kubernetes API (`namespaces` get/list — a
rule the base connector already holds). Auditors increasingly ask "is hardening
_enforced at admission_, not just configured per-workload"; the namespace PSS
labels are that proof.

This is the slice-487 pattern verbatim: a new `internal/pss` collector + a new
`k8s.pod_security_admission.v1` evidence kind + schema with
`x-default-scf-anchors` (candidate: `CFG-02` Secure Baseline Configurations),
registered in `DefaultSeed`, faked Kubernetes API surface in tests. No
platform-side wire change (invariant #3 — push only); `profiles_supported` stays
`[pull]`; the interval stays honestly named.

**Scope discipline.** Namespace PSS label configuration only. It does NOT read
the cluster's `AdmissionConfiguration` file (out of API reach), validating
webhooks, or third-party policy engines (OPA/Gatekeeper, Kyverno) — those are
follow-ons. No new ClusterRole rule is required (it reuses the base connector's
`namespaces` get/list grant).

## Threat model

Inherits the slice-487 connector-family threat model verbatim.

- **S — Spoofing.** Reuses the existing connector push credential; cluster auth
  uses the base connector's read-only ServiceAccount (no new grant). The
  credential stays source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates it.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:k8s:pss@<version>`) + hour-truncated `observed_at`.
- **I — Information disclosure.** Namespace labels are configuration metadata; no
  Secret / ConfigMap / env / log data is read. A test asserts no payload leak.
- **D — Denial of service.** Bounded page reads + run timeout.
- **E — Elevation of privilege.** No new verb / resource; reuses the existing
  `namespaces` get/list rule. No `secrets`, no write verbs.

## Acceptance criteria

- [ ] **AC-1.** A new `internal/pss` collector under `connectors/k8s/` following
      the slice-487 collector pattern (narrow `API` interface, faked in tests).
- [ ] **AC-2.** It collects per-namespace PSS enforce/audit/warn levels via the
      read-only Kubernetes API (reusing the existing `namespaces` grant).
- [ ] **AC-3.** A `k8s.pod_security_admission.v1` evidence kind + schema with
      `x-default-scf-anchors`, registered in `DefaultSeed`.
- [ ] **AC-4.** No new ClusterRole rule is required (documented as such; the
      least-privilege test still passes unchanged).
- [ ] **AC-5.** Records push through the existing `IngestEvidence` API with a
      sha256 content-hash; no platform-side wire change.
- [ ] **AC-6.** Tests: collect → push round-trip against a mocked API; no-leak
      test; no-token-log test.
- [ ] **AC-7.** README + decisions log + changelog updated.

## Constitutional invariants honored

- **#3 — Single canonical inbound API.** Push-only platform wire.
- **Anti-pattern — no closed proprietary endpoint agent.** Read-only Kubernetes
  API.
- **Evidence integrity.** sha256 content-hash per record.
- **Anti-pattern: honest intervals.** The pull profile names its interval.

## Dependencies

- **#487** (base Kubernetes connector) — the collector pattern + `namespaces`
  grant.

## Anti-criteria (P0 — block merge)

- Does NOT widen the platform-side wire — push only.
- Does NOT add write verbs / `secrets` / wildcards to the ClusterRole.
- Does NOT read Secret / ConfigMap / env / log values.
- Does NOT label the pull profile "continuous monitoring."
