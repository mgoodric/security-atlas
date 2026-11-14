# #2040 — Login BFF forwards the browser's client IP — decisions log

Build-time calls made while closing #2040, a follow-on to #1913. This log
does not block merge.

- detection_tier_actual: manual_review
- detection_tier_target: integration

(#1913's own decisions log recorded the shared bucket as a known limit.
A test that drives two clients through the BFF would have caught it.)

## D1 — Capture the socket peer in a node preload

Next.js 16 sets `X-Forwarded-For` to the socket peer **only when the
header is absent** (`next/dist/server/base-server.js`:
`req.headers['x-forwarded-for'] ??= socket.remoteAddress`). When a browser
sends its own header, the peer is gone before any App Router code runs,
and a server action has no socket API. So the BFF cannot tell a forged
header from a trusted proxy's from inside Next.

`web/peer-chain.cjs` is loaded with `node --require` (the web.Dockerfile
CMD and `npm run start`). It wraps `http.Server`/`https.Server` `emit` and,
for every request, **overwrites** `x-atlas-peer-chain` with
`<inbound XFF hops>, <socket peer>`. That is the chain a standards-following
proxy would build, and any copy of the header the browser sent is
discarded. It also sets a `globalThis` marker.

Rejected:

- A custom `server.js`. It means more code to keep in step with Next's
  standalone output.
- Trusting Next's `x-forwarded-for`. It is forgeable whenever web is
  reachable directly, which is the compose default.

## D2 — Fail closed without the preload

`web/lib/client-ip.ts` reads the chain only when the marker is set. Without
it (for example `next dev`), the header might be browser-supplied, so the
BFF forwards no `X-Forwarded-For`, which is the pre-#2040 behaviour. The
same applies to a malformed `WEB_TRUSTED_PROXY_CIDRS`, which is logged and
not partly applied. Only the single resolved address is forwarded, never
the inbound header.

## D3 — `WEB_TRUSTED_PROXY_CIDRS`, separate from `TRUSTED_PROXY_CIDRS`

The two allowlists answer different questions:

- web's: which proxies sit in front of web;
- atlas's: does it trust web.

In compose they share one `.env`, so a single name would force one value
onto both. The walk mirrors `internal/api/auth/clientip.go`: right to
left, stop at the first untrusted hop, cap of 50 hops.

## D4 — Compose: web on a fixed-address `bffnet`, atlas trusts only it

Trusting the compose default network is not safe. atlas publishes `:8080`,
and a host-published connection can arrive from that network's gateway,
which is inside the subnet. So web sits **only** on a new `bffnet`
(`10.125.0.0/29`) at `10.125.0.2`, atlas joins `default` and `bffnet`, and
atlas's `TRUSTED_PROXY_CIDRS` defaults to `10.125.0.2/32`. Web needs only
atlas (`ATLAS_HTTP_URL`). The subnet sits next to the proxy harness's
`10.123`/`10.124` and stays outside Docker's default `172.17–31` pools.

An operator who sets `TRUSTED_PROXY_CIDRS` for their own proxy replaces
the default. `.env.example` and the configuration reference both say to
keep `10.125.0.2/32` in that list.

## D5 — Helm and native: documented values, empty by default

Pod IPs are not known ahead of time, so the chart adds
`atlas.trustedProxyCIDRs` and `web.trustedProxyCIDRs`, both empty. The
chart README says to set both to the pod CIDR.

The native install exports `TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128`.
The API listens on loopback only, so that trusts only local processes.

## D6 — The edge compose file is unchanged

`docker-compose.edge.yml` runs the new web image. Its web forwards the
header, and its atlas ignores it because it trusts no proxy, so behaviour
there is unchanged. Wiring the edge channel is a separate change to that
file.
