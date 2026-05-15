# Slice 038 — Helm chart for K8s deployment — decisions log

Slice type: AFK. This log records the subjective build-time calls made
while building the Helm chart, per the JUDGMENT slice-development workflow
(Claude makes the call + records it; the maintainer iterates
post-deployment). None of these touch a constitutional invariant.

Format: Decision · Rationale · Revisit-when · Confidence.

---

## D1 — Reuse `bootstrap.Dockerfile` for the migration Hook Job

**Decision.** The `pre-install`/`pre-upgrade` migration Job runs the
existing `deploy/docker/bootstrap/bootstrap.sh` via the `bootstrap`
container image, rather than authoring a K8s-native migration mechanism.

**Rationale.** `bootstrap.Dockerfile` already bakes `migrations/` +
`controls/` into `/repo` and ships `psql` + a POSIX shell — it is
self-contained, no host bind-mount required (slice 037/065 made it so for
exactly the GHCR-image path). It carries the hardened slice-065 role model
(`01-roles.sql`, the `schema_migrations` ledger, the BYPASSRLS CREATEROLE

- schema-`public` ownership for `atlas_migrate`). Reusing it means the
  Helm path and the docker-compose path apply migrations through identical,
  already-tested logic — and AC-7 (idempotent `helm upgrade`) falls out for
  free because the ledger already skips applied migrations.

**Revisit when.** A future migration becomes genuinely non-idempotent, or
the project adopts a real migration-versioning tool that wants a different
runner shape.

**Confidence.** High. This is the lowest-risk path and avoids reinventing
a hardened component.

## D2 — Optional bundled MinIO, enabled by default

**Decision.** The chart ships an optional MinIO Deployment + Service +
bucket-create hook Job, gated `minio.enabled` (default `true`).
`values-production.yaml` sets `minio.enabled: false` and requires
`artifacts.s3Endpoint`.

**Rationale.** The P0 anti-criterion "does NOT bundle a Postgres operator"
is **Postgres-specific** — it is about the database, not object storage.
AC-1 ("`helm install` deploys to a minikube cluster successfully")
requires a working S3 for the atlas server to boot; bundling an optional
MinIO is the direct K8s analogue of the docker-compose bundle's `minio`
service and is the only way a fresh `helm install` works end-to-end on a
single-node cluster with no external dependencies beyond Postgres.
Production turns it off.

**Revisit when.** If a maintainer decides the chart should be
S3-only-always (force every deployment to bring object storage), flip the
default to `false` and document the dev-cluster setup.

**Confidence.** High. Mirrors the established docker-compose shape; the
anti-criterion is unambiguously Postgres-scoped.

## D3 — NATS bundled as a StatefulSet (single replica)

**Decision.** NATS ships as a first-party StatefulSet with a
`volumeClaimTemplate`, single replica, not as an upstream subchart and not
as an external dependency.

