# Slice 460 — decisions log

`NATS_URL` (and the broader server-read-vs-`.env.example` gap) — JUDGMENT slice.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is a config-hygiene / docs slice; the
"gap" it closes is a documentation-completeness gap, not a runtime defect — the
bundle's behavior is byte-for-byte unchanged.)

## Context

The slice spec (`docs/issues/460-...md`) framed `NATS_URL` as the single
server-read variable absent from the template, surfaced during the slice-430
config-reference audit, and offered two options: (1) leave as-is + add a
clarifying compose comment, or (2) promote `NATS_URL` to `.env.example` as a
commented external-NATS override.

The orchestrator directive widened the mandate: do a COMPLETE audit of every
env var the **server** reads versus what `.env.example` templates, and add any
genuinely operator-facing key that was missing.

## The full audit (server-read vs `.env.example`)

Method: `grep -rn "os.Getenv\|os.LookupEnv" cmd/atlas/ internal/`, dropped
`_test.go`, resolved constant-named keys to literals, then cross-checked each
against (a) `.env.example` and (b) whether `deploy/docker/docker-compose.yml`
plumbs it to the `atlas` service.

The slice spec's premise — "`NATS_URL` is the only server-read var absent" —
was **incorrect**. ~20 server-read vars are absent from the docker
`.env.example`. The discriminator that resolves the gap cleanly is **does
docker-compose plumb the key to the `atlas` service?** A key the bundle does
not plumb cannot be influenced by a docker `.env` value, so templating it in the
docker template would create a worse trap ("operator sets it, nothing happens").

### Keys compose ALREADY plumbs but the template did NOT declare → ADDED

| Key                              | Compose form  | Action                                      |
| -------------------------------- | ------------- | ------------------------------------------- |
| `ATLAS_OAUTH_TOKEN_RATE_PER_MIN` | `${...:-600}` | Added as **active** key (default 600).      |
| `OTEL_EXPORTER_OTLP_PROTOCOL`    | `${...:-}`    | Added as **commented** opt-in (OTel block). |
| `OTEL_SERVICE_NAME`              | `${...:-}`    | Added as **commented** opt-in.              |
| `OTEL_RESOURCE_ATTRIBUTES`       | `${...:-}`    | Added as **commented** opt-in.              |
| `OTEL_TRACES_SAMPLER`            | `${...:-}`    | Added as **commented** opt-in.              |
| `OTEL_TRACES_SAMPLER_ARG`        | `${...:-}`    | Added as **commented** opt-in.              |

These were the genuine inconsistency: the bundle made them overridable
(`${VAR...}`) but the template — the operator's authoritative copy-and-edit
surface — never named them. The existing OTEL block even listed them in prose
without templating them as discrete, uncommentable keys.

### `NATS_URL` — the slice exemplar → PROMOTED (spec option 2)

Compose previously hardcoded `NATS_URL: nats://nats:4222` (no `${}`), so it was
the one key that was both server-read AND not operator-overridable in the
bundle. Chose **option 2** over the spec's recommended option 1 because:

- the directive leaned explicitly toward adding it;
- external-NATS (a shared JetStream deployment) is a real self-host scenario;
- promoting it as a **commented** opt-in defaulting to today's hardcoded value
  is **zero behavior change** — compose now reads `${NATS_URL:-nats://nats:4222}`,
  which renders byte-identically when the variable is unset (verified via
  `docker compose config`);
- it satisfies AC-2's intent (a clarifying comment lands at the compose
  `NATS_URL:` line) AND AC-3 (template + config-reference row + drift green).

### Keys the bundle does NOT plumb → DELIBERATELY OMITTED (recorded, not added)

Read by the server only when run outside the compose bundle (bare binary /
bespoke deploy). Templating them in the **docker** `.env.example` would mislead.

| Key                                                                                                                          | Why omitted                                                                                                                                                  |
| ---------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `TRUST_FORWARDED_HEADERS`                                                                                                    | Reverse-proxy IP-capture toggle (security-relevant). Not plumbed by compose; the bundle is single-host. **Best candidate for a future slice** (see Revisit). |
| `ATLAS_AUDIT_SINK_PATH` / `_HMAC_KEY` / `_BUFFER_SIZE`                                                                       | Audit-sink durability knobs; not plumbed by compose.                                                                                                         |
| `OSCAL_SIGNING_KEY`, `ATLAS_OSCAL_SIGNING_MODE`, `ATLAS_COSIGN_KMS_REF`, `ATLAS_COSIGN_BINARY`, `ATLAS_OSCAL_ALLOW_EMBEDDED` | OSCAL export-signing config; not plumbed by compose; the bundle uses the ephemeral-signer default.                                                           |
| `ATLAS_EXPORT_MAX_CONCURRENT_PER_USER`                                                                                       | Export concurrency cap; not plumbed by compose.                                                                                                              |
| `ATLAS_ENABLE_DEMO_SEED`                                                                                                     | Demo-seed toggle; CLI/dev surface, not plumbed by compose.                                                                                                   |
| `CHROME_DEBUG_URL`                                                                                                           | Remote-Chrome for PDF render; test-harness knob.                                                                                                             |

