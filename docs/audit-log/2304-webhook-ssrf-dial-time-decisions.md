# #2304 — Webhook SSRF: dial-time IP check and refused redirects: decisions log

**Issue:** [#2304](https://github.com/mgoodric/security-atlas/issues/2304), a
security follow-up from the review of #2303 (#2202). Both gaps pre-date #2303.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## What changed

`notify.SSRFPolicy` gains `NewHTTPClient(timeout)`, the one hardened outbound
client for operator-configured notification targets, and `DialControl`, the
`net.Dialer.Control` hook it installs. The webhook transport (`newClient`) and
the Slack transport (`NewHTTPTransport`, which now takes the policy) both use
it. `ValidateWebhookURL` still runs at construction; the client re-applies the
same deny list at every dial.

## D1 — Dial-time check in `net.Dialer.Control`, on the address dialled

Control runs after name resolution and before `connect`, once for every
address the dialer tries (each Happy-Eyeballs attempt included), with the
literal `ip:port` the kernel will connect to. Checking there, with the same
`denyReason` as config time, closes DNS rebinding: the name may resolve to
anything later, but nothing internal is connected to. It covers IPv4, IPv6,
IPv4-mapped IPv6 (`::ffff:127.0.0.1` is reported as loopback by `net.IP`),
`0.0.0.0` / `::`, link-local incl. 169.254.169.254, RFC1918, ULA and CGNAT.
An address that does not parse as an IP is refused (fail closed).

A wrapping `DialContext` that resolves and then dials a chosen IP (the
adminsso preflight's shape) was the alternative. Control is simpler and checks
every address tried, not only the first.

## D2 — No environment proxy

`http.DefaultTransport` uses `ProxyFromEnvironment`. With `HTTPS_PROXY` set,
the client would dial the proxy, Control would approve the proxy's address,
and the proxy would connect to the internal target. The hardened transport sets
`Proxy: nil`. An operator who needs egress through a proxy has no path for
webhook/Slack delivery today; that is deliberate until someone asks, and would
need the proxy itself to enforce the deny list.

## D3 — Redirects are not followed

`CheckRedirect` returns `http.ErrUseLastResponse`, so a 3xx comes back as the
response and the POST body, the `Authorization` bearer and the
`X-Atlas-Signature` HMAC are never sent anywhere but the configured URL. Post
treats any 3xx as a delivery failure: `webhook: post status 307: redirect not
followed; configure the final URL`. The delivery is recorded `failed` and
retried on the next tick like any other failure.

Re-validating each hop was the alternative. Not following is simpler, cannot
be got wrong per hop, and matches webhook norms (GitHub, Stripe and Slack do
not follow redirects on delivery). **For receivers:** configure the final URL;
an endpoint that redirects (http to https, a trailing-slash rewrite, a moved
host) fails every delivery.

## D4 — Backstop `http.Client.Timeout`

`Config.Timeout` drove only a per-request context deadline. A directly built
`Config{Timeout: 0}` had none. The client now always carries a whole-request
timeout: `cfg.Timeout`, or `DefaultTimeout` (10s) when that is zero.

## D5 — Slack gets the same client, at dial time only

The Slack incoming-webhook URL is operator-configured exactly like the generic
webhook, but had no SSRF check at all, shared `http.DefaultTransport`, and
followed redirects. It now uses `NewHTTPClient` with the strict policy, so an
internal target is refused at dial. No config-time `ValidateWebhookURL` call
was added: the URL is a Secret and the dial-time check is the stronger leg;
startup-time rejection for Slack can follow if wanted.

## D6 — Other outbound HTTP clients

| Client                                                       | Target set by                | Status                                                                                                                                                                                                                                                                                              |
| ------------------------------------------------------------ | ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/notify/webhook`                                    | operator env                 | Fixed here                                                                                                                                                                                                                                                                                          |
| `internal/notify/slack`                                      | operator env                 | Fixed here (D5)                                                                                                                                                                                                                                                                                     |
| `internal/api/adminsso` preflight                            | tenant admin                 | Already has a resolve-then-dial check and refuses redirects (no change)                                                                                                                                                                                                                             |
| `internal/auth/oidc` runtime discovery, JWKS, token exchange | tenant admin (issuer URL)    | **Gap.** Uses go-oidc/oauth2 defaults (`http.DefaultClient`): no dial-time check, redirects followed, env proxy honoured. Filed as [#2534](https://github.com/mgoodric/security-atlas/issues/2534); it needs the `AllowPrivateIPs` opt-in carried through for on-prem IdPs, so it is its own change |
| `internal/llm/ollama.go`, `internal/llm/cloud`               | operator env                 | Not in scope: Ollama is on loopback/LAN by design; cloud endpoints are fixed vendor hosts                                                                                                                                                                                                           |
| `internal/mcp/client.go`                                     | operator (CLI/MCP config)    | Not in scope: points at the operator's own atlas API, often on loopback                                                                                                                                                                                                                             |
| `connectors/*`, `pkg/sdk-go`, `cmd/atlas-cli`                | operator, separate processes | Not in scope: each runs under the operator's own credentials against the vendor API or a self-hosted instance (often on a LAN); there is no cross-trust-boundary caller to defend against                                                                                                           |

## Tests

- `internal/notify`: `TestSSRFPolicy_DialControl` (deny table incl. IPv4-mapped
  IPv6, `0.0.0.0`, `::`, metadata IP, a hostname instead of an IP);
  `TestSSRFPolicy_NewHTTPClient_Hardened` (no proxy, redirect refusal,
  timeout).
- `internal/notify/webhook`: `TestHTTPTransport_Post_DNSRebindingRefusedAtDial`
  (resolver seam returns a public IP at validation; the dial resolves
  `localhost` to loopback and is refused; the loopback server gets nothing);
  `TestHTTPTransport_Post_RedirectNotFollowed` (307 to a loopback server with
  an HMAC secret set; the target gets nothing);
  `TestNewHTTPTransport_BackstopTimeout`. Ordinary delivery stays covered by
  `TestHTTPTransport_Post_HeadersAndScrub`.
- `internal/notify/slack`: `TestHTTPTransport_Post_RedirectNotFollowed`.

The rebinding and redirect tests were run with `Control` and `CheckRedirect`
disabled and failed, then passed with them restored.
