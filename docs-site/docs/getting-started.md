# Getting started

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - The order to bring an existing security program into security-atlas
    - Which credential each step needs
    - How to check each step worked before moving on
<!-- prettier-ignore-end -->

This is the front door for an adopter: a fresh clone at the start, and at
the end a working program with a catalog, a framework, your controls,
evidence and a risk register. Each step is short, says how to check it
worked, and links to the page with the detail.

Where a step depends on work that has not shipped yet, the step says what
to do today and links the issue.

| Step                                                             | You end with                                       |
| ---------------------------------------------------------------- | -------------------------------------------------- |
| [1. Choose a deployment](#1-choose-a-deployment)                 | Docker or native, and the services you need        |
| [2. Install and bootstrap](#2-install-and-bootstrap)             | A running, seeded install                          |
| [3. Sign in and get credentials](#3-sign-in-and-get-credentials) | A user token, and which credential each step needs |
| [4. Load the SCF catalog](#4-load-the-scf-catalog)               | The SCF spine, sample or a real release            |
| [5. Enable frameworks](#5-enable-frameworks)                     | A framework with requirements, and its scope       |
| [6. Bring your controls](#6-bring-your-controls)                 | Controls anchored to SCF                           |
| [7. Evidence](#7-evidence)                                       | A first evidence record                            |
| [8. Risks and policies](#8-risks-and-policies)                   | A risk register and your policies                  |
| [9. First audit period](#9-first-audit-period)                   | An open, then frozen, audit period                 |
| [10. Where to go next](#10-where-to-go-next)                     | Dashboard, board pack, MCP                         |

## 1. Choose a deployment

| Path                                | Use it when                                | Guide                               |
| ----------------------------------- | ------------------------------------------ | ----------------------------------- |
| Docker (`docker compose`)           | You can run containers. The shortest path. | [Install](install.md)               |
| Native (Go, Postgres 16, Node only) | No approved container runtime on the host. | [Native install](install-native.md) |

Postgres 16 is the only service the platform needs. NATS, an
S3-compatible store, the OSCAL bridge and an OTLP endpoint are optional;
the
[optional services table](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md)
says what you lose without each. The Docker bundle runs all of them.

The commands below assume the Docker bundle on the same host: HTTP on
`localhost:8080`, gRPC on `localhost:50051`, the web app on
`localhost:3000`. On a native install, use the addresses from your
`atlas.env`. You also need `curl`, `jq`, and Go to build the CLI (the
toolchain `go.mod` pins; a Homebrew `go` fetches it).

## 2. Install and bootstrap

Follow [Install](install.md) (or [Native install](install-native.md))
to the end of "confirm it is live". In short, for Docker:

```sh
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas
cp deploy/docker/.env.example deploy/docker/.env
${EDITOR:-vi} deploy/docker/.env     # replace every CHANGE_ME
just self-host-up
```

**Decide two things before the first `just self-host-up`.** The bootstrap
loads a small SCF sample catalog and uploads 50 stock SOC 2 controls. To
start from a real SCF release, or with none of the stock controls, set
`SCF_CATALOG` and `CONTROLS_DIR` in `deploy/docker/.env` first; see
[Bring your own SCF release](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md#bring-your-own-scf-release).
Both imports add and update, never delete, so changing them after the
first boot leaves the stock rows in place.

You need the CLI on the host for later steps. It builds without a C
toolchain:

```sh
CGO_ENABLED=0 go build -o ./bin/ ./cmd/atlas-cli
export PATH="$PWD/bin:$PATH"
```

**Check:**

```sh
curl -fsS http://localhost:8080/ready
# {"status":"ready","db":"ok"}
docker compose -f deploy/docker/docker-compose.yml logs --no-log-prefix atlas-bootstrap | tail -2
# [bootstrap] uploaded 50 control bundles
# [bootstrap] bootstrap complete — security-atlas is seeded and ready
```

## 3. Sign in and get credentials

Open <http://localhost:3000> and sign in with `ATLAS_DEFAULT_USER_EMAIL`
and `ATLAS_DEFAULT_USER_PASSWORD` from your `.env`. If the first sign-in
fails, see [First-time login](troubleshooting/first-login.md). Its
`credentials issue --reset-bootstrap` recovery currently answers 401
([#2121](https://github.com/mgoodric/security-atlas/issues/2121)); the
page's other recovery steps still apply.

Three kinds of credential reach the platform, and each later step needs
a particular one:

| Credential   | How you get it                                             | Works on                                     | Used in steps |
| ------------ | ---------------------------------------------------------- | -------------------------------------------- | ------------- |
| User token   | Sign in: the UI, or `POST /auth/local/login` from a script | REST (`/v1/...`) and the CLI's HTTP commands | 4, 5, 6, 8, 9 |
| API key      | `atlas-cli credentials issue`, with an admin bearer        | gRPC evidence push only                      | 7             |
| OAuth client | `atlas-cli oauth issue-client`, then `client_credentials`  | REST, for long-running machine callers       | optional      |

The REST API rejects API keys, and the gRPC push endpoint accepts only
API keys. [OAuth grants](oauth-grants.md) covers the OAuth flows.

To get a user token in a shell, sign in from a script. The password is
read at a prompt, so it stays out of your shell history and out of every
command's arguments:

```sh
export ATLAS=http://localhost:8080
export ATLAS_EMAIL=<ATLAS_DEFAULT_USER_EMAIL>
printf 'Password: '; read -rs ATLAS_PASSWORD; echo; export ATLAS_PASSWORD

TOKEN=$(jq -n '{email: env.ATLAS_EMAIL, password: env.ATLAS_PASSWORD}' \
  | curl -fsS -X POST "$ATLAS/auth/local/login" -H 'Content-Type: application/json' -d @- \
  | jq -r .token)
unset ATLAS_PASSWORD
```

`$TOKEN` is your own admin session, for this interactive first run only.
Keep it in this shell: never write it to a file, a CI secret or a cron
job. Automation gets its own credential, an OAuth client or a scoped API
key. The commands below pass it as `-H "Authorization: Bearer $TOKEN"`
or `--token "$TOKEN"`, which puts it in the process's arguments, where
other users of the same host can read it with `ps`; run them on a host
only you use.

The token lasts one hour; run the same command again when a call starts
answering 401. `tenant_id` may be left out of the login body while the
install has one tenant. Once it has several, the body needs it; see
[Signing in from a script](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md#signing-in-from-a-script).

**Check:**

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/controls" | jq .count
# 50
```

Fifty is the stock SOC 2 set; with `CONTROLS_DIR` pointed elsewhere,
expect your own count. Uploads always land in the tenant of the
credential that made them, so a control uploaded with this token is
visible to you here.

## 4. Load the SCF catalog

The Secure Controls Framework is the spine: every framework requirement
maps to SCF anchors, and every control is anchored to one. The bootstrap
loaded it in step 2.

| Catalog                                              | What it holds                                                    | When to use it          |
| ---------------------------------------------------- | ---------------------------------------------------------------- | ----------------------- |
| Stock sample (`migrations/fixtures/scf-sample.json`) | 77 anchors: enough for the bundled crosswalks and stock controls | Trying the platform out |
| A real SCF release, pinned                           | The full catalog (~1,400 anchors)                                | Running your program    |

A real release is a JSON file you convert from SCF's download and point
`SCF_CATALOG` at; the conversion and the upgrade path are in
[Bring your own SCF release](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md#bring-your-own-scf-release).
A newer release later becomes the current version and the old one is
kept for audit replay.

**Check:**

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/frameworks/scf/versions" | jq -c '.versions[]'
# {"id":"…","version":"test-2026.1","status":"current","effective_from":"2026-01-15"}
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/anchors?include=state&limit=1" | jq '.catalog_total'
# 77
```

`test-2026.1` and 77 are the sample. A real release shows its own
version and count.

## 5. Enable frameworks

The bootstrap imported SOC 2 (2017). Every other bundled framework is one
command; the bootstrap log prints it for each one. For example,
ISO/IEC 27001:2022:

```sh
docker compose -f deploy/docker/docker-compose.yml run --rm \
  --entrypoint atlas-cli atlas-bootstrap \
  catalog import-crosswalk /repo/data/crosswalks/iso27001-2022.yaml
# framework=iso27001:2022 framework_version_id=… new_version=true
# requirements created=93 updated=0 unchanged=0 differed=0
```

On a native install, run `atlas-cli catalog import-crosswalk
data/crosswalks/iso27001-2022.yaml` with `DATABASE_URL` set. The bundled
files are in `data/crosswalks/`: SOC 2, ISO 27001, NIST CSF 2.0, PCI DSS
4.0 and the HIPAA Security Rule.

Crosswalks also come from SCF's own workbooks, and the sources differ in
how much they tell you about each mapping. The full SCF workbook gives
anchor IDs only, so every edge from it lands as a draft placeholder; the
OLIR STRM workbook gives the relationship type and strength. ISO 27001
spans two workbook columns and needs both. All of this is in
[SCF workbook crosswalk import](https://github.com/mgoodric/security-atlas/blob/main/docs/operator/scf-crosswalk-import.md);
[Framework setup](framework-setup.md) covers reading a crosswalk.

**Check:** the importer's `requirements created=` line above, and the
framework list:

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/frameworks" | jq -r '.frameworks[].slug'
# iso27001
# scf
# soc2
```

### Set the framework scope

A framework scope says which part of your environment a framework
covers, as a predicate over scope dimensions: the PCI cardholder data
environment, the SOC 2 system boundary. It moves from draft through
review and approval to active, and approval records the document that
justifies the boundary. The **Framework scopes** page in the UI walks
through it; from a shell, for SOC 2 over the seeded `environment = prod`
cell:

```sh
FV=$(curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/frameworks" \
  | jq -r '.frameworks[] | select(.slug == "soc2") | .latest_version_id')

SCOPE_ID=$(curl -fsS -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  "$ATLAS/v1/framework-scopes" \
  -d "{\"framework_version_id\":\"$FV\",\"name\":\"SOC 2 system boundary\",
       \"predicate\":{\"op\":\"eq\",\"dim\":\"environment\",\"value\":\"prod\"}}" \
  | jq -r .framework_scope.id)

curl -fsS -X PATCH -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/framework-scopes/$SCOPE_ID/submit"
curl -fsS -X PATCH -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  "$ATLAS/v1/framework-scopes/$SCOPE_ID/approve" \
  -d '{"approval_evidence_file_url":"<link to the boundary document>",
       "approval_evidence_file_hash":"<its sha256, 64 hex characters>"}'
curl -fsS -X PATCH -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  "$ATLAS/v1/framework-scopes/$SCOPE_ID/activate" -d "{\"effective_from\":\"$(date -u +%FT%TZ)\"}"
```

**Check:**

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/framework-scopes" | jq -c '.framework_scopes[] | {name, state}'
# {"name":"SOC 2 system boundary","state":"activated"}
```

[Scope](primitives/scope.md) explains the predicate language and why a
framework scope intersects with each control's own applicability.
Per-requirement applicability decisions are recorded on the scope, and
`GET /v1/framework-scopes/applicability/export?framework_version=$FV&format=csv`
(or `format=xlsx`) exports them as a Statement of Applicability.

## 6. Bring your controls

A control is yours: your wording, your owner, your evidence. It anchors
to one SCF anchor, and through that anchor satisfies every framework
requirement mapped to it. You never write one control per framework.

**Today, a control is a bundle**: a directory holding a `control.yaml`.
The 50 stock bundles in `controls/soc2/` are working examples to copy or
fork. A minimal one:

```yaml
# my-controls/acme_mfa_all_staff/control.yaml
bundle_schema_version: "1"
bundle_id: acme_mfa_all_staff
title: "MFA is enforced for every staff account"
description: |
  Every workforce identity signs in through the IdP with MFA enforced.
scf_anchor_id: IAC-37
implementation_type: manual_attested
owner_role: it
lifecycle_state: active
freshness_class: quarterly
```

Validate it locally, then upload it with your user token:

```sh
atlas-cli controls validate my-controls/acme_mfa_all_staff
# OK bundle_id=acme_mfa_all_staff title="MFA is enforced for every staff account" …

atlas-cli controls upload --endpoint "$ATLAS" --token "$TOKEN" my-controls/acme_mfa_all_staff
# { "control_id": "470d1eb3-…", "bundle_id": "acme_mfa_all_staff", "version": 1, "is_new_bundle": true, … }
```

The upload may warn that the bundle declares no test cases. That is
expected for a manual control: there is no evidence query to test.

When no SCF anchor fits a control, don't force one. Record the fit with
`anchor_fit` and a note; see
[Controls with no SCF equivalent](framework-setup.md#controls-with-no-scf-equivalent).

**From a spreadsheet.** `atlas-cli controls import` uploads a CSV or
XLSX of your existing controls, with the criteria each was tested under,
and stages them with ranked SCF anchor candidates:

```sh
atlas-cli controls import controls.csv --framework soc2 --endpoint "$ATLAS" --token "$TOKEN"
atlas-cli controls import show <batch id> --endpoint "$ATLAS" --token "$TOKEN"
```

Approving a staged row into a real control has not shipped yet
([#1734](https://github.com/mgoodric/security-atlas/issues/1734)).
Until it does, use the candidates to pick each control's
`scf_anchor_id`, and write the bundle as above.

**Check** that the control reaches the criteria it was audited under.
Take its `control_id` from the upload output:

```sh
CONTROL_ID=<control_id from the upload>
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/controls/$CONTROL_ID/coverage" \
  | jq -c '.requirements[] | {framework: .framework_slug, code}'
# {"framework":"iso27001","code":"A.8.5"}
# {"framework":"soc2","code":"CC6.6"}
```

One control, anchored once, reaches both frameworks.

If a criterion your auditor tested the control under is missing, the
anchor does not map to it. Choose a closer anchor, or record the gap
with `anchor_fit`. Mapping a requirement directly for your organisation
is planned as tenant overlay edges
([#1735](https://github.com/mgoodric/security-atlas/issues/1735)).

## 7. Evidence

Evidence lands in an append-only ledger. A first record is one manual
attestation, pushed with an API key:
[Push a first manual attestation](first-manual-attestation.md) walks
through issuing the key, the payload, and the common rejections.

Issuing the key takes an admin bearer, which on a new install is the
bootstrap token: `ATLAS_BOOTSTRAP_TOKEN` in your `.env`, or on a native
install the `bootstrap credential issued` line in the server log. Paste
it at the prompt; the CLI reads it from `SECURITY_ATLAS_TOKEN`, so it
never appears in the command's arguments. In short:

```sh
export SECURITY_ATLAS_ENDPOINT=localhost:50051
printf 'Bootstrap token: '; read -rs BOOTSTRAP_TOKEN; echo

KEY_JSON=$(SECURITY_ATLAS_TOKEN="$BOOTSTRAP_TOKEN" atlas-cli credentials issue --insecure \
  --kinds manual.attestation.v1 --ttl 24h)
KEY_ID=$(jq -r .handle.id <<<"$KEY_JSON")
export SECURITY_ATLAS_TOKEN=$(jq -r .bearerToken <<<"$KEY_JSON")
unset KEY_JSON

cat > attestation.json <<'EOF'
{
  "attestor": "jane.doe@example.com",
  "statement": "MFA is enforced for every staff account in the IdP.",
  "supporting_uri": "https://wiki.example.com/security/mfa"
}
EOF

atlas-cli evidence push --insecure \
  --kind manual.attestation.v1 --schema-version 1.1.0 \
  --control "$CONTROL_ID" --scope '{"environment":"prod"}' \
  --observed-at "$(date -u +%FT%TZ)" --result pass \
  --payload @attestation.json \
  --idempotency-key "attest-$CONTROL_ID-$(date -u +%F)" \
  --actor-type human --actor-id jane.doe@example.com
# { "recordId": "…", "hash": "…", "ingestedAt": "…", "credentialId": "key_…" }
```

`--control` takes the control's UUID or its bundle ID
(`acme_mfa_all_staff`); either links the record to the control.

The key exists for this first push. Keep it in this shell, never in a
shell rc file, and revoke it once the push has landed:

```sh
SECURITY_ATLAS_TOKEN="$BOOTSTRAP_TOKEN" atlas-cli credentials revoke --insecure --id "$KEY_ID"
# revoked key_…
unset BOOTSTRAP_TOKEN SECURITY_ATLAS_TOKEN
```

A key that pushes on a schedule belongs in your secret manager, issued
for that job alone.

Two more sources of evidence:

- **Connectors** collect evidence from the systems you run, each as its
  own process pushing with its own API key. Each shipped connector has a
  README under
  [`connectors/`](https://github.com/mgoodric/security-atlas/tree/main/connectors);
  the [AWS connector](https://github.com/mgoodric/security-atlas/blob/main/connectors/aws/README.md)
  is the usual first one. [Connector authoring](connector-authoring.md)
  covers writing your own.
- **Historical evidence**, such as last year's audit results with their
  real dates, has no import yet
  ([#1742](https://github.com/mgoodric/security-atlas/issues/1742)).
  Until it lands, keep prior reports in your document store and link
  them from an attestation's `supporting_uri`.

**Check:**

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/controls/$CONTROL_ID/evidence-summary" \
  | jq -c '.evidence.records[] | {evidence_kind, result, observed_at}'
# {"evidence_kind":"manual.attestation.v1","result":"pass","observed_at":"…"}
```

## 8. Risks and policies

**Risks.** The register defaults to NIST 800-30, likelihood and impact
from 1 to 5. Create risks in the **Risks** page, or from a shell:

```sh
curl -fsS -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  "$ATLAS/v1/risks" -d "{
    \"title\": \"Phished staff credentials reach production\",
    \"description\": \"A phished workforce credential is used to reach production systems.\",
    \"category\": \"confidentiality\",
    \"methodology\": \"nist_800_30\",
    \"inherent_score\": {\"likelihood\": 4, \"impact\": 4},
    \"treatment\": \"mitigate\",
    \"treatment_owner\": \"jane.doe@example.com\",
    \"linked_control_ids\": [\"$CONTROL_ID\"]
  }" | jq -c '.risk | {id, treatment, linked_control_ids}'
# {"id":"…","treatment":"mitigate","linked_control_ids":["…"]}
```

`category` is one of the tenant's categories
(`GET /v1/risks/categories`). `treatment` is `accept`, `mitigate`,
`transfer` or `avoid`. Each one asks for its own backing: `mitigate`
needs at least one linked control or action plan, and an accepted risk
records who accepted it and until when. More controls link later with
`POST /v1/risks/{id}/controls`, and the risk page shows the residual
score they bring. Risks nest: a parent shows the roll-up of its children. All
of this is in [Risks](primitives/risks.md). There is no register import
yet ([#2069](https://github.com/mgoodric/security-atlas/issues/2069)),
so an existing register goes in one `POST /v1/risks` per row.

**Policies.** `atlas-cli policy import <dir>` imports your approved
policies, one Markdown file each with YAML front matter, as published
policies that keep their approval history:

```markdown
---
title: Information Security Policy
version: "3.1"
approved_by_name: Jane Doe
approved_at: 2026-03-01
effective_date: 2026-03-15
linked_controls:
  - acme_mfa_all_staff
---

# Information Security Policy

...
```

```sh
atlas-cli policy import my-policies --http-endpoint "$ATLAS" --token "$TOKEN"
# imported     information-security.md -> 6a494018-…
# imported 1, skipped 0, failed 0
```

`atlas-cli policy import --help` lists the optional fields. With no
policies of your own, `atlas-cli policy seed-stock` loads five starting
drafts. See [Policy](primitives/policy.md).

**Check:**

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/risks" | jq -c '.risks[] | {title, treatment}'
# {"title":"Phished staff credentials reach production","treatment":"mitigate"}
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/policies" | jq -c '.policies[] | {title, status, version}'
# {"title":"Information Security Policy","status":"published","version":"3.1"}
```

## 9. First audit period

An audit period is a window over one framework version. While it is
open, evidence keeps flowing in. Freezing it fixes the evidence the
auditor samples from at `frozen_at`, so evidence that arrives later
does not change the auditor's view.

```sh
PERIOD_ID=$(curl -fsS -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  "$ATLAS/v1/audit-periods" \
  -d "{\"name\":\"SOC 2 Type II 2026\",\"framework_version_id\":\"$FV\",
       \"period_start\":\"2026-01-01T00:00:00Z\",\"period_end\":\"2026-12-31T23:59:59Z\"}" \
  | jq -r .audit_period.id)

# When the auditor is ready for a fixed evidence universe:
curl -fsS -X POST -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/audit-periods/$PERIOD_ID/freeze" \
  | jq -c '.audit_period | {status, frozen_at, frozen_hash}'
# {"status":"frozen","frozen_at":"…","frozen_hash":"…"}
```

`$FV` is the SOC 2 version ID from step 5. Sampling, walkthroughs,
findings and the OSCAL export bundle are in [First audit](first-audit.md).

**Check:**

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$ATLAS/v1/audit-periods" | jq -c '.audit_periods[] | {name, status}'
# {"name":"SOC 2 Type II 2026","status":"frozen"}
```

## 10. Where to go next

- **Dashboard.** The home page at <http://localhost:3000> shows posture
  per framework and evidence freshness; [Measuring your program](metrics.md)
  explains each number.
- **Board pack.** [Board reporting](board-reporting.md) generates the
  quarterly pack from the same data.
- **Assistant access.** [MCP server](mcp.md) lets Claude Desktop or
  Claude Code query and update your program.
- **Single sign-on.** [Connect an external IdP](oidc-setup.md) is meant
  to replace local sign-in. Today the IdP set on the admin SSO page does
  not reach production sign-in
  ([#2034](https://github.com/mgoodric/security-atlas/issues/2034)), so
  keep local sign-in until that lands.
- **A populated demo.** To see every page with data before your own is
  in, [load the demo dataset](https://github.com/mgoodric/security-atlas/blob/main/docs/getting-started/demo-seed.md)
  into a separate install.
- **Operations.** [Backup and restore](backup-restore.md) and
  [Upgrade](upgrade.md).

---

## Was this helpful?

Tell us in [GitHub Discussions](https://github.com/mgoodric/security-atlas/discussions).
