# Slice 465 — decisions log

`TRUST_FORWARDED_HEADERS` plumbed through the self-host bundle — JUDGMENT slice.
Parent: slice 460 (its decisions log named this the strongest deferred
candidate; see `docs/audit-log/460-nats-url-env-example-decisions.md` "Revisit
once in use").

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is a plumb-and-template slice: the
server already reads the variable correctly; the bundle's behavior is
byte-for-byte unchanged when the variable is unset, which `docker compose
config` confirms.)

## Context

The server reads `TRUST_FORWARDED_HEADERS` in two places — both shipped, both
out of scope to edit:

- `internal/api/auth/clientip.go:34,54` — when set to `"1"`, the session-create
  client-IP helper parses the leftmost `X-Forwarded-For` entry (RFC 7239)
  instead of `r.RemoteAddr`.
- `internal/api/admindemo/handler.go:433,441` — same constant, same `"1"` gate,
  mirrored on the demo-seed request path.

`deploy/docker/docker-compose.yml` did not pass the key to the `atlas` service,
and `deploy/docker/.env.example` did not template it. An operator fronting the
bundle with a trusted reverse proxy (the common production topology) therefore
had no bundle-supported way to opt into correct client-IP capture; sessions
recorded the proxy IP, silently degrading the audit/forensic value of the
session-IP column.

## Threat model (why default-OFF is load-bearing)

`X-Forwarded-For` is a plain request header. Any HTTP client can set it. The
trust model is asymmetric:

| Topology                                           | `TRUST_FORWARDED_HEADERS` | Recorded client IP                          | Safe? |
| -------------------------------------------------- | ------------------------- | ------------------------------------------- | ----- |
| Direct bind (no proxy)                             | unset / off (default)     | TCP peer (`r.RemoteAddr`) — correct         | yes   |
| Direct bind (no proxy)                             | `1`                       | **client-supplied header — forgeable**      | NO    |
| Behind proxy that **overwrites** XFF               | `1`                       | real client IP (proxy-stamped)              | yes   |
| Behind proxy that **appends** / passes XFF through | `1`                       | **leftmost is client-supplied — forgeable** | NO    |

The dangerous transition is enabling the flag without a proxy that _strips and
re-issues_ the header. The downstream blast radius: forged IPs land on every
sign-in session row, and any IP-based rate-limit or audit/forensic decision then
trusts attacker-controlled input (a CWE-290 / spoofing class issue). Hence the
default MUST be off, and the template MUST ship it commented out with the
warning prominent — enabling it is a deliberate, proxy-conditional operator
gesture, never a default-on convenience.

## Decisions made

1. **Default value = empty (`${TRUST_FORWARDED_HEADERS:-}`), not the literal
   `0` or `false`.** The reading code treats _any value other than `"1"`_ as
   off, so an empty value is the "off" path. Empty also makes the
   unchanged-when-unset proof clean: `docker compose config` renders the same
   bytes whether the var is unset or explicitly empty. Mirrors the
   `ATLAS_TEST_MODE` / OTEL opt-in pattern already in the file. Confidence:
   **high**.

2. **Single boolean now; trusted-proxy CIDR allowlist deferred to v2 (AC-4).**
   A more defensive shape would be a `TRUSTED_PROXY_CIDRS` allowlist — trust XFF
   only when `r.RemoteAddr` is itself inside a configured trusted-proxy range,
   and walk the XFF chain right-to-left skipping trusted hops (the standard
   "rightmost-untrusted" algorithm). That is the correct long-term design, but
   it is a **reading-code change** (new env var, new parse logic, new tests) —
   out of scope for this plumb-and-template slice, and it would touch
   `clientip.go`, which the directive forbids here. The existing single-boolean
   posture is honest as long as the operator understands the precondition, which
   the warning makes explicit. Filed as a v2 follow-on candidate (see Revisit).
   Confidence: **medium** (defensible scope cut; the boolean is a real
   foot-gun-if-misused, which the docs mitigate but do not eliminate).

3. **Warning phrasing — name the vector explicitly ("client-IP spoofing
   vector"), name the precondition ("a trusted reverse proxy that OVERWRITES
   the inbound header"), and name the blast radius (rate-limit / audit
   decisions trust forged input).** Vague "use with care" wording is what lets
   operators enable security-relevant toggles blindly. Same tone as the
   `ATLAS_TEST_MODE` / `ATLAS_METRICS_FALLBACK_ENABLE` danger notes already in
   the template and config page. Confidence: **high**.

4. **Did NOT add a note to `docs/SELF_HOSTING.md`.** SELF_HOSTING.md has no
   reverse-proxy / TLS section today (only a `Secure`-cookie mention lives in
   the config reference, not there), so adding the toggle would require
   introducing a new heading — scope creep for an S-sized slice. The
   slice-430 config-reference page (`docs-site/docs/configuration.md`) is the
   canonical, drift-guarded operator surface for env vars and now carries the
   full warning in two places (the security-posture danger admonition + the
   table row). The in-code operator note in `clientip.go` already anticipated
   "a future operator-docs page" for the proxy topology; that broader
   reverse-proxy/TLS deployment guide is its own slice. Confidence: **high**.

5. **Placed the compose env line in the cookies/security cluster (next to
   `ATLAS_SECURE_COOKIES`)** and the config-page row in the "Cookies and
   security" table + a posture-toggle bullet in the security-critical
   admonition — co-locating it with the other "this changes the deployment's
   security posture" knobs rather than burying it among ports. Confidence:
   **high**.

6. **Edited `docs-site/docs/configuration.md` body only** (the danger bullet +
   one table row) to keep the slice-430 drift guard green. Did NOT touch
   `docs-site/mkdocs.yml` nav. `just config-reference-drift-check` is green:
   36 variables (26 active + 10 opt-in) match the page. Confidence: **high**.

## Revisit once in use

- **Trusted-proxy CIDR allowlist (`TRUSTED_PROXY_CIDRS`)** — the defensive
  shape deferred in D2. File a v2 follow-on slice that (a) adds the env var,
  (b) changes `clientip.go` to walk the XFF chain right-to-left trusting only
  hops inside the configured CIDRs, and (c) deprecates the bare boolean (or
  keeps it as the "trust the single hop" shorthand). This removes the
  "operator enables it with no proxy" foot-gun structurally rather than via
  documentation.
- **Validate the proxy path end-to-end** — the bundle e2e exercises only the
  default (unset → off) path. Once a real reverse-proxy deployment exists,
  add an e2e that fronts atlas with a header-overwriting proxy and asserts the
  session row records the forwarded client IP, not the proxy IP.
- **Helm chart parity** — when the K8s/Helm deploy path templates env, mirror
  this toggle there with the same default-off posture and Ingress-scrub caveat.

## Confidence summary

| Decision                                            | Confidence |
| --------------------------------------------------- | ---------- |
| Empty default (`${VAR:-}`), not `0`/`false` literal | high       |
| Single boolean now; CIDR allowlist deferred to v2   | medium     |
| Warning names vector + precondition + blast radius  | high       |
| Leave `SELF_HOSTING.md` untouched                   | high       |
| Co-locate with cookies/security cluster             | high       |
| Edit `configuration.md` body only (not nav)         | high       |