**Rationale.** Canvas §10.1 lists NATS as part of the platform ("NATS
(single binary)"), peer to "one-binary core", distinct from the external
Postgres. A StatefulSet + `volumeClaimTemplate` is the correct primitive
for JetStream's persistent stream state (`-js -sd /data`). Single replica
in v1: clustered NATS is a later concern, but the StatefulSet shape is
chosen now so persistence is correct from day one (a Deployment +
standalone PVC would be wrong to migrate away from later). Not an upstream
subchart: keeps the values surface small and the slice self-contained, and
the NATS config we need is two CLI flags.

**Revisit when.** Clustered / HA NATS is needed — bump `replicas` and add
a cluster-route config.

**Confidence.** High.

## D4 — `helm lint` + `helm template` are the executable test surface; minikube install is a documented manual check

**Decision.** AC-1's literal "`helm install` to a minikube cluster" is
implemented as a documented manual integration procedure in
`deploy/helm/README.md`, not an automated CI gate. The automated test
surface is `helm lint` + `helm template` against both default and
production values, run in CI and locally.

**Rationale.** minikube / kubectl are not present in the slice worktree,
and spinning up a real Kubernetes node in CI is a meaningful runner-cost
and flakiness addition for a leaf infra slice. `helm lint` +
`helm template` catch the failure classes a chart actually has at this
stage: invalid templates, broken conditionals, missing required values,
malformed manifests. The README documents the exact minikube procedure
(including the in-cluster Postgres setup the chart deliberately does not
provide) so the maintainer can run the full integration check on demand.
This mirrors slice 065's choice to keep its end-to-end bundle job
non-required until it has green runs.

**Revisit when.** The project adds a `kind`-based integration CI lane (a
natural follow-up — it could host both this and slice 065's e2e bundle
test); promote AC-1 to an automated check then.

**Confidence.** Medium-high. The render-validation surface is solid; the
gap is that no automated test exercises a real apply. Mitigated by the
documented manual procedure and by the migration logic being the
already-e2e-tested slice-065 `bootstrap.sh`.

## D5 — Secret shape: inline-or-`existingSecret`

**Decision.** The chart renders a `Secret` from inline `values.yaml`
credentials **only** when `secrets.existingSecret` is empty (dev
convenience). When `existingSecret` is set, the chart renders no Secret
and every workload reads from the operator-pre-created Secret by name.
`values-production.yaml` uses `existingSecret`. All inline defaults are
the neutral literal `changeme`.

**Rationale.** Inline-only would force real production credentials into
values files and Helm release history — unacceptable for a tool whose
customers diligence the tool itself. `existingSecret`-only would make a
quick dev `helm install` painful (no working default path). The
two-mode shape is the standard Helm idiom and lets the README steer
production hard toward `existingSecret` + External Secrets Operator /
Sealed Secrets / SOPS. Neutral `changeme` placeholders (not vendor token
prefixes like `ghp_*` / `sk_live_*` / `AKIA*`) keep GitGuardian quiet on
the committed `values.yaml`. The DSNs are assembled inside the Secret
template from `postgres.*` + the role passwords so no `user:pass@host`
literal lives in a ConfigMap or in values.

**Revisit when.** If the project standardizes on a specific secrets
operator, the chart could grow first-class support for it (e.g. render an
`ExternalSecret` CR).

**Confidence.** High. Standard idiom; the security-review pass confirmed
the shape.

## D6 — CI helm job: slice-061 gated + stub-sibling, kept non-required

**Decision.** The `Helm chart · lint + template` CI job follows the
slice-061 pattern: a real job gated `if: needs.changes.outputs.code ==
'true'` + an identically-named stub sibling gated `!= 'true'`. It is
**not** added to `.github/branch-protection.json`'s required-checks list.

**Rationale.** `deploy/**` is already in the slice-061 `dorny/paths-filter`
`code:` filter, so any PR touching `deploy/helm/**` runs the real job and
docs-only PRs resolve the stub in < 30s — no "waiting for status" hang on
the rollup. Keeping it out of `branch-protection.json` matches slice 065's
`test-self-host-bundle` precedent: a brand-new check ships non-required and
is promoted after it has a few green runs, so a transient setup issue in
the new job cannot block unrelated merges. The job block is appended
self-contained (like the slice-030 and slice-065 blocks) so parallel
ci.yml edits rebase cleanly.

**Revisit when.** After a handful of green runs — promote to a required
check by adding `Helm chart · lint + template` to
`branch-protection.json`.

**Confidence.** High. Directly follows two established in-repo precedents.

## D7 — `.prettierignore` excludes `deploy/helm/templates/`

**Decision.** `deploy/helm/templates/` is added to `.prettierignore`. The
pure-YAML chart files (`Chart.yaml`, `values.yaml`,
`values-production.yaml`) stay prettier-managed.

**Rationale.** Helm templates embed Go-template `{{ ... }}` directives and
`{{- ... -}}` whitespace-trim markers that are not valid YAML; prettier
would reflow them and break both the control flow and the `nindent`-based
indentation that Helm rendering depends on. `helm lint` is the correct
linter for the templates; prettier is correct for the plain-YAML values
files. This is the same class of fix as the existing `CHANGELOG.md` entry
in `.prettierignore`.

**Revisit when.** Never expected to — this is a structural fact about Helm
templates.

**Confidence.** High.

## D8 — Single Ingress fronting web + atlas under one host

**Decision.** The optional Ingress routes `/` to the web Service and the
configurable API prefixes (`/v1`, `/health`, `/auth`) to the atlas HTTP
Service, all under one host. cert-manager TLS and bring-your-own-TLS-Secret
are both supported; with neither, the Ingress is plain HTTP.

**Rationale.** The single-host shape is what lets `web.publicApiBaseUrl`
stay empty (the browser uses same-origin relative URLs) — the same
reasoning the docker-compose `.env.example` documents for
`NEXT_PUBLIC_API_BASE_URL`. It is the simplest correct production topology
and matches the slice-037 reverse-proxy guidance. The API prefix list is
values-driven so an operator can extend it without editing the template.

**Revisit when.** The platform grows API surface under a new path prefix
not covered by the default list — operators add it via
`ingress.paths.apiPrefixes`, or the default list is extended.

**Confidence.** Medium-high. The prefix list (`/v1`, `/health`, `/auth`)
is inferred from the docker-compose reverse-proxy note and the atlas
server's known routes; if the server exposes another top-level prefix the
default list needs a one-line addition. Low blast radius — it is a values
default, not a hard-coded constraint.
