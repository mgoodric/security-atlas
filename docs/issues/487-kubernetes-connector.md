# 487 — Kubernetes connector (RBAC + workload security config)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

The v1 connector roster ships the 7 MVP connectors (`aws`, `github`, `okta`,
`1password`, `osquery`, `jira`, `manual`; canvas §10.1, `connectors/`); the
planned layout (`CLAUDE.md`, "Planned repository layout") names `k8s` as a
first-tier connector alongside the clouds. For the platform's persona — a SaaS
startup security leader — **Kubernetes is near-universal**: the product itself
almost certainly runs on a cluster, and "prove cluster RBAC is least-privilege"

- "prove workloads run non-root with read-only filesystems and no privileged
  containers" are recurring SOC 2 CC6.x / ISO A.8 evidence demands the platform
  cannot serve today without manual upload.

The connector pattern is locked by slice 004 (AWS exemplar) and reused by slices
442 (GCP) / 443 (Slack) / 486 (Azure), and codified in the
`feedback_connector_patterns` memory: stable `actor_id` format, stable optional
fields, `observed_at` granularity, register-per-run, scope minimums,
vendor-native auth. This slice ships **one vertical Kubernetes connector**
following that template: collect **RBAC** evidence (ClusterRole / Role +
their bindings — who-can-do-what) + **workload security-context** evidence
(per-workload `runAsNonRoot`, `privileged`, `readOnlyRootFilesystem`,
`allowPrivilegeEscalation`, host-namespace flags) via the read-only Kubernetes
API (`kubectl auth`-style read verbs), register `profiles_supported` per run,
and `Push` each record to the platform's single inbound `IngestEvidence` API.

