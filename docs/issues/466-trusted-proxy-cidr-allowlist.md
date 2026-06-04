# 466 — `TRUSTED_PROXY_CIDRS` allowlist as the structural fix for X-Forwarded-For trust

**Cluster:** infra / security
**Estimate:** M (1–2d)
**Type:** JUDGMENT

**Status:** `ready`

> Surfaced during slice 465 (`TRUST_FORWARDED_HEADERS` plumb-and-template).
> Recorded in `docs/audit-log/465-trust-forwarded-headers-decisions.md` as the
> correct long-term design rather than filed inline (465 was an S plumb-only
> slice and the structural fix touches reading code that 465 was scoped out
> of). Orchestrator-filed at batch-193 reconcile to keep it tracked.
> Parent: slice 465 (grandparent: slice 460).

## Narrative

Slice 465 plumbed the existing boolean `TRUST_FORWARDED_HEADERS` env var
through the self-host bundle (default OFF). The boolean is a blunt instrument:
when an operator sets `TRUST_FORWARDED_HEADERS=1`, the server
(`internal/api/auth/clientip.go`) trusts the **first** IP in `X-Forwarded-For`
unconditionally. If the deployment is NOT actually behind a header-stripping
trusted proxy, any client can forge `X-Forwarded-For` and spoof its source IP —
poisoning the slice-162 client-IP capture that feeds the audit log and any
IP-based rate limiting.

The structurally-correct design is a **CIDR allowlist**: `TRUSTED_PROXY_CIDRS`
(comma-separated CIDRs). The server walks `X-Forwarded-For` right-to-left,
accepting a hop only if the immediately-connecting peer is within an allowed
CIDR, and stops at the first untrusted hop — so a client-forged prefix is
ignored. This is the standard reverse-proxy-aware client-IP resolution pattern
(mirrors what mature frameworks do). With it, "operator enabled the flag with
no proxy in front" stops being a spoofing vector because an empty/mismatched
allowlist yields the direct peer IP, not the forged header.

This touches the reading code (`internal/api/auth/clientip.go` +
`internal/api/admindemo/handler.go` mirror) that slice 465 was explicitly
scoped out of, which is why it is its own slice.

## Why this matters

- **Security:** the boolean is foot-gun-shaped; the allowlist is fail-safe.
  Client-IP integrity underpins audit-log attribution (canvas §8 / threat-model
  R) and any rate limiting.
- **Diligence:** "how do you prevent source-IP spoofing behind a proxy" is a
  standard security-questionnaire item; the allowlist is the defensible answer.

## Scope

- **In scope:** add `TRUSTED_PROXY_CIDRS` parsing + a right-to-left
  `X-Forwarded-For` walk in `internal/api/auth/clientip.go`; keep
  `TRUST_FORWARDED_HEADERS` as a back-compat alias (or migrate it with a
  documented deprecation); plumb `TRUSTED_PROXY_CIDRS` through
  `.env.example` + compose + the slice-430 config reference; unit-test the
  walk (forged-prefix rejected, trusted-hop accepted, empty-allowlist =
  direct-peer); an e2e that fronts atlas with a header-overwriting proxy.
- **Out of scope:** Helm-chart parity beyond the env var (note as follow-on);
  per-tenant proxy config; changing the audit-log schema.

## Acceptance criteria

- [ ] **AC-1.** `TRUSTED_PROXY_CIDRS` parsed into a validated CIDR set at
      startup; malformed entries fail loud at boot, not silently per-request.
- [ ] **AC-2.** Client-IP resolution walks `X-Forwarded-For` right-to-left and
      returns the first IP whose connecting peer is NOT in an allowed CIDR
      (i.e. the real client), ignoring any client-forged prefix.
- [ ] **AC-3.** Empty/unset `TRUSTED_PROXY_CIDRS` ⇒ direct peer IP
      (`RemoteAddr`), identical to today's `TRUST_FORWARDED_HEADERS` unset.
- [ ] **AC-4.** `TRUST_FORWARDED_HEADERS=1` back-compat: documented mapping
      (e.g. treated as "trust any proxy" with a loud deprecation warning) OR a
      clean migration with operator-facing release note. Decision recorded.
- [ ] **AC-5.** Unit tests: forged-prefix rejected · single trusted hop ·
      multiple trusted hops · empty allowlist · malformed header.
- [ ] **AC-6.** e2e: a header-overwriting proxy in front of atlas yields the
      proxy-supplied client IP; a direct client forging the header does not.
- [ ] **AC-7.** `.env.example` + compose + config-reference updated; slice-430
      config-drift guard green; security note on correct usage.

## Threat model (STRIDE)

- **S — Spoofing.** The whole point: prevents client source-IP spoofing via
  forged `X-Forwarded-For`. The allowlist is the mitigation.
- **T — Tampering.** Malformed CIDR config failing loud at boot (AC-1) prevents
  a silently-misconfigured deployment from trusting all headers.
- **R — Repudiation.** Correct client-IP capture preserves audit-log
  attribution integrity (the downstream consumer of this value).
- **I — Information disclosure.** N/A.
- **D — Denial of service.** A per-request CIDR walk is O(hops × cidrs); cap
  the XFF hop count parsed to avoid a pathological-header CPU sink.
- **E — Elevation of privilege.** N/A directly; but IP-based allow rules (if
  any are ever added) would depend on this being correct.

## Notes

- Surfaced in `docs/audit-log/465-trust-forwarded-headers-decisions.md` (D2/D3).
- Reading code: `internal/api/auth/clientip.go` (+ the `admindemo/handler.go`
  mirror). No unmerged dependency → `ready`. Supersedes the blunt
  `TRUST_FORWARDED_HEADERS` boolean from slice 465 (which stays as the
  back-compat surface).
