# Slice 470 — decisions log (trusted-proxy e2e seed harness)

JUDGMENT slice. The subjective build-time calls are recorded here; no human
sign-off gate. The maintainer iterates post-deployment.

- detection_tier_actual: integration
- detection_tier_target: integration

(One bug surfaced DURING the slice, and it was in the TEST HARNESS, not the
resolver. The first live proxy-mode run had AC-2 pass but AC-3 FAIL: a "direct
forging" login driven against atlas's HOST-published port had its forged
X-Forwarded-For honoured. Root cause: docker source-NATs a host-port request to
the bridge GATEWAY address (10.123.0.1), which is INSIDE the trusted /24 — so
atlas correctly treated it as a trusted hop. That is RIGHT walk behaviour
(`clientip.go` was never wrong) but the WRONG threat model for the assertion.
The fix is entirely test-side (D4 below): drive the forging login from a
container on a genuinely-untrusted second network (clientnet, 10.124.0.0/24).
Caught at the integration/e2e tier — exactly the tier whose job is to catch
multi-container-topology mistakes; target == actual. The resolver itself was
NOT touched and needed no change.)

## Summary

Slice 466 replaced `TRUST_FORWARDED_HEADERS` with a `TRUSTED_PROXY_CIDRS`
allowlist + a right-to-left X-Forwarded-For walk, and proved the walk at the
unit tier + over a real TCP connection (`clientip_server_test.go`). What it
could NOT prove — because the single-VM seed ships no proxy container (D4) —
was the **multi-container topology**: a real header-overwriting proxy process
in front of atlas, connecting from a container-network address inside
`TRUSTED_PROXY_CIDRS`. This slice lands exactly that e2e.

A header-overwriting nginx (`deploy/docker/proxy/nginx.conf`) is added in front
of atlas via a compose overlay (`deploy/docker/docker-compose.proxy.yml`), and
the existing self-host e2e harness (`deploy/docker/test-self-host-bundle.sh`)
gains a third mode, `proxy`, that:

- **AC-2** — logs in THROUGH the proxy and asserts the session row's
  `ip_address` equals the proxy-supplied client IP (`203.0.113.10`, TEST-NET-3).
- **AC-3** — logs in DIRECT to atlas forging `X-Forwarded-For: 198.51.100.66`
  and asserts the session row's `ip_address` is the direct container peer, NOT
  the forged TEST-NET value (the walk stops at the untrusted hop).

The `proxy` mode is added to the CI `test-self-host-bundle` matrix (AC-4).

## Decisions

### D1 — harness placement: extend the docker-compose self-host harness, NOT a Go integration package or a Playwright spec

**Decision:** implement the e2e as a third mode (`proxy`) of the existing
`deploy/docker/test-self-host-bundle.sh` (bash + docker-compose), layering a
`docker-compose.proxy.yml` overlay. NOT a Go integration-tagged package, NOT a
Playwright `web/e2e` spec.

**Why:** the slice's load-bearing requirement is a **real proxy process
connecting to atlas from a container-network address** — a genuine
multi-container topology. The Go integration tier (`go test -tags=integration`)
runs atlas in-process via `httptest`; it has no second container and no
distinct network peer, which is precisely the gap slice 466's existing
real-TCP unit tests (`clientip_server_test.go`) already cover from loopback.
The Playwright tier exercises the **browser↔web↔atlas** path, not a
proxy-in-front-of-atlas hop, and its harness mints bearers rather than driving
`/auth/local/login` with a controllable source-IP/XFF. The self-host harness,
by contrast, ALREADY stands up the full multi-container stack, ALREADY logs in
via `/auth/local/login`, and ALREADY reads DB rows back via `psql` as the
superuser (assertions 5–7). Adding a proxy container + two IP assertions reuses
all of that with the smallest new surface. This matches the harness's own
charter (slice 065: "end-to-end install in real deploy shapes").

