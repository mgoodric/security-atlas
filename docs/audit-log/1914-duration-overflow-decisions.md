# Decisions log — #1914: range-check request input before time.Duration conversion

- detection_tier_actual: manual_review
- detection_tier_target: unit

Ticket: security-atlas#1914, found while fixing #1892 (#1893). There,
`ttl_seconds` large enough to wrap `time.Duration` slipped past a cap check.
The same unchecked `time.Duration(<input>) * unit` shape existed in other
handlers. Each site below now range-checks the integer before multiplying.

## D1 — Admin credential cap is the overflow bound, not a policy cap

`POST /v1/admin/credentials` documents no maximum for `ttl_seconds`, and
`0` already means "no expiry", so any finite cap below that would be a
policy change for existing callers with nothing to anchor it. The cap is
`math.MaxInt64 / int64(time.Second)` = 9223372036 seconds (about 292
years): the largest value that converts without wrapping. Negative or
larger is a 400, written before the store is called. A tighter policy cap
(as `reset-bootstrap` has at 7d) is a separate decision.

## D2 — Clamp where the endpoint already clamps, refuse where it refuses

The fix keeps each endpoint's existing contract for oversized input and only
removes the wrap:

| Site                                        | Input                | Existing contract for too-large  | Fix                                                                                           |
| ------------------------------------------- | -------------------- | -------------------------------- | --------------------------------------------------------------------------------------------- |
| `admincreds.Issue`                          | `ttl_seconds` (body) | none (wrapped)                   | 400 outside `[0, 9223372036]` (D1)                                                            |
| `artifacts` GET `?ttl=`                     | seconds              | clamp to `MaxDownloadTTL` (1h)   | clamp seconds to 3600 before converting                                                       |
| `freshnessdrift.parseSinceDays` `?since=Nd` | int32 days           | clamp to `maxDriftWindow` (400d) | clamp days to 400 before converting                                                           |
| `exceptions.parseWithin` `?within=`         | `Nd`/`Nh`/`Nm`       | 400 "exceeds maximum (365d)"     | parse error (400) only when `num * unit` would overflow, so `400d` still gets the cap message |

Before the fix: `?ttl=4611686018427387904` wrapped to 0 and got the 15-minute
default; `?since=2147483647d` wrapped to about 166 years and passed the
400d clamp; `?within=4611686018427387904d` wrapped to 0 and passed as a
valid window.

## D3 — Sites left alone

The other `time.Duration(` conversions in non-test code are not request or
CLI input:

- `cmd/atlas-cli/cmd_login.go` (`ExpiresIn`, `Interval`) and
  `pkg/sdk-go/oauth/oauth.go` (`ExpiresIn`) read the configured issuer's
  token response.
- `internal/auth/apikeystore/apikeystore.go` reads `ttl_seconds` back from
  the database, written from an already-converted Duration.
- `internal/api/oauth/device_code_grant.go` converts a constant.
- `internal/api/evidence/http.go` converts a float computed from the rate
  limiter's own state.
- `internal/api/install_reset.go` (`handleResetBootstrap`, `ttl_seconds`)
  is request input, but #1893 already range-checks it to `[1, 604800]`
  before the conversion, which is the shape this ticket applies elsewhere.
- The CLI `--ttl` flag goes through `time.ParseDuration`, which refuses
  overflow, and the admin gRPC `Issue` RPC uses `durationpb.AsDuration`,
  which saturates instead of wrapping. It did accept a negative TTL;
  #1906 refuses that with `InvalidArgument`.
