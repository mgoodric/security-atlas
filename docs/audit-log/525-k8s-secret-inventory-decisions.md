# 525 — Kubernetes Secret-inventory (metadata-only) evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls: the
evidence-kind field shape (exactly which metadata fields are in — values are
categorically OUT), the SCF anchor choice, the load-bearing ClusterRole grant,
the honest pull-interval naming, and THE load-bearing call — the structural
metadata-only guard that makes it physically impossible for a Secret value to
reach a record. It does NOT block merge; the maintainer iterates post-deployment
from the "Revisit once in use" list.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no product-behavior bug surfaced during the
  slice — the build-time adjustments were the expected fifth-kind authoring
  fixes, caught at the unit/build tier as I threaded the new collector).
- **detection_tier_target:** none.

The build-time adjustments were the expected consequence of adding a fifth
evidence kind to the slice-487 connector, all authoring fixes rather than
product-behavior defects:

- the `cmd` seam harness gained a `secretMetaScan` override; the register
  integration test's `supported_kinds` count moved 4 → 5 and the round-trip test
  pushes a fifth record (secret-inventory) end-to-end;
- because the secret-inventory mode is OPT-IN (default off), the existing
  completing seam/integration tests did NOT need a `--skip-secret-inventory`
  flag — they simply leave `--collect-secret-inventory` false (the opposite of
  the slices 487/523/524 on-by-default pattern; see D5);
- `gofmt` re-aligned the seam-test `seamOverrides` struct + the `cmd_run.go`
  seam var block on first write (no semantic change).

## Decisions made

### D1 — A SEPARATE sibling kind `k8s.secret_inventory.v1` on the same binary

- **Options:** (a) a new sibling kind on the existing `atlas-k8s` binary;
  (b) fold secret facts into an existing kind (rejected — no existing kind is
  about secrets, and the over-collection guards of the other kinds explicitly ban
  secret material); (c) a brand-new connector binary (rejected — same source,
  same ServiceAccount, same read-only API).
- **Chosen:** (a). A new `connectors/k8s/internal/secretmeta` collector + a new
  `k8s.secret_inventory.v1` kind on the SAME `atlas-k8s` binary. This is the
  slice-487/523/524 pattern verbatim: `secretmeta.Collect` mirrors
  `pss.Assess`/`netpol.Assess`, `secretmeta.Client` mirrors `pss.Client`,
  `idem.SecretInventoryKey` mirrors `PSSAdmissionKey`, `buildSecretMetaRecord`
  mirrors `buildPSSRecord`. **Confidence: high.**

### D2 (load-bearing) — THE metadata-only guard: a Secret VALUE physically cannot enter a record

- **Decision:** the secret-inventory record carries ONLY metadata — namespace,
  name, type, age (days) + creation timestamp, and the NAMES of the keys under
  `.data`. There is NO field of any kind that can hold a value.
- **How it is enforced (structural, not procedural — slice 595/636 style):**
  1. **Type-level.** `secretmeta.RawSecret` and `secretmeta.Inventory` have no
     value field. `KeyNames []string` holds the `.data` MAP KEYS only.
  2. **Decode boundary.** The client decode target `apiSecret` models `.data` as
     `map[string]json.RawMessage` and `reduce()` reads ONLY the map KEYS
     (`for k := range s.Data`); the `RawMessage` values (the base64 blobs) are
     never decoded, never base64-decoded, never copied into a record-bound field,
     and are dropped when the decode target leaves scope. `.stringData` is NOT
     modeled at all, so Go's json decoder discards it.
  3. **Reflection guard (`TestStruct_MetadataOnly_NoValueBearingFields`).** Fails
     the build if any field name on either struct hints at a value / data /
     content / payload / secret / cert / key / token / base64 / decoded /
     plaintext surface (with `KeyNames` allow-listed — map KEYS, not values).
  4. **Fixture-with-real-data drop tests.** `TestReduce_DropsSecretValues` and
     `TestClient_NoSecretValueReachesRecord` feed a Secret with REAL `.data`
     (base64), `.stringData` (plaintext), and an annotation carrying a secret
     blob, then serialize the whole record and assert NONE of those markers —
     base64, the base64-DECODED form, or the plaintext — appears anywhere.
- **Rationale:** information disclosure is the PRIMARY threat for this slice
  because the ClusterRole now reaches `secrets`. A procedural "don't copy the
  value" is not enough; the type makes the leak unrepresentable and the guards
  fail the build if a future edit reopens the door. **Confidence: high.**

### D3 (load-bearing) — the ClusterRole gains EXACTLY one rule: core `secrets` get,list

- **Decision:** `k8sauth.SecretsRule()` returns exactly `apiGroups:[""],
resources:["secrets"], verbs:["get","list"]` — and nothing more.
  `k8sauth.SecretInventoryClusterRole()` = base `DocumentedClusterRole()` + that
  one rule. The base role is UNCHANGED and still excludes `secrets` (operators not
  running the inventory mode keep the narrower grant).
- **Enforcement:** the existing `TestDocumentedClusterRole_IsLeastPrivilege`
  still asserts the BASE role has no `secrets`. Two new tests
  (`TestSecretsRule_IsGetListOnSecretsOnly`,
  `TestSecretInventoryClusterRole_AddsExactlyTheSecretsRule`) pin that the
  inventory role adds EXACTLY the one secrets get/list rule, introduces no
  wildcard, grants no write verb, and still carries `secrets` in exactly ONE
  rule. The `permissions --secret-inventory` subcommand prints the role WITH the
  grant (loudly documented); the default `permissions` output still withholds it
  (`TestNewPermissionsCmd_RendersClusterRole` still asserts no `secrets`).
