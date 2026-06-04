# 464 — `TRUST_FORWARDED_HEADERS` is server-read but not plumbed through the self-host bundle

**Cluster:** infra
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`

## Surfaced during slice 460

Slice 460 audited every env var the `atlas` server reads against the docker
`.env.example` template, using the discriminator "does `docker-compose.yml`
plumb the key to the `atlas` service?" as the inclusion boundary for the
template. One **security-relevant** key fell on the wrong side of that line and
was deliberately deferred rather than half-wired:

- `internal/api/auth/clientip.go:34,54` — `const trustForwardedHeadersEnv = "TRUST_FORWARDED_HEADERS"`; when set to `"1"`, the server parses the left-most `X-Forwarded-For` IP for session-row client-IP capture (RFC 7239 convention), instead of `r.RemoteAddr`.
- `internal/api/admindemo/handler.go:433` — same constant, same `"1"` gate, on the demo-seed request path.

The server reads it, but `deploy/docker/docker-compose.yml` does **not** pass it
to the `atlas` service, and `deploy/docker/.env.example` does not template it.

## Why this matters

The moment a self-host operator fronts the bundle with a reverse proxy or load
balancer — the common production topology — `r.RemoteAddr` becomes the proxy's
address, not the real client's. Session rows then record the proxy IP for every
sign-in, degrading the audit/forensic value of the session-IP column. The
operator has no bundle-supported way to opt into correct client-IP capture
today.

This was NOT fixed in slice 460 because wiring it correctly is a real change
with its own threat model (trusting `X-Forwarded-For` from an untrusted network
is a spoofing vector), not a config-template typo — out of scope for slice 460's
S-sized config-hygiene mandate.

## Acceptance criteria

- [ ] **AC-1.** `TRUST_FORWARDED_HEADERS` is plumbed to the `atlas` service in `docker-compose.yml` as `${TRUST_FORWARDED_HEADERS:-}` (default off — preserves today's behavior).
- [ ] **AC-2.** `TRUST_FORWARDED_HEADERS` is added to `.env.example` as a commented opt-in with a clear caveat: only enable it when the deployment sits behind a trusted reverse proxy that sets/overwrites `X-Forwarded-For`, because trusting the header from an untrusted source lets clients spoof their recorded IP.
- [ ] **AC-3.** The slice-430 config-reference page (`docs-site/docs/configuration.md`) gains the row and `just config-reference-drift-check` passes.
- [ ] **AC-4.** A decision is recorded (JUDGMENT) on the trust-boundary phrasing and whether a single boolean is sufficient vs. a trusted-proxy CIDR allowlist (the more defensive shape; likely a v2 follow-on).

## Notes

- Default-off keeps the change zero-behavior for existing deployments.
- Consider whether the audit-sink trio (`ATLAS_AUDIT_SINK_PATH` / `_HMAC_KEY` /
  `_BUFFER_SIZE`) and the OSCAL export-signing set warrant the same
  plumb-and-template treatment; slice 460's decisions log lists them as the same
  class of "server-read, bundle-unplumbed, operator-facing" key. They are lower
  urgency than `TRUST_FORWARDED_HEADERS` (no silent forensic degradation), so
  they can ride a later slice or this one's follow-on.

Parent: slice 460. Audit detail: `docs/audit-log/460-nats-url-env-example-decisions.md`.
