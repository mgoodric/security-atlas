# 676 — Pervasive 503s on Next.js RSC prefetch (edge nginx capacity/keepalive fix) — decisions log

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

The 503-burst symptom was caught at **manual_review** — the 2026-06-10
demo-tenant UI audit (ATLAS-026). For an edge-proxy connection-budget exhaustion
under a browser-driven RSC-prefetch storm, manual_review is also the realistic
**target** tier: none of the four CI surfaces front the request through the edge
nginx proxy (the Playwright `Frontend · Playwright e2e` job talks to the Next.js
web server / atlas Go server directly, not through `deploy/docker/proxy/`), and
the slice-470 proxy that **does** use this `nginx.conf` exercises a single-login
XFF-overwrite assertion, not a concurrency burst. A faithful re-measure of the
`?_rsc=` 503 rate needs a live edge stack PLUS a real browser prefetch storm —
infrastructure this repo's CI deliberately does not stand up (see "What requires
a live edge stack" below). Building that load-test harness is out of scope for
this bounded fix; if the maintainer wants it as a standing gate it is a spillover
(named below). `actual=manual_review, target=manual_review` → not a CI-coverage
gap that this slice could close without over-building.

---

## Context

A large volume of Next.js App Router RSC requests (`?_rsc=...` GETs) across
`/dashboard`, `/dashboards/metrics`, `/board-packs`, and metric-detail pages
returned **HTTP 503** in the 2026-06-10 demo-tenant audit — dozens of `?_rsc=`
GETs 503ing in a single session, only a few returning 200.