- **Rationale:** AC-4 / P0-525 — exactly the secrets get/list grant, documented
  loudly as the one access the base connector intentionally withheld; no write,
  no wildcard. **Confidence: high.**

### D4 — anchor choice: CRY-01 + CRY-09 (the spec's candidate IAC-22 is a dangling anchor)

- **Spec candidates:** `CRY-01` / `IAC-22`.
- **Verified against the bundled SCF catalog fixture
  (`migrations/fixtures/scf-sample.json`):** `CRY-01` ("Use of Cryptographic
  Controls") is present; `CRY-09` ("Cryptographic Key Management") is present;
  **`IAC-22` is ABSENT** (the IAC family in the fixture is 01/06/07/15/21). A
  dangling anchor fails the slice-068 drift guard (the registered kind's anchors
  must resolve against the seeded catalog).
- **Chosen:** `["CRY-01", "CRY-09"]`. CRY-01 covers the use of cryptographic
  controls (TLS / key material inventory); CRY-09 (Cryptographic Key Management)
  is the closest real anchor for the secret-material lifecycle signal this kind
  produces (rotation / sprawl of TLS secrets + SA tokens) — a stronger fit than
  any IAC anchor for secret-MATERIAL inventory, and it is present in the catalog.
  The `--secret-control` flag defaults to `scf:CRY-01`.
- **Rationale:** AC-3 requires real anchors that resolve; IAC-22 would dangle, so
  CRY-09 substitutes per the spec's "if either is absent, pick the closest real
  anchor and document the choice" instruction. **Confidence: high** that the
  anchors resolve; **medium** that CRY-09 is the single best lifecycle anchor —
  the maintainer may prefer a more specific key-rotation anchor once the SCF
  crosswalk for secret rotation is richer (Revisit).

### D5 — the mode is OPT-IN (default off), unlike the other four kinds

- **Decision:** secret-inventory is gated behind `--collect-secret-inventory`
  (default false), NOT a `--skip-*` flag. The other four kinds run by default and
  are individually skippable; this one is off by default and individually
  enabled.
- **Rationale:** the other four kinds need no grant beyond the base read-only
  ClusterRole, so on-by-default is safe. This kind needs the extra `secrets`
  grant the operator must consciously add; collecting it by default would either
  fail (no grant) or silently start reading secrets the moment someone widened
  the role for another reason. Opt-in makes "I am reading Secret objects" an
  explicit, auditable operator decision that lines up 1:1 with the explicit
  ClusterRole change. **Confidence: high.**

### D6 — field shape: type / namespace / name / age_days / created_at / key_count / key_names

- **In:** `namespace`, `secret_name`, `secret_type` (empty → `Opaque`, Kubernetes'
  default), `age_days` (whole days, clamped ≥0), `key_count`, and the optional
  `created_at` (RFC 3339) + `key_names` (sorted map keys).
- **Out (categorically):** any `.data` / `.stringData` value, raw or base64.
- **Rationale:** these answer "how many TLS secrets / SA tokens exist, where, how
  old, and which key shapes" — the rotation/sprawl audit question — with zero
  value exposure. `key_count` is always present (0 for an empty Secret) so the
  evaluator never has to distinguish "no keys" from "field omitted"; `created_at`
  and `key_names` are omitted when absent/empty to keep records lean.
  **Confidence: high.**

### D7 — honest pull interval, push-only wire (unchanged invariants)

- `profiles_supported` stays `[pull]`; the interval is named honestly
  (`24h (recommended; operator-scheduled — NOT continuous monitoring)`), reusing
  the existing `PullInterval`. No platform-side wire change — records push through
  the existing `IngestEvidence` (Push) API with the sha256 content-hash the SDK
  computes (invariant #3). No migration, no new tenant-scoped table, no RLS
  change. **Confidence: high.**

## Revisit once in use

- **CRY-09 vs a more specific rotation anchor (D4).** Once the SCF crosswalk for
  secret/key rotation is richer, the maintainer may retarget the second anchor or
  the `--secret-control` default to a more specific key-rotation control.
- **Age threshold semantics.** The connector emits `age_days` descriptively and
  does NOT verdict staleness. If operators want a per-tenant "stale secret"
  threshold, that is an evaluator-side policy (or a future descriptive `stale`
  hint), not a connector change.
- **Secret type taxonomy.** The connector records `type` verbatim (with empty →
  `Opaque`). If a downstream consumer wants normalized buckets (TLS / SA-token /
  dockercfg / opaque / other), that normalization belongs in the evaluator or a
  follow-on, not the raw inventory.
- **Per-Secret label/annotation metadata.** Out of scope by design (over-collection
  risk). If a future audit needs, e.g., a `managed-by` label, it is a deliberate
  additive field behind the same allow-list discipline — a slice, not a drift.

## Anti-criteria honored (P0)

- Does NOT widen the platform-side wire — push only (invariant #3).
- Does NOT collect Secret VALUES — metadata only (D2 structural guard).
- Does NOT add write verbs / wildcards to the ClusterRole — exactly `secrets`
  get,list (D3).
- Does NOT label the pull profile "continuous monitoring" (D7).
