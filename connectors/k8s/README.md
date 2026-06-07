# Kubernetes connector

The Kubernetes connector (slice 487) brings cluster RBAC + workload hardening to
the platform's evidence pipeline. It follows the locked connector pattern
verbatim: register-per-run, a stable `actor_id`, an hour-truncated `observed_at`,
scope minimums, and vendor-native read-only auth. It emits two evidence kinds:

| Kind                               | Profile | Source                                                                      |
| ---------------------------------- | ------- | --------------------------------------------------------------------------- |
| `k8s.rbac_binding.v1`              | pull    | Kubernetes API `rbac.authorization.k8s.io/v1` roles + bindings (get,list)   |
| `k8s.workload_security_context.v1` | pull    | Kubernetes API `apps/v1` deployments / daemonsets / statefulsets (get,list) |

The connector is **API-based**, not an in-node agent — consistent with the
"no closed proprietary collector agents on endpoints" anti-pattern. It reads the
read-only Kubernetes API the same way `kubectl get` does.

The connector reads **RBAC + security-context configuration only**. It never
reads Secret values, ConfigMap values, container env, or pod logs — and its
ClusterRole does not even grant access to `secrets`. The cluster credential stays
source-side and never enters an evidence record or a platform push (canvas
invariant #3).

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler / a
CronJob in-cluster) — the recommended cadence is **every 24h**. This is
deliberately **not** "continuous monitoring": the interval is named honestly. A
watch-based event-driven profile (via the Kubernetes audit log) is a documented
follow-on, not part of v0.

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
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "clusterroles", "rolebindings", "clusterrolebindings"]
    verbs: ["get", "list"]
  - apiGroups: ["apps"]
    resources: ["deployments", "daemonsets", "statefulsets"]
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
role with write verbs (`create`/`update`/`patch`/`delete`/`deletecollection`),
wildcards (`*`), or **`get`/`list` on `secrets`**. The connector has no write
code path and no Secret-read code path; the only operations it issues are reads
of the four RBAC kinds + the three workload kinds + namespaces. The
`atlas-k8s permissions` output and `internal/k8sauth.DocumentedClusterRole()` are
the single source of truth — a unit test fails the build if anyone widens the
rule set into a write verb, a wildcard, or `secrets`.

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

# Read RBAC + workload security contexts, push evidence records.
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

| Flag                 | Subcommand | Required | Default                       | Notes                                                      |
| -------------------- | ---------- | -------- | ----------------------------- | ---------------------------------------------------------- |
| `--endpoint`         | both       | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                                     |
| `--token`            | both       | yes      | env `SECURITY_ATLAS_TOKEN`    | security-atlas bearer token                                |
| `--insecure`         | both       | no       | `false`                       | disables TLS; loopback endpoints only                      |
| `--cluster`          | `run`      | yes      | —                             | cluster identifier; scopes every record                    |
| `--environment`      | `run`      | yes      | —                             | environment scope tag; records are never emitted un-scoped |
| `--api-server`       | `run`      | no\*     | env `KUBERNETES_API_SERVER`   | API server URL (\*required, via flag or env)               |
| `--auth-mode`        | `run`      | no       | `kubeconfig-token`            | `kubeconfig-token` or `in-cluster`                         |
| `--rbac-control`     | `run`      | no       | `scf:IAC-21`                  | control id attached to RBAC records                        |
| `--workload-control` | `run`      | no       | `scf:CFG-02`                  | control id attached to workload records                    |
| `--skip-rbac`        | `run`      | no       | `false`                       | skip the `k8s.rbac_binding.v1` pull                        |
| `--skip-workload`    | `run`      | no       | `false`                       | skip the `k8s.workload_security_context.v1` pull           |

The cluster token is **only** read from `KUBECONFIG_TOKEN` (or the projected
in-cluster mount) — never a CLI flag — so it never lands in shell history. It is
never logged and never enters an evidence record (the resolved credential redacts
its token on every format path).

`register` announces `name=k8s-connector`,
`supported_kinds=[k8s.rbac_binding.v1, k8s.workload_security_context.v1]`, and
`profiles_supported=[pull]` to `ConnectorRegistryService.Register`. The
`profiles_supported` value describes how the connector retrieves data **from the
cluster** (a scheduled pull); the platform-side wire is always push
(invariant #3).

## Scope minimums

Every emitted record sets the minimum scope dimensions the connector-pattern
convention requires:

| Scope key     | Value                    | Source                   |
| ------------- | ------------------------ | ------------------------ |
| `cluster`     | the `--cluster` flag     | the `--cluster` flag     |
| `environment` | the `--environment` flag | the `--environment` flag |

`run` fails loudly when `--cluster` or `--environment` is unset rather than
pushing an un-scoped record.

`source_attribution.actor_id` follows the cross-connector convention
`connector:<vendor>:<service>@<version>` — `connector:k8s:rbac@<version>` for RBAC
records and `connector:k8s:workload@<version>` for workload records, where
`<version>` is the build's module version (or `dev` under `go run`).

## Idempotency

Each record's `idempotency_key` is
`sha256("<kind>|<identity>|<hour_truncated_observed_at>")` (see `internal/idem`).
RBAC identity = `scope/namespace/name`; workload identity = `kind/namespace/name`.
`observed_at` is truncated to the UTC hour, so two runs within the same hour for
the same binding / workload collapse to one ledger row; a run that crosses an hour
boundary writes a fresh record.

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

## What the connector never collects (the load-bearing guard)

The connector collects **RBAC rules/bindings + workload security-context flags
only**. It never reads, materializes, or emits:

- Secret values (its ClusterRole does not grant `secrets`)
- ConfigMap values
- container `env` / `envFrom` payloads (the workload client decodes only the
  security-context + host-namespace fields; env / volumes are discarded by the
  JSON decoder)
- pod logs

Tests assert that no Secret / ConfigMap / env material ever enters an evidence
record, and that the cluster token never appears in any formatted credential.

## Not in v0 (follow-ons)

The connector ships exactly two evidence surfaces. It does **not** ship:

- NetworkPolicy coverage evidence
- Pod-Security-Standards admission-config evidence
- Secret-inventory (metadata-only) evidence
- image-provenance / audit-log evidence
- a watch-based event-driven profile via the Kubernetes audit log
- cursor pagination (v0 reads a bounded first page per endpoint, `limit=500`)

These are filed as follow-on slices (see `docs/issues/487-kubernetes-connector.md`
and the spillover band 523–526).

## Tests

```sh
go test ./connectors/k8s/...
```

Unit tests fake the Kubernetes API surfaces (no live cluster, no real
credentials) and pin the RBAC normalization + wildcard heuristic, the workload
hardening verdict matrix, the security-context aggregation across containers, the
credential redaction, the read-only ClusterRole contract, and the `idem`
hour-window behavior. The integration test (in-package, bufconn platform — no
Postgres) exercises the full collect → build → SDK `Push` → push-receipt
round-trip for both kinds and asserts two same-hour pushes collapse to one
`record_id`, that emitted payloads carry config / authz metadata only (no Secret
/ ConfigMap / env values), and that the credential never surfaces in a formatted
log.