**Scope discipline.** **One connector, two evidence surfaces** (RBAC +
workload security-context), the minimum that demonstrates the Kubernetes
connector is a real first-class peer. It does **not** ship NetworkPolicy /
PodSecurityStandards-admission / Secret-inventory / image-provenance / audit-log
evidence (follow-ons), does **not** ship a watch/event-driven profile (pull-
profile only in v0 — name the interval honestly), and does **not** add any
platform-side wire change (the wire is always push — invariant #3). It is
**API-based, not an in-cluster proprietary agent** — consistent with the
"no closed proprietary collector agents on endpoints" anti-pattern.
**Follow-on slices:** NetworkPolicy coverage evidence; Pod-Security-Standards
admission-config evidence; Secret-inventory (metadata-only) evidence; watch-based
event-driven profile via the Kubernetes audit log.

## Threat model (STRIDE) — connector family (source-credential heavy)

A connector is a separate process holding **source-side credentials** (here, a
Kubernetes ServiceAccount kubeconfig / token with cluster-read access). The
dominant risks are credential handling (over-broad cluster verbs, kubeconfig
leakage), over-collection (Secret VALUES must never be read), and ensuring the
connector emits only push to the platform (no inbound surface widening).

**S — Spoofing.** The connector authenticates TO the platform via its push
credential (the existing connector auth — OAuth client_credentials per slice 191) and TO the cluster via a ServiceAccount token / kubeconfig. Risk: a stolen
push credential, or a kubeconfig with cluster-admin scope.
**Mitigation:** push reuses the existing connector credential boundary (no new
auth scheme); cluster auth uses a **read-only** ServiceAccount bound to a
purpose-built ClusterRole granting only `get`/`list` on the in-scope resource
kinds (RBAC objects + workloads) — explicitly NOT `get` on Secrets — documented
as the required minimum. The kubeconfig/token stays source-side; the platform
never sees it (invariant #3).

**T — Tampering.** Evidence records carry a sha256 content-hash; a tampered
record is detectable.
**Mitigation:** each pushed record is content-hashed (v1 evidence-integrity
primitive); the platform's ingest validates the hash. The connector does not
accept inbound data — it only reads the cluster + pushes.

**R — Repudiation.** Which connector run produced which evidence must be
traceable.
**Mitigation:** register-per-run records the connector identity + run; each
evidence record carries a stable `actor_id` (the k8s connector + cluster + run
context) and `observed_at` at a documented granularity (slice 004 pattern).

**I — Information disclosure (PRIMARY for Kubernetes).** A cluster holds Secrets
(credentials, tokens, TLS keys) and ConfigMaps that may embed sensitive values.
The connector must collect ONLY RBAC + security-context **configuration**, never
Secret values or pod environment payloads.
**Mitigation:** the connector's ClusterRole does NOT grant read on Secrets; it
collects RBAC rules/bindings and workload security-context flags only — NOT
Secret data, NOT ConfigMap values, NOT container env values, NOT logs. A test
asserts no Secret/ConfigMap value ever enters an evidence record. The credential
is never logged.

**D — Denial of service.** A large cluster (thousands of pods/roles across many
namespaces) could make a run unbounded.
**Mitigation:** the connector paginates API reads (`limit` + `continue`) with
bounded page sizes + a per-run cap; the pull profile runs on a named interval
(honest, not "continuous"); a run timeout caps a hung collection.

**E — Elevation of privilege.** Risk: the ServiceAccount is bound to
`cluster-admin` "to be safe," or the connector runs in-cluster with a privileged
pod security context.
**Mitigation:** the connector requires a least-privilege read-only ClusterRole
(`get`/`list` on the named kinds, no Secrets, no write verbs); the docs name the
exact ClusterRole rules and warn against `cluster-admin`. When run in-cluster the
README recommends a non-root, non-privileged pod spec. No platform-side privilege
beyond push (invariant #3).

## Acceptance criteria

**Connector — collection**

- [ ] **AC-1.** A `connectors/k8s/` connector lands following the slice-004 /
      442 template (register-per-run, stable `actor_id`, `observed_at`
      granularity, scope minimums).
- [ ] **AC-2.** It collects **RBAC** evidence (ClusterRole / Role + bindings)
      via the read-only Kubernetes API.
- [ ] **AC-3.** It collects **workload security-context** evidence
      (`runAsNonRoot`, `privileged`, `readOnlyRootFilesystem`,
      `allowPrivilegeEscalation`, host-namespace flags) per workload via the
      read-only Kubernetes API.
- [ ] **AC-4.** It authenticates via a least-privilege read-only ServiceAccount
      / kubeconfig (no Secret read, no write verbs), documented as the minimum,
      including the exact ClusterRole rule set.

**Connector — push**

- [ ] **AC-5.** Each collected record is pushed to the platform's single
      `IngestEvidence` (`Push`) API — no platform-side wire change (invariant #3).
- [ ] **AC-6.** Each record carries a sha256 content-hash + stable optional
      fields per the connector pattern.
- [ ] **AC-7.** The connector registers its `profiles_supported` (`pull` in v0)
      per run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-8.** The k8s-RBAC + workload-security-context evidence_kind schemas
      land in the schema-registry schemas tree with `x-default-scf-anchors` set
      (OQ #9).

**Tests**

- [ ] **AC-9.** Connector unit/integration tests cover the collect → push
      round-trip against a mocked Kubernetes API surface (the connector's own
      client is faked; the push receipt is asserted).
- [ ] **AC-10.** A test asserts the connector emits ONLY RBAC + security-context
      config (no Secret values / ConfigMap values / container env / logs).
- [ ] **AC-11.** A test asserts the connector never logs the kubeconfig / token.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** A connector README documents the minimal read-only ClusterRole
      (exact rules), the recommended non-privileged in-cluster pod spec, the pull
      interval, and the evidence kinds.
- [ ] **AC-13.** A decisions log
      (`docs/audit-log/487-kubernetes-connector-decisions.md`) records the
      evidence-kind shape + scope-minimum + stable-field JUDGMENT calls.
- [ ] **AC-14.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** First-class
  peer connector holding source-side credentials; push-only platform wire.
- **Licensing / anti-pattern — no closed proprietary endpoint agent.** The
  connector is OSS, in-tree, and uses the read-only Kubernetes API (not a
  proprietary in-node agent).
- **Evidence integrity.** sha256 content-hash per record (v1 primitive).
- **Anti-pattern: honest intervals.** The pull profile names its interval.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — Evidence SDK, connectors,
  `profiles_supported`, push wire.
- `CLAUDE.md` "Planned repository layout" — `connectors/k8s/` named.
- `Plans/EVIDENCE_SDK.md` — full SDK contract incl. push profile.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The push surface.
- **#004** (AWS connector exemplar) — `merged`. The connector pattern template.
- **#191** (SDK OAuth client_credentials migration) — `merged`. Connector push
  credential.

## Anti-criteria (P0 — block merge)

- **P0-487-1.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-487-2.** Does NOT require or document `cluster-admin` / write verbs —
  read-only least-privilege ClusterRole only (threat-model E).
- **P0-487-3.** Does NOT read Secret values / ConfigMap values / container env /
  logs — RBAC + security-context config only (threat-model I).
- **P0-487-4.** Does NOT log or transmit the kubeconfig / token into the platform.
- **P0-487-5.** Does NOT ship a closed/proprietary in-node agent — OSS,
  read-only Kubernetes API (anti-pattern: proprietary endpoint collectors).
- **P0-487-6.** Does NOT label the pull profile "continuous monitoring."
- **P0-487-7.** Does NOT implement NetworkPolicy / PSS-admission / Secret-
  inventory / audit-log evidence — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; no live cluster in CI) ·
`security-review` (source-credential + Secret over-collection risk) · `simplify`
· `changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** the connector is the slice-004 / 442 pattern
  verbatim — the work is Kubernetes-API-specific collection + the two
  evidence-kind schemas. Use `client-go` typed list calls; mirror the AWS / GCP
  connector structure.
- **The Secret-read exclusion is the load-bearing scope-minimum guard** — the
  ClusterRole must NOT grant `get`/`list` on `secrets`. Test it (AC-10).
- **JUDGMENT calls you own:** evidence-kind field shapes, `x-default-scf-anchors`
  per kind, and the scope minimum (exact ClusterRole rules). Record in the
  decisions log; the maintainer re-checks anchor accuracy (OQ #9 load-bearing).
- Reuse `feedback_connector_patterns`: stable actor_id, stable optional fields,
  observed_at granularity, register-per-run, scope minimums, vendor-native auth.
- Detection-tier: `none` unless a bug surfaces during the build.
