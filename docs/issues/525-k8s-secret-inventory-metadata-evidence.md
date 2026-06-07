# 525 — Kubernetes connector: Secret-inventory (metadata-only) evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #487 — base Kubernetes connector — merged first)

## Narrative

Slice 487 deliberately excluded all Secret access — the base connector's
ClusterRole does not grant `secrets`, and a test asserts no Secret value ever
enters a record. This slice adds **Secret-inventory evidence that remains
metadata-only**: per-Secret `type` (`Opaque` / `kubernetes.io/tls` /
`kubernetes.io/service-account-token` / ...), `namespace`, name, creation
timestamp, and key _names_ (NOT values) — read read-only via the Kubernetes API.
The auditable question is "how many TLS secrets / SA tokens exist, where, and how
old" (rotation + sprawl signals), never the secret contents.

This is the load-bearing scope call: the slice grants `get`/`list` on `secrets`
(which the base connector forbids), so it MUST collect **metadata only** and
explicitly drop the `.data` / `.stringData` fields at the client boundary. The
schema has no value field; a test asserts no Secret value ever materializes.

This is the slice-487 pattern otherwise verbatim: a new `internal/secretmeta`
collector + a new `k8s.secret_inventory.v1` evidence kind + schema with
`x-default-scf-anchors` (candidate: `CRY-01` / `IAC-22`), registered in
`DefaultSeed`, faked Kubernetes API surface in tests. No platform-side wire change
(invariant #3 — push only); `profiles_supported` stays `[pull]`; the interval
stays honestly named.

**Scope discipline.** Secret METADATA only — type, namespace, name, age, key
names. NEVER `.data` / `.stringData` values. This is why it is a separate slice:
it requires a deliberate, documented ClusterRole widening that the base connector
refused, and the metadata-only guard must be re-proved in code + test.

## Threat model

Inherits the slice-487 connector-family threat model, with Information Disclosure
elevated to PRIMARY because the ClusterRole now reaches `secrets`.

- **S — Spoofing.** Reuses the existing connector push credential; the cluster
  ServiceAccount gains a `secrets` get/list rule. The credential stays
  source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates it.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:k8s:secretmeta@<version>`) + hour-truncated `observed_at`.
- **I — Information disclosure (PRIMARY).** The connector CAN now read Secret
  objects but MUST drop `.data` / `.stringData` at the client boundary before any
  Go struct materializes them. The schema has no value field. A test asserts no
  Secret value (and no base64-decoded value) ever enters a record. This is the
  whole point of the slice.
- **D — Denial of service.** Bounded page reads + run timeout.
- **E — Elevation of privilege.** The ClusterRole gains exactly one rule:
  `"" (core): secrets` verbs `get,list` — documented loudly as the one grant the
  base connector intentionally withheld. No write verbs; no wildcard.

## Acceptance criteria

- [ ] **AC-1.** A new `internal/secretmeta` collector under `connectors/k8s/`
      following the slice-487 collector pattern.
- [ ] **AC-2.** It collects per-Secret type / namespace / name / age / key-NAMES
      via the read-only Kubernetes API.
- [ ] **AC-3.** A `k8s.secret_inventory.v1` evidence kind + schema with NO value
      field + `x-default-scf-anchors`, registered in `DefaultSeed`.
- [ ] **AC-4.** The documented ClusterRole gains the one `secrets` get/list rule;
      README + permissions subcommand updated; the least-privilege test is updated
      to permit `secrets` get/list ONLY for this connector mode (and still reject
      write verbs / wildcards).
- [ ] **AC-5.** A test asserts no `.data` / `.stringData` value (raw or base64)
      ever enters a record — the load-bearing metadata-only guard.
- [ ] **AC-6.** Records push through the existing `IngestEvidence` API with a
      sha256 content-hash; no platform-side wire change.
- [ ] **AC-7.** Tests: collect → push round-trip against a mocked API;
      no-token-log test.
- [ ] **AC-8.** README + decisions log + changelog updated.

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
- Does NOT collect Secret VALUES — metadata only (the load-bearing guard).
- Does NOT add write verbs / wildcards to the ClusterRole.
- Does NOT label the pull profile "continuous monitoring."
