# Decisions log — Slice 088 (CLI `http.Client` explicit timeout)

Slice 088 remediates the **MEDIUM** finding from the Q2 2026 security audit (slice 085) — `cmd/atlas-cli` used `http.DefaultClient.Do(req)` at two call sites with no timeout, leaving the CLI vulnerable to indefinite hangs against an unresponsive atlas server. Fix: new `cmd/atlas-cli/cmdhttp` constructor + per-call-site timeout + AC-4 grep gate.

This is a `JUDGMENT` slice (per CLAUDE.md "Slice types"): the two call-site timeout values (10s vs 30s), the new package's API shape, and the sub-timeout defaults on `http.Transport` are all subjective build-time calls. Recorded below; the slice ships when CI is green.

## Build-time judgment calls

### D1 — package location: `cmd/atlas-cli/cmdhttp/` (not `cmd/atlas-cli/internal/cmdhttp/`) (high confidence)

**Decision:** new package at `cmd/atlas-cli/cmdhttp/client.go`, importable from sibling files in `cmd/atlas-cli/` via the package path `github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp`.

**Alternatives considered:**

- `cmd/atlas-cli/internal/cmdhttp/` — Go's `internal/` rule would still permit imports from `cmd/atlas-cli`, but adds a directory hop and signals "private to this binary" more strongly than needed. The CLI is the only consumer regardless of `internal/` placement; no other binary should be reaching into another binary's helpers.
- Inline helper inside `cmd_features.go` or a new `cmd_http.go` in the same package — fast to write but defeats the slice's refactor goal (single construction point future commands can pull from). The audit explicitly flagged the absence of a single path.
- Top-level `internal/cmdhttp/` (shared with the server) — overreach. The server's outbound HTTP shape is unrelated; sharing would invite the wrong code path to be reused.

**Rationale:** the slice doc lists this exact path as the suggested location, the constructor is small and discoverable, and a future `cmd_<x>.go` adding HTTP calls has the import already established. The package is in the same module tree as its consumers; Go's package boundary is sufficient encapsulation here.

### D2 — call-site timeouts: 10s for feature-flag · 30s for credential reset (high confidence)

**Decision:**

- `cmd_features.go:181` (admin feature-flag list / patch) — **10 seconds**.
- `cmd_credentials.go:148` (admin reset-bootstrap POST) — **30 seconds**.

**Alternatives considered:**

- Uniform 30s everywhere — simpler to remember but defeats the responsiveness goal. A feature-flag read hanging for 30 seconds in a CI script is a noticeable UX regression compared to "fails in 10s, retry the command."
- Uniform 60s — even more conservative; rejected for the same reason. The audit's recommendation was 30s as an upper bound, not a floor.
- Uniform 5s — too aggressive for the credential path, which may legitimately wait on cosign signing inside the server (slice 080 + 062 land cosign in the release flow; the server may invoke similar signing during issuance).
- 15s + 60s — wider spread but no signal that the credential path actually needs > 30s today; the audit specifically suggested 30s.

**Rationale:** the two call sites have meaningfully different work shapes. Feature-flag reads are small SELECTs over a tenant-scoped admin table — sub-second on the server side, with the wire being the only variable. 10s gives ample headroom for one retransmit cycle on a degraded link before erroring out. Credential issuance bounds the cosign / api_keystore HMAC path, which is server-bounded by an issuance-side timeout in the same order; 30s matches the upper bound of what the server itself promises to take. These are recommendations, not invariants — a future engineer adding a third call site uses the same "what's the longest legitimate server-side work?" lens.

### D3 — explicit `*http.Transport` per call (not `http.DefaultTransport` reference) (high confidence)

**Decision:** the constructor returns an `*http.Client` whose `Transport` is a freshly allocated `*http.Transport` with conservative sub-timeouts (10s dial · 10s TLS · 20s response-header).

**Alternatives considered:**

- Use `http.DefaultTransport` (cast to `*http.Transport`) — would share the connection pool across calls and across the entire CLI run. For a short-lived CLI this is fine in practice but pulls a process-global into the construction. Anti-criterion P0-A1 explicitly rejects mutating package-globals; instantiating a fresh transport keeps the spirit of that boundary.
- Bare `&http.Client{Timeout: t}` (no Transport set) — net/http falls back to `DefaultTransport`, same global as above. Same objection.
- Single package-level transport reused across `Client()` calls — the responsiveness goal allows different `Client.Timeout` values without forcing a transport reset, so a shared transport would work; the test (`TestClientReturnsDistinctInstances`) would have to relax. Rejected: the CLI is short-lived, the cost of a per-call transport is negligible, and the test surface is clearer.

