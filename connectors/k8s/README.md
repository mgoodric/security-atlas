# Kubernetes connector

The Kubernetes connector (slice 487) brings cluster RBAC + workload hardening +
network segmentation + admission-time enforcement to the platform's evidence
pipeline. It follows the locked connector pattern verbatim: register-per-run, a
stable `actor_id`, an hour-truncated `observed_at`, scope minimums, and
vendor-native read-only auth. It emits seven evidence kinds:

| Kind                               | Profile          | Source                                                                                                                                                                       |
| ---------------------------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `k8s.rbac_binding.v1`              | pull + subscribe | Kubernetes API `rbac.authorization.k8s.io/v1` roles + bindings (get,list; **+ watch** on the subscribe profile — slice 526)                                                  |
| `k8s.workload_security_context.v1` | pull + subscribe | Kubernetes API `apps/v1` deployments / daemonsets / statefulsets (get,list; **+ watch** on the subscribe profile — slice 526)                                                |
| `k8s.networkpolicy_coverage.v1`    | pull             | Kubernetes API `networking.k8s.io/v1` networkpolicies + namespaces (get,list) — plus the installed CNI's policy CRDs when present (Cilium / Calico, get,list)                |
| `k8s.pod_security_admission.v1`    | pull             | Kubernetes API core `namespaces` (get,list) — `pod-security.kubernetes.io/*` labels                                                                                          |
| `k8s.admission_webhook.v1`         | pull             | Kubernetes API `admissionregistration.k8s.io/v1` validating + mutating webhook configurations (get,list) — **slice 652** — webhook CONFIG metadata, **never the caBundle**   |
| `k8s.admission_policy.v1`          | pull             | OPA/Gatekeeper `templates.gatekeeper.sh` + Kyverno `kyverno.io` policy CRDs (get,list, probe-detected) — **slice 652** — policy CONFIG metadata, **never the Rego/CEL body** |
| `k8s.secret_inventory.v1`          | pull             | Kubernetes API core `secrets` (get,list) — **OPT-IN, slice 525** — Secret METADATA only (type / namespace / name / age / key-NAMES), **never a value**                       |

The third kind (`k8s.networkpolicy_coverage.v1`, slice 523) reports, per
namespace, how many NetworkPolicies exist, a per-policy SPEC summary, and the
derived default-deny assessment — the recurring SOC 2 CC6.6 / ISO A.8 "prove
network segmentation between workloads" evidence demand.

**CNI-native policy CRDs (slice 622).** Many production clusters enforce
segmentation entirely through their CNI's own CRDs rather than (or in addition
to) upstream `networking.k8s.io` NetworkPolicy. When a Cilium CRD (`cilium.io`:
`CiliumNetworkPolicy` / `CiliumClusterwideNetworkPolicy`) or a Calico CRD
(`crd.projectcalico.org`: `NetworkPolicy` / `GlobalNetworkPolicy`) is **present**
in the cluster — detected by API discovery, never hard-failing when absent — the
collector reads those policy objects (`get,list`) and folds their coverage into
the same per-namespace default-deny assessment. Each policy summary carries a
`source` (`networking.k8s.io` / `cilium.io` / `crd.projectcalico.org`) and the
namespace carries the `sources` set, so the evaluator can reason about which
enforcement plane covers a namespace. A cluster-wide CNI policy
(`CiliumClusterwideNetworkPolicy` / Calico `GlobalNetworkPolicy`) with an
all-endpoints zero-rule shape folds default-deny into every namespace. Without
this, a Cilium/Calico-only cluster reads as fully unprotected — a false-FAIL.
Same over-collection guard as upstream: **CRD SPEC metadata only**, never the
peer / selector / CIDR / port contents of a rule. The fields are added
additively to the `1.0.0` schema (no version bump); records predating CNI
support stay valid and read as upstream-only.

The fourth kind (`k8s.pod_security_admission.v1`, slice 524) reports, per
namespace, the Pod-Security-Standards admission configuration: which namespaces
carry the `pod-security.kubernetes.io/enforce`, `/audit`, and `/warn` labels and
at which level (`privileged` / `baseline` / `restricted`), plus the optional
pinned version per mode. This is the **enforced** side of workload hardening
(does admission block under-hardened pods), complementing the **actual**-posture
`k8s.workload_security_context.v1` kind — the recurring "is hardening enforced at
admission, not just configured per-workload" auditor ask. It reuses the existing
`namespaces` get/list grant (**no new ClusterRole rule**).