### Keys read but NOT operator config (internal compose wiring) → correctly absent

`ATLAS_GRPC_ADDR`, `ATLAS_HTTP_ADDR` (in-container bind addrs; operators change
host _ports_ via the templated `*_PORT` vars), `ATLAS_ISSUER_URL`,
`ATLAS_DATA_DIR`, `ATLAS_KEYSTORE_PATH` (fixed compose-network wiring / volume
paths), `DATABASE_URL` (compose maps `DATABASE_URL_MIGRATE`→`DATABASE_URL`; the
templated key is `DATABASE_URL_MIGRATE`), `OSCAL_BRIDGE_ADDR`,
`ATLAS_OAUTH_DEPRECATION_URL`.

### Dev-only cadence knobs (the code itself classifies these) → correctly absent

`ATLAS_KEY_ROTATION_INTERVAL`, `ATLAS_EXCEPTION_EXPIRY_INTERVAL`,
`ATLAS_DECISION_OVERDUE_INTERVAL`, `ATLAS_EVAL_RECOMPUTE_INTERVAL`,
`ATLAS_FRESHNESS_DRIFT_TICK_CHECK`, `ATLAS_METRICS_INTERVAL`. Every one is
annotated in `cmd/atlas/main.go` as "overrides the cadence for dev loops" with a
sane production default (24h / hourly / 15-min). The project's own code already
classifies them as dev knobs, not operator config.

### Dead template entries

None found. Every active + commented key in `.env.example` resolves to a real
server-read (or compose-plumbed) variable — the slice-430 drift guard's stale-key
check (check 3) confirms this and stays green.

## Decisions made

1. **Promote `NATS_URL` (option 2), not leave-as-is (option 1).** Commented
   opt-in defaulting to the bundled address; compose now honors
   `${NATS_URL:-nats://nats:4222}`. Confidence: **high** (zero behavior change,
   verified by `docker compose config`).
2. **Add the 6 compose-plumbed-but-untemplated keys** (`ATLAS_OAUTH_TOKEN_RATE_PER_MIN`
   active; 5 OTel knobs commented). Confidence: **high** (these were a real
   template-vs-bundle inconsistency).
3. **Use the "does compose plumb it?" test as the inclusion boundary** for the
   docker template, rather than "does the server read it anywhere?" Confidence:
   **high** — avoids the constitutional anti-pattern of "a knob nobody turns"
   and the worse trap of a docker-template key the bundle silently ignores.
4. **Document the omitted operator-facing-but-unplumbed keys here** rather than
   half-wiring them. Wiring `TRUST_FORWARDED_HEADERS` / audit-sink / OSCAL-signing
   through compose is a real feature change with its own threat model — out of
   scope for an S-sized config-hygiene slice. Confidence: **medium** (defensible
   scope cut; a maintainer may want `TRUST_FORWARDED_HEADERS` sooner — see below).
5. **Edited `docs-site/docs/configuration.md` body table rows** to keep the
   slice-430 drift guard green. This touches `docs-site/` only in the table
   body — NOT the mkdocs nav, which sibling slice 432 owns. The drift guard
   (`just config-reference-drift-check`) is green: 35 vars (26 active + 9 opt-in).
   Confidence: **high**.

## Revisit once in use

- **`TRUST_FORWARDED_HEADERS`** — the strongest deferred candidate. The moment a
  self-host operator fronts the bundle with a reverse proxy/load balancer (the
  common production topology), correct client-IP capture on session rows needs
  this. File a follow-up slice to (a) plumb it through the `atlas` compose
  service and (b) template it with the X-Forwarded-For trust caveat. Until then
  it is documented here, not in the template.
- **Audit-sink + OSCAL-signing knobs** — promote to the template if/when the
  bundle grows first-class support for an external audit-log path or KMS-backed
  export signing.
- **`NATS_URL` external-cluster path** — once a real external-NATS deployment
  exists, validate the override end-to-end (the bundle e2e only exercises the
  in-compose default today).

## Confidence summary

| Decision                                    | Confidence |
| ------------------------------------------- | ---------- |
| Promote `NATS_URL` as commented opt-in      | high       |
| Add 6 compose-plumbed keys                  | high       |
| "compose-plumbs-it" inclusion boundary      | high       |
| Defer unplumbed operator keys (doc-only)    | medium     |
| Edit `configuration.md` body rows (not nav) | high       |
