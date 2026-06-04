# 470 — header-overwriting reverse-proxy container in the e2e seed harness

**Cluster:** infra / testing
**Estimate:** S–M (0.5–1d)
**Type:** JUDGMENT

**Status:** `ready`

> Surfaced during slice 466 (`TRUSTED_PROXY_CIDRS` allowlist). Parent: slice
> 466 (grandparent: slice 465). Filed rather than fixed inline because the
> docker-compose self-host seed ships no reverse-proxy container and adding
> one to the e2e bring-up is a test-harness change, not part of 466's
> reading-code surface.

## Narrative

Slice 466 replaced the blunt `TRUST_FORWARDED_HEADERS` boolean with a
`TRUSTED_PROXY_CIDRS` allowlist and a right-to-left `X-Forwarded-For` walk.
AC-6 of slice 466 wanted a true end-to-end test: a header-overwriting proxy
in front of atlas yields the proxy-supplied client IP, while a direct client
forging the header does not.

The slice-466 docker-compose seed (`deploy/docker/docker-compose.yml`) is a
single-VM bundle where atlas binds the public port directly — there is no
nginx/traefik/envoy container, so the seed cannot stand up a real proxy hop.
Slice 466 covered the walk thoroughly at the unit level (forged-prefix
rejected, single/multiple trusted hops, empty allowlist, malformed header,
malformed-CIDR-fails-at-boot, hop cap) and added two wire-level tests over a
real TCP connection via `httptest.Server` with a trusted-loopback CIDR
(`internal/api/auth/clientip_server_test.go`). What remains uncovered is the
**multi-container topology**: a real proxy process overwriting
`X-Forwarded-For` and connecting to atlas from a container-network address
listed in `TRUSTED_PROXY_CIDRS`.

## Scope

- **In scope:** add a minimal header-overwriting reverse-proxy service
  (nginx or caddy) to an e2e/integration compose overlay that sits in front
  of atlas; set `TRUSTED_PROXY_CIDRS` to the proxy's container-network CIDR;
  assert (a) a request through the proxy records the proxy-supplied client IP
  on the session row, and (b) a direct-to-atlas request forging
  `X-Forwarded-For` records the direct peer, not the forged value. Wire it
  into the integration or Playwright tier per `web/e2e/README.md` /
  `CONTRIBUTING.md` "Integration-test enrolment".
- **Out of scope:** changing the production single-VM seed (no proxy there by
  design); per-tenant proxy config.

## Acceptance criteria

- [ ] **AC-1.** An e2e/integration overlay fronts atlas with a real
      header-overwriting proxy; the proxy's source CIDR is in
      `TRUSTED_PROXY_CIDRS`.
- [ ] **AC-2.** Request through the proxy → session row records the
      proxy-supplied client IP.
- [ ] **AC-3.** Direct request forging `X-Forwarded-For` → session row records
      the direct peer, not the forged value.
- [ ] **AC-4.** Enrolled in the relevant test tier and green in CI.

## Notes

- Parent decision: `docs/audit-log/466-trusted-proxy-cidrs-decisions.md` (D4).
- Resolver under test: `internal/api/auth/clientip.go` (slice 466).