The fifth and sixth kinds (`k8s.admission_webhook.v1` + `k8s.admission_policy.v1`,
slice 652) are the **"is hardening enforced beyond namespace PSS labels?"**
surfaces an auditor asks about once PSS labels are covered. Unlike slice 524
(which reused the held `namespaces` grant), this slice **deliberately adds new
read-only ClusterRole rules** — the **flagged scope expansion** this slice owns:

- **Admission webhooks** (`k8s.admission_webhook.v1`): the new
  `admissionregistration.k8s.io` `validatingwebhookconfigurations` +
  `mutatingwebhookconfigurations` `get,list` rule. Per webhook entry it records
  the CONFIG metadata — which resource TYPES + operation VERBS it intercepts, its
  `failurePolicy` (Fail / Ignore) and a derived `fail_closed` flag, whether it
  scopes by a namespace/object selector (the **presence**, never the match
  expressions), the declared `sideEffects`, and the dispatch service ref. It
  **never** reads the webhook's `.clientConfig.caBundle` / TLS client key or any
  intercepted object.
- **Third-party policy engines** (`k8s.admission_policy.v1`): OPA/Gatekeeper
  (`templates.gatekeeper.sh` `constrainttemplates`) and Kyverno (`kyverno.io`
  `clusterpolicies` + `policies`), each detected by an **API-discovery probe** so
  an **absent engine is never an error** (the slice-622 pattern). Per policy it
  records name, scope (cluster / namespaced), kind, and enforcement action
  (`enforce` / `audit` / `dryrun` / ...) + a derived `enforcing` flag. It
  **never** reads the policy's Rego/CEL decision-logic body. For Gatekeeper, v0
  records the ConstraintTemplate catalog (which policies are DEFINED); the
  per-constraint enforcement action is out of v0 because reading it would require
  a wildcard grant over the dynamic per-template constraint CRDs — deliberately
  avoided to keep every rule wildcard-free.

Both kinds are descriptive (`INCONCLUSIVE`); the platform evaluator owns the
fail-open / coverage policy call. Print the role that includes these rules with
`atlas-k8s permissions --admission`.

