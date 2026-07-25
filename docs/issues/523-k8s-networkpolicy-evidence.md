# 523 — Kubernetes connector: NetworkPolicy coverage evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #487 — base Kubernetes connector — merged first)

## Narrative

Slice 487 shipped the base Kubernetes connector with two evidence surfaces (RBAC
roles + bindings, and workload security-context), deliberately scoped to the
minimum that proves the connector is a first-class peer. This slice adds a third
surface: **NetworkPolicy coverage** — which namespaces have a default-deny
NetworkPolicy and which workloads are governed by an ingress/egress policy, read
read-only via the Kubernetes API (`networking.k8s.io/v1` networkpolicies,
get/list). The recurring SOC 2 CC6.6 / ISO A.8 evidence demand is "prove network
segmentation between workloads"; today the platform cannot serve it without manual
upload.

This is the slice-487 pattern verbatim: a new `internal/netpol` collector + a new
`k8s.networkpolicy_coverage.v1` evidence kind + its schema with
`x-default-scf-anchors` (candidate: `NET-04` Network Segmentation), registered in
`DefaultSeed`, faked Kubernetes API surface in tests. No platform-side wire change
(invariant #3 — push only); `profiles_supported` stays `[pull]`; the interval
stays honestly named.

**Scope discipline.** NetworkPolicy configuration only. It does NOT read CNI
plugin internals, Cilium/Calico CRDs (a follow-on), or Service/Ingress objects.
The connector's read-only ClusterRole gains exactly one rule:
`networking.k8s.io: networkpolicies` verbs `get,list` — never `secrets`, never a
write verb.

## Threat model

Inherits the slice-487 connector-family threat model verbatim: a separate process
holding source-side read-only ServiceAccount credentials, emitting push-only to
the platform.

- **S — Spoofing.** Reuses the existing connector push credential; cluster auth
  uses the same read-only ServiceAccount with one additional `get,list` rule. The
  credential stays source-side.
- **T — Tampering.** Each pushed record carries a sha256 content-hash; the ingest
  validates it.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:k8s:netpol@<version>`) + hour-truncated `observed_at`.
- **I — Information disclosure.** NetworkPolicy specs are configuration, not
  payloads; no Secret / ConfigMap / env data is read. A test asserts no payload
  material enters a record.
- **D — Denial of service.** Bounded page reads + run timeout cap a large cluster.
- **E — Elevation of privilege.** No new write verb; no `secrets`; the additional
  rule is `get,list` on `networkpolicies` only.

## Acceptance criteria

- [ ] **AC-1.** A new `internal/netpol` collector under `connectors/k8s/`,
      following the slice-487 collector pattern (narrow `API` interface, faked in
      tests; thin read-only HTTP client).
- [ ] **AC-2.** It collects NetworkPolicy coverage per namespace + the policies'
      pod selectors via the read-only Kubernetes API.
- [ ] **AC-3.** A `k8s.networkpolicy_coverage.v1` evidence kind + schema with
      `x-default-scf-anchors`, registered in `DefaultSeed`.
- [ ] **AC-4.** The connector's documented ClusterRole gains the one
      `networking.k8s.io: networkpolicies` get/list rule (README + permissions
      subcommand + least-privilege test updated).
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

- **#487** (base Kubernetes connector) — the collector pattern + ClusterRole base.

## Anti-criteria (P0 — block merge)

- Does NOT widen the platform-side wire — push only.
- Does NOT require write verbs / `secrets` / wildcards in the ClusterRole.
- Does NOT read Secret / ConfigMap / env / log values.
- Does NOT label the pull profile "continuous monitoring."