**Shard-manifest impact:** NONE. No new Go integration package is created, so
`scripts/integration-shards.txt` and `scripts/check-integration-shard-coverage.sh`
are untouched. (Had this been a Go integration package, the slice would have
owned enrolling it per the slice-417 manifest.)

**Alternatives considered:** (a) Playwright + a compose proxy — rejected: the
Playwright harness mints test JWTs and asserts on rendered UI, not on a
session-row `ip_address` produced by a controllable network hop; bending it to
drive a raw login through a proxy with a forged XFF would be more scaffolding
than reusing the bash harness, for a less faithful network model. (b) A Go
integration test with a synthetic `RemoteAddr` — rejected: that is what the
unit tier already does; it proves nothing new about a real second container.

### D2 — proxy image: nginx:1.27-alpine

**Decision:** use `nginx:1.27-alpine` as the header-overwriting proxy.

**Why:** smallest, most-ubiquitous reverse proxy; a 40-line config does the one
thing the test needs (overwrite `X-Forwarded-For`, `proxy_pass` to atlas). The
image pulls cleanly (verified locally). Caddy/Traefik/Envoy would all work but
carry more surface for no gain here. The base bundle already pulls comparable
alpine images (postgres, nats), so nginx:alpine is in-keeping.

**Confidence:** high.

### D3 — assertion mechanism: read the session row's `ip_address` by exact session id

**Decision:** each login captures the `atlas_session` cookie (whose value IS
the session row id — `sessions.go` `CookieName`), then reads back
`SELECT ip_address FROM sessions WHERE id = <captured-id>` as the postgres
superuser. NOT a "most recent row" read, NOT a debug echo endpoint.

**Why:** pinning to the exact session id the login created removes any race
between the proxy login and the direct login writing two `sessions` rows. There
is no debug "echo my client IP" endpoint in the codebase, and adding one would
be a production-surface change for a test (rejected on anti-pattern grounds —
the harness already has superuser psql access, which is the existing pattern
for assertions 4–8). The `sessions.ip_address` TEXT column is the real
persisted output of `clientIP(r)` at session-create time
(`internal/api/auth/http.go:115`), so asserting on it is asserting on exactly
the production code path slice 466 ships.

**Confidence:** high.

### D4 — deterministic CIDR via TWO fixed-subnet networks (trusted + untrusted)

**Decision:** the overlay declares TWO explicit fixed-subnet networks. The
first, `atlasnet` (10.123.0.0/24, TRUSTED, `TRUSTED_PROXY_CIDRS` == this),
carries the proxy plus every platform service. The second, `clientnet`
(10.124.0.0/24, UNTRUSTED), carries the direct-forging client. atlas is
multi-homed on both. The AC-2 proxy login is driven through the proxy (its
address ∈ atlasnet ⇒ trusted hop); the AC-3 forging login is driven from a
`forging-client` container that lives ONLY on clientnet, so its peer (10.124.x)
is outside the allowlist ⇒ the forged header is rejected.

**Why two networks (the load-bearing slice-470 finding):** the FIRST live run
drove the AC-3 forging login against atlas's HOST-published port and the forged
IP was HONOURED. Root cause: docker source-NATs a host-port request to the
bridge GATEWAY (10.123.0.1), which is INSIDE the trusted /24 — atlas correctly
treated it as a trusted hop. That is right walk behaviour, wrong threat model.
The fix is a genuinely-untrusted second network: a client there connects from a
10.124.x peer that is really outside `TRUSTED_PROXY_CIDRS`, faithfully modelling
"a client that is NOT behind your trusted proxy". This is also why the assertion
could not simply use the host port + `-H X-Forwarded-For` (the AC-3 v1 shape).