The original slice framing was investigation-led (overload vs cascading backend
error vs handler bug, <70% confidence). A 2026-06-12 code-level scout (recorded
in the slice doc's "SCOUT + RESCOPE" note) narrowed it and removed the
confounders, converting it to a bounded fix:

1. **Confounders removed.** The three data-error pages the 503s concentrated on —
   board-packs (slice **673**), OSCAL component-definitions (slice **659**),
   metrics (slice **677**) — are now ALL MERGED on `main`
   (`docs/audit-log/659-*.md` and `docs/audit-log/677-*.md` present; 673 closed),
   so the symptom-503s those data-errors caused are gone. The remaining RSC 503s
   are NOT app-emitted (the `app/api/**` 503s are upstream-translations,
   unrelated).

2. **Root cause = edge-proxy connection-budget exhaustion.** The remaining RSC
   503s originate at the **edge nginx proxy** (`deploy/docker/proxy/nginx.conf`),
   which was configured with **`worker_connections 64`** and opened a **fresh
   upstream socket per request** (no `keepalive`, no `proxy_http_version 1.1` /
   `Connection ""`). Next.js App Router prefetches the RSC payload of EVERY
   visible `<Link>` as it enters the viewport, so a single navigation to a
   link-dense page fans out into many concurrent `?_rsc=` GETs in one burst.
   That burst exhausted the 64-connection budget (and churned a fresh
   connect/teardown per request on top), so nginx 503'd the overflow.

The `worker_connections 64` value was originally sized for the slice-470
single-login XFF-overwrite e2e (this is a TEST-ONLY proxy in that harness); it
was never sized for an interactive RSC-prefetch workload. The rescope is to tune
THIS proxy's capacity + keepalive so the burst is absorbed, scoped to the
`nginx.conf` edit + a re-verify. Scope explicitly excludes app code, routes, and
the Next.js config — this is an edge-proxy capacity/keepalive fix ONLY.

---

## D1 — `worker_connections` 64 → 1024

**Decision.** Raise `events { worker_connections }` from `64` to `1024`.

**Why.** 64 is below a realistic single-navigation RSC-prefetch fan-out on a
link-dense page; the overflow is exactly what 503'd. 1024 is a single deliberate
value: comfortably above any realistic prefetch fan-out for the solo-operator
edge box, while staying inside the default open-file budget (`RLIMIT_NOFILE` 1024) of the `nginx:alpine` image — so the worker can actually back the budget
with file descriptors without an `worker_rlimit_nofile` bump. Documented inline
with the RSC-prefetch rationale so a future reader understands the tuning.

**Rejected.** A larger value (e.g. 4096/8192) would have required also raising
`worker_rlimit_nofile` past the image default to be honest, adding a second
coupled knob for no demonstrated need on a single solo-operator upstream. Kept it
to one value, one budget. Per the brief: no rate-limiting, no caching layer
(over-engineering, out of scope).

## D2 — upstream `keepalive 32` + `proxy_http_version 1.1` + `Connection ""`

**Decision.** Add `keepalive 32;` to `upstream atlas_upstream { ... }`, and in
`location /` add `proxy_http_version 1.1;` + `proxy_set_header Connection "";`.

**Why.** Without a keepalive pool nginx opens and tears down a fresh upstream
socket for every proxied request — under the prefetch burst that is a storm of
short-lived connections plus ephemeral-port / TIME_WAIT churn, on top of the
`worker_connections` pressure. A pooled 32 idle upstream connections lets the
burst reuse warm sockets instead of each forcing a new connect. The
`proxy_http_version 1.1` + cleared `Connection` header are MANDATORY for the
keepalive to take effect: nginx defaults to HTTP/1.0 to the upstream (which has
no keepalive), and a forwarded client `Connection: close` would defeat reuse.
32 is a reasonable idle-pool size for a single upstream on a solo-operator box.

**Scope discipline.** The two new `proxy_set_header`/`proxy_http_version`
directives affect ONLY the proxy↔upstream framing. Every existing client-facing
directive is preserved verbatim — in particular the **hard X-Forwarded-For
overwrite** (`proxy_set_header X-Forwarded-For 203.0.113.10;`) that the slice-470
TRUSTED_PROXY_CIDRS harness asserts on is UNCHANGED; the keepalive directives are
added alongside it, not in place of it. `Host` / `X-Real-IP` / `X-Forwarded-Proto`
are untouched.

## D3 — no additional safeguards added

**Decision.** Ship `worker_connections` + `keepalive` only. No `proxy_next_upstream`
tuning, no `keepalive_timeout`/`keepalive_requests` overrides, no rate-limit, no
cache.

**Why.** The brief permits a small additional safeguard only if justified, and
warns against over-engineering. The capacity raise (D1) + keepalive reuse (D2) is
the minimal, sufficient pair for the named root cause. The `nginx:alpine`
defaults for `keepalive_timeout`/`keepalive_requests` are sane for a single
trusted upstream; overriding them would be speculative tuning with no measured
need. `proxy_next_upstream` is moot with a single `server` in the pool. Kept the
diff minimal.

---

## Verification

### What was verified (in this worktree)

- **`nginx -t` grammar + directive placement: PASS.** Ran the config through the
  `nginx:1.27-alpine` image (the exact image `docker-compose.proxy.yml` pins).
  - Against the real `server atlas:8080;` upstream, `nginx -t` reaches
    `[emerg] host not found in upstream "atlas:8080"` — a DNS-resolution failure
    for the compose service name, EXPECTED outside the compose network and
    present identically in the pre-slice config. It proves nginx parsed every
    directive (worker_connections / keepalive / proxy_http_version / Connection /
    the X-Forwarded-For overwrite) without a grammar error before reaching the
    runtime DNS step.
  - With the upstream temporarily stubbed to a resolvable `127.0.0.1:8080`,
    `nginx -t` reports: `the configuration file ... syntax is ok` /
    `test is successful`. This confirms the full config — including all four new
    directives, placed in the correct contexts (`worker_connections` in
    `events`; `keepalive` in `upstream`; `proxy_http_version` + `Connection ""`
    in `location`) — is valid nginx grammar. The stub was a throwaway; the
    committed `nginx.conf` keeps `server atlas:8080;`.
- **Diff is minimal + scoped.** Only `deploy/docker/proxy/nginx.conf` changed.
  No app code, no routes, no Next.js config, no compose change. The slice-470
  X-Forwarded-For overwrite assertion surface is preserved byte-for-byte.
- **Confounders confirmed merged** on `main` (659 / 673 / 677), so a residual
  RSC 503 after this fix would be a genuine new surface, not a data-error cascade.

### What requires a live edge stack + browser to confirm

- **The real `?_rsc=` 503-rate re-measure (AC).** A faithful re-verify needs the
  edge stack stood up (`docker-compose.edge.yml` or the
  `docker-compose.yml` + `docker-compose.proxy.yml` overlay) AND a real browser
  driving an RSC-prefetch storm against a link-dense page, then measuring the
  `?_rsc=` 503 rate before/after. This worktree cannot stand up a full edge stack
  - headless browser prefetch storm, and the repo's CI deliberately does not
    front requests through `deploy/docker/proxy/` (no existing harness to extend
    cleanly — the only `nginx.conf` consumer, the slice-470 proxy overlay, asserts
    a single-login XFF overwrite, not concurrency). Building a new prefetch
    load-test harness from scratch is out of scope for this bounded fix (it would
    be a spillover, not this slice).
- **Honest status of the AC.** The proxy capacity/keepalive fix is the
  deliverable and is validated at the config-grammar level. The end-to-end
  503-rate drop should be re-measured by the maintainer on the deployed edge box
  (`atlas-edge.home.gmoney.sh`) after this lands and Watchtower rolls the edge
  channel — that is the environment where the prefetch burst actually occurs.
  Stated plainly so the merge is not over-claimed.

---

## Spillover

None filed. The only out-of-scope item surfaced is the absence of a standing
edge-proxy concurrency/load-test harness (a real `?_rsc=` prefetch-storm gate).
That is a maintainer call — it is named here and in the slice doc's scope note as
a candidate follow-on rather than pre-filed, because whether it is worth the CI
cost of standing up an edge stack + browser prefetch storm in CI is a judgement
the maintainer should make, and the bounded proxy fix does not depend on it. If
the maintainer wants it filed, the next live slice number is 740+ (live max 733).