**Rationale:** the sub-timeouts are belt-and-suspenders. `Client.Timeout` is the wall-clock budget for the round trip, but it can be slow to fire if a connection phase (DNS, TCP SYN, TLS) is stuck in a way that doesn't cooperate with the deadline. Bounding each phase individually means we get a meaningful error within tens of seconds even in pathological cases. Values are conservative enough that they don't pre-empt a legitimately slow-but-progressing connection on a degraded link.

### D4 — no retry-on-timeout (high confidence)

**Decision:** the constructor returns a client; it does not wrap retry logic. Anti-criterion P0-A2 explicitly rules retries out for this slice.

**Alternatives considered:**

- Add a single retry-on-timeout with constant backoff — simple, but introduces idempotency hazards on the credential POST path. A "reset bootstrap" call that succeeds server-side but fails client-side mid-response is the worst case to retry.
- Add a retry policy specifically for the read path (`featuresDoRequest` GET) — narrower but mixes the constructor's responsibility (timeout) with a call-site concern (retry). Belongs in the call site, not here.

**Rationale:** retry-on-timeout is a separate concern with different correctness implications (idempotency, observability) and a future slice if the user demand materializes. Today's CLI does not retry anywhere; introducing retry only for HTTP calls would be inconsistent.

### D5 — test-strategy: real `httptest.NewServer` + distinct-instances + timeout-field-set (high confidence)

**Decision:** three tests:

- `TestClientTimeoutIsHonored` — `httptest.NewServer` that sleeps 5s; client with 200ms timeout; assert `Do` returns within 2s with a timeout-shaped error.
- `TestClientReturnsDistinctInstances` — two `Client(t)` calls produce distinct `*http.Client` AND distinct `*http.Transport` pointers.
- `TestClientTimeoutFieldIsSet` — table test across {1s, 10s, 30s, 2m} confirming `Client.Timeout` field equals the duration passed.

**Alternatives considered:**

- Mock the transport with a fake `http.RoundTripper` that sleeps — faster (no real socket) but tests the mock more than the production path. The `httptest.NewServer` path exercises the real net/http stack including the TLS bypass for `http://`.
- Single combined test — rejected per the constitutional Splitting Test; each property is independently verifiable.
- Skip `TestClientTimeoutFieldIsSet` — over-redundant with `TestClientTimeoutIsHonored`? No: that test verifies the timeout fires on a real connection; this verifies the field-level contract that any inspector (e.g., a future telemetry wrapper) can read out the configured value.

**Rationale:** the test margin (2s for a 200ms timeout) is intentionally generous to absorb CI scheduler jitter. The `isTimeout` helper covers both `errors.Is(err, os.ErrDeadlineExceeded)` AND `net.Error.Timeout()` AND a string-match fallback because net/http wraps timeout errors in shapes that vary across Go versions; we want the test to be portable across Go 1.26 (current) and any future stdlib refactor that changes the wrap shape.

### D6 — README placement: append to existing `## Security` section (high confidence)