**Why fixed subnets at all:** docker's default network hands out an arbitrary
`172.x` subnet that cannot be pinned ahead of time, so `TRUSTED_PROXY_CIDRS`
would be a guess. Fixed subnets make the allowlist deterministic and the test
reproducible. The whole /24 is trusted (not a single host) because the proxy
gets a dynamic address within it — trusting the proxy's subnet is the realistic
operator posture anyway (you trust your ingress network, not one pinned IP).

**Why a private RFC 1918 /24 (10.123.0.0/24) for the network but TEST-NET
(203.0.113.0/24 / 198.51.100.0/24) for the synthetic client/forged IPs:** the
network subnet must be a real, routable-on-the-docker-bridge private range
(TEST-NET ranges are not assignable to a live interface), whereas the
client/forged IPs are pure header VALUES that never bind an interface — TEST-NET
is correct there and guarantees no collision with a real routable address (and
no GitGuardian-relevant literal). 10.123.0.0/24 is an arbitrary, unlikely-to-
collide private /24.

**Confidence:** high.

### D5 — hard-overwrite X-Forwarded-For (not the `$proxy_add_x_forwarded_for` append form)

**Decision:** the nginx config uses `proxy_set_header X-Forwarded-For
203.0.113.10;` — a hard overwrite — not the common append idiom
(`$proxy_add_x_forwarded_for`).

**Why:** a header-scrubbing trusted proxy STRIPS whatever the client sent and
re-issues `X-Forwarded-For` from its own trusted view — that is the exact
deployment posture `TRUSTED_PROXY_CIDRS` assumes (clientip.go package doc +
slice 466 D-notes). The overwrite models that scrubbing faithfully and makes
the proxy-supplied value deterministic for the assertion. The append form would
let a client-forged prefix survive into the header the proxy forwards; that is a
different (mis-)configuration the slice is not testing.

**Confidence:** high.

### D6 — proxy mode reuses the bundled-mode Postgres auth path

**Decision:** `proxy` mode shares bundled mode's
`POSTGRES_HOST_AUTH_METHOD=trust` + repo `01-roles.sql` init path (the
mode-conditionals were widened from `= bundled` to
`= bundled || = proxy`); it does NOT take the external-mode shared-cluster
pre-staging.

**Why:** the proxy overlay is orthogonal to Postgres auth — it adds a hop in
front of atlas, nothing about the DB. Reusing the simplest (bundled) DB path
keeps the new mode focused on the one thing it proves. The external-mode shape
is independently exercised by the `external` matrix leg.

**Confidence:** high.

## Revisit

- **R1 (D4):** if a future docker/compose version changes default IPAM such
  that `10.123.0.0/24` collides on a CI runner, bump the fixed subnet (and the
  matching `TRUSTED_PROXY_CIDRS`) — it is a single literal in two files
  (`docker-compose.proxy.yml` + the harness `.env.test` line).
- **R2 (D1):** if the project later adds a first-class "edge / ingress" deploy
  channel with a real proxy in the SHIPPED topology (today the seed ships none
  by design), fold this proxy-IP assertion into that channel's e2e and retire
  the test-only overlay, OR keep both (the overlay stays the focused
  spoofing-rejection proof).
- **R3 (D2):** if nginx:1.27-alpine is ever unpullable on a runner (cf. the
  bitnami/minio lesson in the compose header), swap to caddy:alpine — the
  config translates directly (Caddy's `header_up X-Forwarded-For 203.0.113.10`).
- **R4:** promote `test-self-host-bundle` (including the new `proxy` leg) to a
  required check once it has a few green runs — it ships non-required today
  (slice 065 / 061 convention), so this proxy leg inherits non-required status.

## Confidence

High overall. The load-bearing behaviours (proxy IP honoured / forged IP
rejected) are asserted against a LIVE multi-container bring-up that drives the
exact production `/auth/local/login` → `clientIP(r)` → `sessions.ip_address`
path, with the assertion pinned to the precise session row each login created.
The resolver itself was unchanged (slice 466), so this slice adds no risk to the
shipped resolution logic — it only adds a defense-in-depth e2e net.
