# 088 — CLI `http.Client` explicit timeout

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK

## Narrative

Surfaced by the 2026-Q2 security audit (slice 085). **MEDIUM severity finding.**

Two `cmd/atlas-cli` call sites use `http.DefaultClient.Do(req)`:

- `cmd/atlas-cli/cmd_features.go:181`
- `cmd/atlas-cli/cmd_credentials.go:148`

Go's `http.DefaultClient` has NO timeout by default. If the atlas server is unresponsive (DNS hang, deep TCP retransmits, server pause-the-world), the CLI hangs indefinitely. The atlas-cli is the operator's primary administrative entrypoint — a hung CLI in an automated CI script or a maintenance window is a real availability concern.

This is a DoS-against-the-operator, not against the platform itself. The atlas server's own HTTP server already has timeouts; only the CLI's outbound requests are unbounded.

**Fix:** replace both call sites with a per-call `*http.Client` carrying an explicit timeout. Suggested timeout: 30 seconds for the credential issuance (which may involve cosign signing) and 10 seconds for the feature-flag toggle (a small read). Engineer's grill picks specific values per call site + records in decisions log.

**Refactor option:** extract a `cmdhttp.Client(timeout)` constructor in `cmd/atlas-cli/cmdhttp/` (new file) so future CLI commands have a single path. Avoids the pattern of "engineer copies http.Client construction every new subcommand."

## Acceptance criteria

- [ ] AC-1: New file `cmd/atlas-cli/cmdhttp/client.go` (or `cmd/atlas-cli/internal/cmdhttp/client.go` if the engineer prefers — engineer's call) exports `Client(timeout time.Duration) *http.Client`. The constructor returns an `http.Client` with `Timeout` set + sensible transport defaults (cookie jar disabled, redirect handling unchanged from net/http default).
- [ ] AC-2: `cmd/atlas-cli/cmd_features.go:181` replaces `http.DefaultClient.Do(req)` with `cmdhttp.Client(10 * time.Second).Do(req)` (or equivalent). Inline comment cites slice 088.
- [ ] AC-3: `cmd/atlas-cli/cmd_credentials.go:148` replaces `http.DefaultClient.Do(req)` with `cmdhttp.Client(30 * time.Second).Do(req)`. Longer timeout because credential issuance may include cosign signing.
- [ ] AC-4: A `grep -rE 'http\.DefaultClient|http\.Get\(|http\.Post\(' cmd/atlas-cli/` returns ZERO matches post-fix — the package no longer uses the default client anywhere.
- [ ] AC-5: Unit tests at `cmd/atlas-cli/cmdhttp/client_test.go` cover: (a) Timeout is honored (point at `httptest.NewServer` that sleeps longer than the timeout; assert `client.Do(req)` returns a deadline-exceeded error within the configured window); (b) the constructor returns distinct `Client` instances per call (no shared state surprise).
- [ ] AC-6: `docs/audit-log/088-cli-http-client-timeout-decisions.md` records the timeout values per call site + the rationale.
- [ ] AC-7: `docs/audits/2026-Q2-security-audit.md` Remediation-status line under the MEDIUM finding points at this slice's merge commit.
- [ ] AC-8: README.md "Security" section gets a one-line "atlas-cli HTTP calls timeout via `cmd/atlas-cli/cmdhttp`. Default 30s. See `cmdhttp/client.go`."
- [ ] AC-9: Pre-commit clean. CI green.

## Constitutional invariants honored

- **Working norms — Surgical fixes**: smallest viable refactor. One constructor + two call-site changes. No broader CLI rewrite.
- **AI-assist boundary**: nothing AI-generated.

## Canvas references

- _(none — CLI hygiene; canvas doesn't speak to HTTP client construction)_

## Dependencies

- **039** (CLI binary distribution + release pipeline, merged)
- **034** (OIDC RP + local users + api_keys admin, merged — credential issuance flow this hardens)

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT add a global timeout default (e.g., reassigning `http.DefaultClient.Timeout`). Mutating package-globals is a footgun for any test or library that relies on default-client behavior.
- **P0-A2**: Does NOT add retry logic in this slice. Retry-on-timeout is a separate concern with different correctness implications (idempotency, observability). Filed as a future slice if needed.
- **P0-A3**: Does NOT change the atlas-server-side request handling. The fix is CLI-only; the server's existing handler timeouts are unchanged.
- **P0-A4**: Does NOT scope the change to "the two call sites the audit found." If `grep` for `http.DefaultClient` in `cmd/atlas-cli/` finds OTHER call sites the audit missed, those get the same treatment (AC-4 is the explicit verification).

## Skill mix (3–5)

- Go `net/http` idioms (correct Client construction, Transport hygiene)
- Unit testing with `httptest.NewServer` + deadline-exceeded assertions
- `simplify` (the constructor is one function)

## Notes for the implementing agent

- The two call sites' timeout values (10s + 30s) are recommendations, not mandates. The engineer's grill can pick different values; the rationale goes in the decisions log.
- This slice is a small refactor + small test. Should land cleanly in ~15-30 min of focused work.
- After this lands, file a follow-on slice IF retry-on-timeout becomes a real ask. Today it isn't — the CLI's idempotency story isn't established enough to safely retry.