The seventh kind (`k8s.secret_inventory.v1`, slice 525) is **opt-in** and is the
**one deliberate exception** to the rule below: it requires adding **exactly one**
ClusterRole rule — core `secrets` `get`/`list`, the one grant the base connector
intentionally withholds. Even with that grant, the connector collects Secret
**METADATA ONLY** — the Secret `type` (`Opaque` / `kubernetes.io/tls` /
`kubernetes.io/service-account-token` / ...), namespace, name, creation timestamp
plus derived age in days, and the **NAMES** of the keys under `.data` (the map
keys, e.g. `tls.crt` / `tls.key` / `token`). It **never** reads, base64-decodes,
or records a Secret **value** (`.data` / `.stringData`). The auditable question is
"how many TLS secrets / SA tokens exist, where, and how old" (rotation + sprawl
signals), never the contents. See
[the secret-inventory section](#secret-inventory-opt-in-the-one-secrets-grant)
below. Enable it with `run --collect-secret-inventory`; it is **off by default**.

The connector is **API-based**, not an in-node agent — consistent with the
"no closed proprietary collector agents on endpoints" anti-pattern. It reads the
read-only Kubernetes API the same way `kubectl get` does.

By default the connector reads **RBAC + security-context + NetworkPolicy +
Pod-Security-Standards configuration only**. It never reads ConfigMap values,
container env, pod logs, nor the peer/CIDR/port contents inside a NetworkPolicy
rule block — and the **base** ClusterRole does not even grant access to
`secrets`. The **only** way the connector reaches Secret objects is the opt-in
`k8s.secret_inventory.v1` mode (slice 525), and even then it reads Secret
**metadata only** — never a value. The cluster credential stays source-side and
never enters an evidence record or a platform push (canvas invariant #3).

## Profiles — honest, not "continuous monitoring"

The connector registers `profiles_supported=[pull, subscribe]`. **Both** describe
how the connector retrieves data **from the cluster**; the platform-side wire is
always **push** (canvas invariant #3) regardless of profile.

### pull (the `run` subcommand)

Each invocation is one bounded read-and-push pass. It is **operator-scheduled**
(cron / scheduler / a CronJob in-cluster) — the recommended cadence is **every
24h**. This is deliberately **not** "continuous monitoring": the interval is
named honestly. The pull profile is the **reconciliation backstop** and the only
profile that resolves the full binding + role-rule + every-kind picture.

### subscribe (the `subscribe` subcommand) — event-driven via the Kubernetes watch (slice 526)

The subscribe profile consumes a long-lived **Kubernetes `watch`** against the
same read-only surfaces the pull profile reads (`rolebindings` for RBAC,
`deployments` for workloads), and pushes the **same two evidence kinds**
(`k8s.rbac_binding.v1`, `k8s.workload_security_context.v1`) — **no new kind** — as
RBAC / workload changes happen, instead of waiting for the next pull pass. This is
**event-driven via the Kubernetes watch API**, **not** "continuous monitoring":
the mechanism is named honestly.

**Watch lifecycle (the reflector pattern).** A Kubernetes watch is a long-lived
stream that ends (the server closes it periodically, or errors). The connector:

1. **LIST**s the resource to obtain the starting `resourceVersion` (RV);
2. **WATCH**es from that RV with `allowWatchBookmarks=true` so the server
   periodically sends a Bookmark event that advances the resume point cheaply
   (no re-LIST per reconnect);
3. on stream close / transient error, **re-watches** from the last-seen RV;
4. on a **410 Gone** (`resourceVersion too old`), **re-LISTs** for a fresh RV and
   resumes the watch from it.

The loop is bound by the process context — `SIGINT`/`SIGTERM` shuts it down
gracefully.

**Dedup + DoS coalescing.** Every event-built record carries the **same
slice-487 hour-window idempotency key** the pull path uses. So a watch-emitted
record and a pull-emitted record for the **same resource in the same hour**
collapse to one ledger row — and a **burst** of edits to one binding within the
hour collapses too (threat-model D). Run **both** profiles: subscribe for
freshness, pull for reconciliation; the hour-window key makes the overlap free.

**Audit-log alternative.** A Kubernetes audit-log consumer is a documented
**future / fallback** option (it sees the actor of each change directly), but it
requires control-plane audit-policy access that managed clusters frequently do
not expose. The `watch` against the read-only API is portable to every cluster,
so it is what ships. (See `docs/audit-log/526-k8s-watch-decisions.md` D1.)

```sh
# Run the event-driven profile (alongside a scheduled `run` pull).
atlas-k8s subscribe --cluster prod-eks --environment prod \
  --endpoint $SECURITY_ATLAS_ENDPOINT --token $SECURITY_ATLAS_TOKEN
```

The subscribe profile needs the base ClusterRole with the **`watch`** verb added
(alongside `get,list`) on **exactly** the rbac + apps surfaces — no new resource,
never `secrets`, never a write verb, never a wildcard. Print it with
`atlas-k8s permissions --subscribe`.

## Auth — least-privilege read-only ClusterRole

The connector authenticates to the cluster API server with a read-only
**ServiceAccount** token: either an out-of-cluster kubeconfig token
(`kubeconfig-token` mode) or the projected in-cluster ServiceAccount token
(`in-cluster` mode). The platform-side push reuses the existing connector
credential boundary (OAuth client_credentials, slice 191) — no new auth scheme.

Create a dedicated read-only ServiceAccount and bind it to **exactly** the
ClusterRole below. Every call the connector makes is a read (`get`/`list`).

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: security-atlas-readonly
rules:
  # On the event-driven (subscribe) profile — slice 526 — add "watch" to the
  # verbs on EXACTLY these two rules (rbac + apps): ["get", "list", "watch"]. No
  # new resource; never secrets/write/wildcard. `permissions --subscribe` prints
  # this variant. The pull-only profile keeps the get,list grant below.
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "clusterroles", "rolebindings", "clusterrolebindings"]
    verbs: ["get", "list"] # subscribe profile: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["deployments", "daemonsets", "statefulsets"]
    verbs: ["get", "list"] # subscribe profile: ["get", "list", "watch"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["networkpolicies"]
    verbs: ["get", "list"]
  # CNI-native policy CRDs (slice 622) — optional. Include only the apiGroup(s)
  # for the CNI installed in your cluster; the connector detects CRD presence and
  # skips an absent CNI without error. get,list only — never write/secrets/wildcard.
  - apiGroups: ["cilium.io"]
    resources: ["ciliumnetworkpolicies", "ciliumclusterwidenetworkpolicies"]
    verbs: ["get", "list"]
  - apiGroups: ["crd.projectcalico.org"]
    resources: ["networkpolicies", "globalnetworkpolicies"]
    verbs: ["get", "list"]
  # Admission webhooks (slice 652) — the deliberate, flagged scope expansion this
  # slice owns. CONFIG metadata only; never the webhook caBundle / TLS key.
  - apiGroups: ["admissionregistration.k8s.io"]
    resources:
      ["validatingwebhookconfigurations", "mutatingwebhookconfigurations"]
    verbs: ["get", "list"]
  # Third-party policy engines (slice 652) — OPTIONAL. Include only the apiGroup(s)
  # for the engine installed in your cluster; the connector probes CRD presence and
  # skips an absent engine without error. get,list only — never write/secrets/wildcard.
  # CONFIG metadata only; never the policy Rego/CEL decision-logic body.
  - apiGroups: ["templates.gatekeeper.sh"]
    resources: ["constrainttemplates"]
    verbs: ["get", "list"]
  - apiGroups: ["kyverno.io"]
    resources: ["clusterpolicies", "policies"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: security-atlas-readonly
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: security-atlas-readonly
subjects:
  - kind: ServiceAccount
    name: security-atlas
    namespace: security-atlas
```

Run `atlas-k8s permissions` to print this rule set.

**Banned grants.** Do **not** bind the ServiceAccount to `cluster-admin` or any
role with write verbs (`create`/`update`/`patch`/`delete`/`deletecollection`) or
wildcards (`*`). The connector has no write code path; the only operations it
issues are reads. The Pod-Security-Standards admission kind needs **no new
rule** — PSS configuration lives in `pod-security.kubernetes.io/*` labels on the
Namespace object, read via the existing `namespaces` get/list grant. The
`atlas-k8s permissions` output and `internal/k8sauth.DocumentedClusterRole()` are
the single source of truth — a unit test fails the build if anyone widens the
**base** rule set into a write verb, a wildcard, or `secrets`.

The one exception is the opt-in Secret-inventory mode below, which adds **exactly
one** `secrets` `get`/`list` rule — and nothing more (a unit test pins that the
secret-inventory ClusterRole adds that single rule and still rejects write verbs
and wildcards).

### Secret-inventory (opt-in): the one `secrets` grant

The `k8s.secret_inventory.v1` kind (slice 525) is **off by default** and is the
single grant the base connector intentionally withholds. Enabling it does two
things, both deliberate and loud:

1. **It adds exactly one ClusterRole rule** — core `secrets` `get`/`list`. No
   write verb, no wildcard, no other resource. Append this rule to the
   `security-atlas-readonly` ClusterRole only if you want the inventory:

   ```yaml
   # OPT-IN (slice 525): the ONE secrets grant the base connector withholds.
   # Secret METADATA only — type/namespace/name/age/key-NAMES, NEVER a value.
   - apiGroups: [""]
     resources: ["secrets"]
     verbs: ["get", "list"]
   ```

   Print the full role including this rule with
   `atlas-k8s permissions --secret-inventory`.

2. **It collects Secret METADATA only — never a value.** The collector reads the
   Secret `type`, namespace, name, creation timestamp + age, and the **names** of
   the keys under `.data` (the map keys, e.g. `tls.crt` / `tls.key` / `token`).
   It **never** reads, base64-decodes, or records the value behind any key, and
   it never touches `.stringData`. This is enforced **structurally**, not by
   discipline: the record struct has no field that can hold a value, a reflection
   guard fails the build if a value-bearing field is ever added, and a test feeds
   a fixture Secret with real `.data` (base64) + `.stringData` and asserts only
   `type` / `namespace` / `name` / `age` / `key-names` survive — no value, raw
   or base64-decoded, ever enters a record.

Run it with:

```bash
atlas-k8s run --cluster prod-us-east --environment prod \
  --collect-secret-inventory
```

The emitted record is **descriptive** (`INCONCLUSIVE`) — it is an inventory
signal (rotation / sprawl: how many TLS secrets / SA tokens exist, where, how
old), not a pass/fail verdict; the platform evaluator owns any policy call.

### Recommended in-cluster pod security context

When run in-cluster (e.g. as a CronJob), the connector pod itself should run
non-privileged — leading by example for the very posture it measures:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  seccompProfile:
    type: RuntimeDefault
containers:
  - name: atlas-k8s
    securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      privileged: false
      capabilities:
        drop: ["ALL"]
```

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-k8s register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Read RBAC + workload security contexts + NetworkPolicy coverage + PSS
# admission config, push records.
# The cluster token is read from the environment (never the CLI, so it stays
# out of shell history):
export KUBERNETES_API_SERVER=https://kube-api.example.com:6443
export KUBECONFIG_TOKEN=<read-only-serviceaccount-token>

atlas-k8s run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --cluster prod-us-east \
  --environment prod

# In-cluster (reads the projected ServiceAccount token automatically):
atlas-k8s run --cluster prod-us-east --environment prod --auth-mode in-cluster

# Print the least-privilege ClusterRole.
atlas-k8s permissions
```

| Flag                              | Subcommand  | Required | Default                       | Notes                                                                                                         |
| --------------------------------- | ----------- | -------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `--endpoint`                      | both        | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                                                                                        |
| `--token`                         | both        | yes      | env `SECURITY_ATLAS_TOKEN`    | security-atlas bearer token                                                                                   |
| `--insecure`                      | both        | no       | `false`                       | disables TLS; loopback endpoints only                                                                         |
| `--cluster`                       | `run`       | yes      | —                             | cluster identifier; scopes every record                                                                       |
| `--environment`                   | `run`       | yes      | —                             | environment scope tag; records are never emitted un-scoped                                                    |
| `--api-server`                    | `run`       | no\*     | env `KUBERNETES_API_SERVER`   | API server URL (\*required, via flag or env)                                                                  |
| `--auth-mode`                     | `run`       | no       | `kubeconfig-token`            | `kubeconfig-token` or `in-cluster`                                                                            |
| `--rbac-control`                  | `run`       | no       | `scf:IAC-21`                  | control id attached to RBAC records                                                                           |
| `--workload-control`              | `run`       | no       | `scf:CFG-02`                  | control id attached to workload records                                                                       |
| `--netpol-control`                | `run`       | no       | `scf:NET-04`                  | control id attached to NetworkPolicy coverage records                                                         |
| `--pss-control`                   | `run`       | no       | `scf:CFG-02`                  | control id attached to PSS admission records                                                                  |
| `--secret-control`                | `run`       | no       | `scf:CRY-01`                  | control id attached to Secret-inventory records                                                               |
| `--skip-rbac`                     | `run`       | no       | `false`                       | skip the `k8s.rbac_binding.v1` pull                                                                           |
| `--skip-workload`                 | `run`       | no       | `false`                       | skip the `k8s.workload_security_context.v1` pull                                                              |
| `--skip-netpol`                   | `run`       | no       | `false`                       | skip the `k8s.networkpolicy_coverage.v1` pull                                                                 |
| `--skip-pss`                      | `run`       | no       | `false`                       | skip the `k8s.pod_security_admission.v1` pull                                                                 |
| `--collect-secret-inventory`      | `run`       | no       | `false`                       | **opt-in** `k8s.secret_inventory.v1` (Secret metadata only; needs the extra `secrets` get/list grant)         |
| `--cluster` / `--environment`     | `subscribe` | yes      | —                             | same scoping as `run`; required on the event-driven profile                                                   |
| `--skip-rbac` / `--skip-workload` | `subscribe` | no       | `false`                       | do not watch that surface (at least one must remain enabled)                                                  |
| `--coalesce-cap`                  | `subscribe` | no       | `100000`                      | in-process per-hour idempotency-key set cap (DoS bound; the platform hour-window key is the durable collapse) |

The cluster token is **only** read from `KUBECONFIG_TOKEN` (or the projected
in-cluster mount) — never a CLI flag — so it never lands in shell history. It is
never logged and never enters an evidence record (the resolved credential redacts
its token on every format path).

`register` announces `name=k8s-connector`,
`supported_kinds=[k8s.rbac_binding.v1, k8s.workload_security_context.v1,
k8s.networkpolicy_coverage.v1, k8s.pod_security_admission.v1,
k8s.secret_inventory.v1]`, and
`profiles_supported=[pull, subscribe]` to
`ConnectorRegistryService.Register`. Both `profiles_supported` values describe how
the connector retrieves data **from the cluster** — `pull` is a scheduled
read-and-push pass; `subscribe` is event-driven via the Kubernetes watch API. The
platform-side wire is always push (invariant #3) regardless of profile.

## Scope minimums

Every emitted record sets the minimum scope dimensions the connector-pattern
convention requires:

| Scope key     | Value                    | Source                                                                                                |
| ------------- | ------------------------ | ----------------------------------------------------------------------------------------------------- |
| `cluster`     | the `--cluster` flag     | the `--cluster` flag                                                                                  |
| `environment` | the `--environment` flag | the `--environment` flag                                                                              |
| `namespace`   | the assessed namespace   | `k8s.networkpolicy_coverage.v1` + `k8s.pod_security_admission.v1` + `k8s.secret_inventory.v1` records |

`run` fails loudly when `--cluster` or `--environment` is unset rather than
pushing an un-scoped record. NetworkPolicy coverage, PSS admission, **and**
Secret-inventory records add a `namespace` scope dimension (one record per
namespace, or per Secret for the inventory) so a FrameworkScope predicate can cut
on namespace.

`source_attribution.actor_id` follows the cross-connector convention
`connector:<vendor>:<service>@<version>` — `connector:k8s:rbac@<version>` for RBAC
records, `connector:k8s:workload@<version>` for workload records,
`connector:k8s:netpol@<version>` for NetworkPolicy coverage records,
`connector:k8s:pss@<version>` for PSS admission records, and
`connector:k8s:secretmeta@<version>` for Secret-inventory records, where
`<version>` is the build's module version (or `dev` under `go run`).

## Idempotency

Each record's `idempotency_key` is
`sha256("<kind>|<identity>|<hour_truncated_observed_at>")` (see `internal/idem`).
RBAC identity = `scope/namespace/name`; workload identity = `kind/namespace/name`;
NetworkPolicy coverage identity = `namespace` (one coverage record per namespace);
PSS admission identity = `namespace` (one PSS record per namespace — a distinct
key from the netpol key thanks to the `k8s.pod_security_admission` kind prefix);
Secret-inventory identity = `namespace/name` (one record per Secret — a distinct
key thanks to the `k8s.secret_inventory` kind prefix).
`observed_at` is truncated to the UTC hour, so two runs within the same hour for
the same binding / workload / namespace / secret collapse to one ledger row; a
run that crosses an hour boundary writes a fresh record.

## Result semantics

- **`k8s.rbac_binding.v1` → `INCONCLUSIVE` (descriptive).** The connector does
  not decide pass/fail for a binding — the platform evaluator interprets which
  binding pattern passes/fails per (control, scope). The connector-side
  `grants_wildcard` flag is a heuristic hint (a rule grants a `*` verb / resource
  / apiGroup), not a verdict.
- **`k8s.workload_security_context.v1` → `PASS` / `FAIL` / `INCONCLUSIVE`.** The
  connector verdicts the deterministic hardening posture: `PASS` only when the
  workload runs non-root **and** is non-privileged **and** has a read-only root
  filesystem **and** forbids privilege escalation **and** uses no host namespace
  (`hostNetwork` / `hostPID` / `hostIPC`); `FAIL` when any of those is off;
  `INCONCLUSIVE` when a per-workload read errored. Container-level settings
  override pod-level; `allowPrivilegeEscalation` defaults to permissive when
  unset, matching Kubernetes admission semantics.
- **`k8s.networkpolicy_coverage.v1` → `PASS` / `FAIL` / `INCONCLUSIVE`.** The
  connector verdicts the per-namespace segmentation posture: a namespace is
  **default-deny** in a direction when it has at least one policy that selects
  every pod (empty `podSelector`) **and** governs that direction with **zero**
  allow rules — the Kubernetes canonical default-deny shape. `PASS` when
  default-deny holds for at least one direction; `FAIL` when the namespace has no
  default-deny (unprotected, or only per-pod allow rules); `INCONCLUSIVE` when a
  per-namespace read errored. The platform evaluator owns the final
  (control, namespace) call (e.g. whether default-deny ingress alone suffices).
- **`k8s.pod_security_admission.v1` → `PASS` / `FAIL`.** The connector verdicts
  the per-namespace admission posture from the `enforce` label: `PASS` when the
  namespace **enforces** a hardened level (`baseline` or `restricted`); `FAIL`
  when no `enforce` label is set (unenforced — recorded honestly), or when
  `enforce` is only `privileged`. `audit` / `warn` modes are reported in the
  record but do not drive the verdict (they observe / warn, they do not block
  admission). The platform evaluator owns the final (control, namespace) call.
- **`k8s.admission_webhook.v1` → `INCONCLUSIVE` (descriptive).** The connector
  emits the webhook's wiring (intercepted resources/operations, `fail_closed`,
  selector scope, dispatch ref), not a verdict — the platform evaluator owns any
  fail-open / coverage policy call per (control, scope).
- **`k8s.admission_policy.v1` → `INCONCLUSIVE` (descriptive).** The connector
  emits the policy's config (engine / name / scope / kind / enforcement action +
  derived `enforcing`), not a verdict — the platform evaluator owns the call.
- **`k8s.secret_inventory.v1` → `INCONCLUSIVE` (descriptive).** The connector
  emits a Secret-metadata inventory, not a verdict: it does not decide whether a
  Secret is too old or whether a key set is wrong — the platform evaluator owns
  any rotation / sprawl policy call per (control, scope). The signal is the
  metadata itself (type / age / key-names per Secret).

## What the connector never collects (the load-bearing guard)

The connector collects **RBAC rules/bindings + workload security-context flags +
NetworkPolicy SPEC metadata + namespace PSS labels + (opt-in) Secret metadata
only**. It never reads, materializes, or emits:

- **Secret values.** By default the base ClusterRole does not grant `secrets` at
  all. The opt-in `k8s.secret_inventory.v1` mode (slice 525) adds `secrets`
  get/list and reads Secret **metadata only** — type / namespace / name / age /
  the **names** of the keys under `.data` — and **never** a value (`.data` /
  `.stringData`, raw or base64-decoded). The record struct physically cannot hold
  a value; a reflection guard fails the build if a value-bearing field is added,
  and a fixture-with-real-`.data` test proves no value (raw or decoded) reaches a
  record.
- ConfigMap values
- container `env` / `envFrom` payloads (the workload client decodes only the
  security-context + host-namespace fields; env / volumes are discarded by the
  JSON decoder)
- pod logs, exec, or any pod contents
- the peer / CIDR / namespaceSelector / port contents inside a NetworkPolicy
  ingress / egress rule block (the netpol client **counts** rule blocks but
  decodes them as opaque `json.RawMessage`, so the peers inside are never
  materialized into Go memory) — and the `RawPolicy` / `Coverage` structs have
  **no field** that could carry a peer or selector value. The same guard applies
  verbatim to the CNI-native policy CRDs (slice 622): the Cilium
  `endpointSelector` and Calico `selector` are read **only** for their
  all-vs-narrow disposition, the `ingress`/`egress` rule arrays are counted as
  opaque `json.RawMessage`, and the per-policy `source` carried into a record is
  an API-group string (`cilium.io` / `crd.projectcalico.org`), never workload
  data
- **Admission-webhook caBundle / TLS key (slice 652).** The
  `k8s.admission_webhook.v1` collector reads webhook **CONFIG metadata only** —
  it does **not** model `.clientConfig.caBundle`, the dispatch `url`, or any TLS
  field, so the JSON decoder discards them; the `RawWebhook` / `Webhook` structs
  have **no field** that could carry a caBundle / TLS key or an intercepted
  object (a reflection guard fails the build if one is added, and a
  fixture-with-real-caBundle drop test proves none reaches a record).
- **Policy-engine Rego/CEL decision-logic body (slice 652).** The
  `k8s.admission_policy.v1` collector reads policy **CONFIG metadata only** — it
  does **not** model the Gatekeeper ConstraintTemplate `targets[]` (Rego) or the
  Kyverno `spec.rules[]` (CEL/JMESPath), so the decoder discards them; the
  `RawPolicy` / `Policy` structs have **no field** that could carry a rule body
  (the same reflection guard + a fixture-with-real-Rego/CEL drop test enforce it).
- actual network traffic / flow logs (this is configuration, not telemetry)
- any namespace label or annotation other than the `pod-security.kubernetes.io/*`
  labels (the PSS client reads **only** those six label keys off `metadata.labels`
  and never decodes `metadata.annotations` at all; every other namespace label,
  the `spec`, and the `status` are discarded by the JSON decoder) — and the
  `RawNamespace` / `Admission` structs have **no field** that could carry a pod
  spec, secret, or arbitrary namespace metadata (a reflection guard fails the
  build if such a field is added)

Tests assert that no Secret / ConfigMap / env material — no NetworkPolicy peer /
selector / port value — and no non-PSS namespace label / annotation ever enters
an evidence record, and that the cluster token never appears in any formatted
credential.

## Not in v0 (follow-ons)

The connector ships seven evidence surfaces (RBAC, workload security context,
NetworkPolicy coverage, Pod-Security-Standards admission config, admission-webhook
config, third-party policy-engine config, and the opt-in Secret-inventory
metadata). It does **not** ship:

- the cluster's `AdmissionConfiguration` file (out of API reach — it lives on the
  control-plane host filesystem, not the Kubernetes API; likely never in scope for
  an API-only connector)
- the **per-constraint** Gatekeeper enforcement action — v0 records the
  ConstraintTemplate catalog (which policies are DEFINED); reading each
  constraint's `enforcementAction` would require a wildcard grant over the dynamic
  per-template constraint CRDs, deliberately avoided to keep every rule
  wildcard-free (a follow-on can revisit if a wildcard-free discovery path is
  acceptable)
- image-provenance / audit-log evidence
- Service / Ingress object coverage
- a watch-based event-driven profile via the Kubernetes audit log

These are filed as follow-on slices (see `docs/issues/487-kubernetes-connector.md`,
`docs/issues/523-k8s-networkpolicy-evidence.md`,
`docs/issues/524-k8s-pod-security-standards-admission-evidence.md`, and the
spillover bands 621–624 + 652–659).

## Tests

```sh
go test ./connectors/k8s/...
```

Unit tests fake the Kubernetes API surfaces (no live cluster, no real
credentials) and pin the RBAC normalization + wildcard heuristic, the workload
hardening verdict matrix, the security-context aggregation across containers, the
NetworkPolicy default-deny assessment matrix, the credential redaction, the
read-only ClusterRole contract, the PSS enforce/audit/warn verdict matrix, and
the `idem` hour-window behavior. The netpol client test serves a NetworkPolicy
whose rule block embeds a podSelector label, an ingress peer namespaceSelector
label, a CIDR, and a port, and asserts none of that peer / selector payload
escapes into the reduced record (the over-collection guard). The PSS client test
serves a namespace carrying unrelated labels (a team label, a secret-looking
label value) **and** annotations (a kubectl last-applied blob with embedded
secret material, an owner-email) alongside the `pod-security.kubernetes.io/*`
labels, and asserts only the PSS labels reach a record (the label-filter guard);
a reflection guard pins that the PSS structs carry only namespace name + the
three modes / levels / versions. The Secret-inventory client test serves Secrets
with **real** `.data` (base64) + `.stringData` (plaintext) + an annotation
carrying a secret blob, and asserts that **no value — base64, decoded, or
plaintext — ever reaches a record** (the load-bearing metadata-only guard); a
reflection guard pins that the Secret-inventory structs carry only metadata +
key-names and have no value-bearing field, and a no-token-log test proves the
bearer token never enters a record. The integration test (in-package, bufconn
platform — no Postgres) exercises the full collect → build → SDK `Push` →
push-receipt round-trip for all five kinds and asserts two same-hour pushes
collapse to one `record_id`, that emitted payloads carry config / authz / Secret
metadata only (no Secret value, no ConfigMap / env values, no NetworkPolicy peer
/ selector value, no non-PSS namespace label / annotation), and that the
credential never surfaces in a formatted log.