**Decision:** add a single bullet to the existing `## Security` section (created by slice 085 PR #168), not a new top-level section.

**Alternatives considered:**

- New `## CLI hardening` section — overstated; the slice is a small refactor, not a hardening campaign.
- Inline reference inside `## Documentation` — buried; security context is the right surface.

**Rationale:** the `## Security` section is the discovery surface for exactly this kind of pipeline hardening / remediation note. Slice 087 in this same batch also appends to `## Security` — known-safe at reconcile per the batch-31 claim-stake note (append-only, different line content).

### D7 — coverage threshold: 98% for cmd/atlas-cli/cmdhttp (high confidence)

**Decision:** add `"cmd/atlas-cli/cmdhttp": 98` to `cmd/scripts/coverage-thresholds.json` thresholds. Measured 100% line coverage on the new package; floor set to `floor(measured - 2pp) = 98` per slice 069's ratchet rule.

**Alternatives considered:**

- Add to `excludes` list — would skip the gate entirely. Inconsistent: every other small constructor package in the repo carries a floor.
- Set floor at 100 — measured value, but slice 069's methodology explicitly bands by 2pp to absorb measurement noise.
- Set floor at 80 — under-ratchets; the gate exists to lock in tested behavior, and 80 would allow regression to two-thirds coverage without signal.

**Rationale:** the package is small (one exported function, a few struct literal lines), 100%-covered today, and the 2pp band leaves room for the unlikely case where Go 1.27 instruments differently. Tests cover the contract (timeout honored, distinct instances, field set); a regression that drops below 98 would necessarily mean an untested branch was added, which is the gate's intended trigger.

## Revisit once in use

- **Re-evaluate the 10s feature-flag timeout** after the first real-world CI script that hits the endpoint. If 10s causes flakiness on cold-cache reads, raise to 15s. If 10s feels too forgiving when the server is genuinely down, drop to 5s.
- **Re-evaluate the 30s credential timeout** after cosign signing actually lands in the issuance path (or doesn't). If the server-side issuance is consistently < 5s, drop the CLI side to 10s — there's no value carrying a 25-second buffer for "what if cosign decides to take that long."
- **Re-evaluate sub-timeouts** (10s dial / 10s TLS / 20s response-header) if any user reports the outer `Client.Timeout` not firing in a stuck-connection case. The sub-timeouts are belt-and-suspenders; if they never trigger in practice, they're dead code we can simplify.
- **Re-evaluate "no retry"** if a user demand for retry-on-transient-503 materializes. The right shape is a separate slice that adds an opt-in retry wrapper at the call site, not in this constructor.
- **Re-evaluate the AC-4 grep gate** if the package needs to coexist with `http.Client{}` literals (e.g., a non-CLI test helper that imports this package). The current gate is `cmd/atlas-cli/`-scoped; if test helpers under `internal/` start referencing the CLI's HTTP paths, the gate may need to widen or narrow.

## Confidence

| Decision                                                   | Confidence |
| ---------------------------------------------------------- | ---------- |
| D1 — package location at `cmd/atlas-cli/cmdhttp/`          | high       |
| D2 — 10s / 30s per-call-site timeouts                      | high       |
| D3 — explicit `*http.Transport` per call with sub-timeouts | high       |
| D4 — no retry-on-timeout                                   | high       |
| D5 — three-test coverage strategy                          | high       |
| D6 — README placement in `## Security`                     | high       |
| D7 — 98% coverage threshold                                | high       |

All decisions are high-confidence because the slice's surface is small, the audit's recommendation is explicit, and the call-site shapes (one GET-style admin read, one POST-style credential reset) have well-understood server-side work bounds. No `medium` or `low`-confidence items today.

## Acceptance criteria status

- [x] AC-1: New file `cmd/atlas-cli/cmdhttp/client.go` exports `Client(timeout time.Duration) *http.Client` with explicit Transport sub-timeouts.
- [x] AC-2: `cmd/atlas-cli/cmd_features.go:181` uses `cmdhttp.Client(10 * time.Second).Do(req)` with slice-088 inline comment.
- [x] AC-3: `cmd/atlas-cli/cmd_credentials.go:148` uses `cmdhttp.Client(30 * time.Second).Do(req)` with slice-088 inline comment.
- [x] AC-4: `grep -rE 'http\.DefaultClient|http\.Get\(|http\.Post\(' cmd/atlas-cli/` returns zero matches. Verified locally.
- [x] AC-5: `cmd/atlas-cli/cmdhttp/client_test.go` covers timeout-honored, distinct-instances, and timeout-field-set. 100% coverage measured.
- [x] AC-6: This decisions log.
- [x] AC-7: `docs/audits/2026-Q2-security-audit.md` MEDIUM finding now has a "Remediation status" line pointing at this slice.
- [x] AC-8: README.md `## Security` section gained the one-line cmdhttp pointer.
- [x] AC-9: Pre-commit clean. CI gates green at PR open.

## Constitutional invariants honored

- **Working norms — Surgical fixes:** one new constructor, two call-site edits, three tests, one threshold entry. No broader CLI rewrite.
- **AI-assist boundary:** nothing AI-generated. The remediation is straight-line Go.
- **Anti-criteria P0-A1 through P0-A4 honored:** no global mutation, no retry, no server-side change, AC-4 grep enforces the package-wide invariant rather than scoping to "the two call sites the audit found."
