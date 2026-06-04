# Slice 466 — decisions log (`TRUSTED_PROXY_CIDRS` allowlist)

JUDGMENT slice. The subjective build-time calls are recorded here; no human
sign-off gate. The maintainer iterates post-deployment.

## Summary

Replaced the slice-465 `TRUST_FORWARDED_HEADERS` boolean (which trusted the
left-most `X-Forwarded-For` IP unconditionally) with a `TRUSTED_PROXY_CIDRS`
comma-separated allowlist + a right-to-left XFF walk. The walk accepts a hop
only while the immediately-connecting peer is inside an allowed CIDR and stops
at the first untrusted hop (the real client), so a client-forged XFF prefix is
ignored. Empty/unset ⇒ direct peer IP (byte-identical to today).

## Decisions

### D1 — back-compat mapping for `TRUST_FORWARDED_HEADERS` (AC-4)

**Decision:** keep `TRUST_FORWARDED_HEADERS` as a **documented deprecated
alias**. When it is `1` AND `TRUSTED_PROXY_CIDRS` is unset, it maps to
"trust any proxy" (`0.0.0.0/0` + `::/0`) and the server logs a one-time
deprecation warning at boot. `TRUSTED_PROXY_CIDRS` always takes precedence
when both are set (then the alias is ignored, no warning).

**Why:** existing slice-465 deployments that set the boolean keep working with
no behavior change (the left-most-IP-trusted posture is reproduced by
trust-any-proxy, since every peer is then "trusted" and the walk returns the
left-most hop). The deprecation warning steers operators to the allowlist
without breaking them. A hard migration (remove the boolean) was rejected:
slice 465 shipped it as the documented surface; silently dropping it would
strand operators who templated it.

**Alternatives considered:** (a) remove the boolean entirely — rejected,
breaks 465 deployments; (b) map `=1` to loopback-only — rejected, changes the
documented 465 semantics (it trusted the header, not "only local").

### D2 — XFF hop-count cap for the DoS mitigation (threat-model D)

**Decision:** cap parsed XFF entries at **50** (`maxForwardedHops`). Beyond 50,
remaining entries are not parsed.

**Why:** the per-request walk is O(hops × cidrs). A pathological multi-kilobyte
`X-Forwarded-For` header could otherwise turn each request into a CPU sink. No
real topology chains 50 proxies; the cap is far beyond legitimate use while
bounding the worst case. Empty/whitespace entries are dropped (a stray comma
does not abort the walk early or consume a hop slot).

**Confidence:** high. 50 is generous; can be lowered later without a contract
change.

### D3 — keep BOTH env vars (do not migrate away)

**Decision:** `TRUSTED_PROXY_CIDRS` is canonical; `TRUST_FORWARDED_HEADERS`
remains as the deprecated alias (per D1). Both are documented in
`.env.example`, `docker-compose.yml`, and the slice-430 config reference.

**Why:** smallest-surprise path. Operators on 465 are not forced to act
immediately; new operators are guided to the allowlist by the docs ordering
and the deprecation note.

### D4 — AC-6 e2e: no proxy container in the seed → wire-level tests + spillover

**Decision:** the docker-compose self-host seed ships no reverse-proxy
container (single-VM bundle, atlas binds the public port directly), so a
full multi-container header-overwriting-proxy e2e cannot be wired from the
seed today. Instead: (a) thorough unit coverage of the walk, (b) two
wire-level tests over a **real TCP connection** via `httptest.Server` with a
trusted-loopback CIDR (`clientip_server_test.go`) — one proving a forwarded
header from a trusted loopback "proxy" is honored, one proving a forged header
from an untrusted peer is rejected, and (c) filed **slice 470** for the
real-proxy-container e2e in the test harness. The assertion was NOT relaxed
silently.

**Why:** adding a proxy container to the e2e bring-up is a test-harness change
orthogonal to 466's reading-code surface; the wire-level tests already exercise
the exact resolution path with a genuine remote address.

### D5 — single implementation shared by both surfaces

**Decision:** the `admindemo` rate-limiter `clientIP` now delegates to the
auth-package resolver via the new exported `auth.ClientIP(r)`; the duplicated
env-reading logic in `internal/api/admindemo/handler.go` was deleted.

**Why:** slice 465 left two copies of the XFF posture (auth + admindemo). One
implementation removes the drift risk that the two surfaces diverge on a
security-relevant decision. No import cycle (auth does not import admindemo).

### D6 — boot-time parse + process-wide validated set (AC-1)

**Decision:** `InitTrustedProxiesFromEnv()` parses + validates the CIDRs once
at startup (called from `cmd/atlas/main.go`), `os.Exit(1)` on a malformed
entry. The per-request path reads the cached `*net.IPNet` set under an RWMutex.

**Why:** AC-1 requires fail-loud-at-boot, not silent-per-request. Parsing once
also avoids re-parsing the env on every request. The malformed-CIDR test
(`TestInitTrustedProxies_MalformedCIDRFailsLoud`) covers the boot guard.

## Revisit

- **R1 (D2):** revisit `maxForwardedHops` if a legitimate topology ever needs
  more than 50 hops (none known).
- **R2 (D1):** remove `TRUST_FORWARDED_HEADERS` entirely in a future major once
  deployments have migrated (deprecation warning is the on-ramp).
- **R3 (D4):** slice 470 lands the real-proxy-container e2e.
- **R4:** Helm-chart parity for `TRUSTED_PROXY_CIDRS` is out of scope here
  (slice 466 scope note); file/track when the Helm path adds the var.

## Confidence

High on the walk correctness (forged-prefix rejection is the load-bearing
behavior and is covered by both `httptest.NewRequest` unit tests and real-TCP
wire tests). Medium on the back-compat mapping being the operator-preferred
shape (D1) — the deprecation warning makes the choice reversible.

## Detection-tier classification

- `detection_tier_actual`: `none` — no bug surfaced during the slice; the walk
  logic was verified correct against the forged-prefix cases on first
  implementation (the right-to-left invariant was reasoned through before
  coding and the unit suite confirmed it).
- `detection_tier_target`: `unit` — the security-load-bearing behavior
  (forged-prefix rejection, hop accounting, boot-time CIDR validation) is the
  kind of defect that MUST be caught at the unit tier; the e2e/integration
  tier (slice 470) is defense-in-depth for the multi-container wiring, not the
  primary net for the resolution logic.
