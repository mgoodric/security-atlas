# 2849 — A policy-sync API-key purpose, refused everywhere else: decisions log

Issue: mgoodric/security-atlas#2849, under the Confluence policy sync spec
(#2813, Q33). The sync endpoint that will accept these keys is #2850.

## Detection-tier classification

- **detection_tier_actual:** integration. One latent bug was found while
  building, in D4: `apikeystore.Rotate` dropped `api_keys.purpose`.
- **detection_tier_target:** integration (the rotate path writes through
  Postgres, and only a round trip through the row shows the loss).

## Decisions

### D1 — 403 on HTTP needs a gate that recognises the key

On `/v1/*` only JWTs authenticate: an API key bearer falls through jwtmw
and `requireCredential` answers 401. The ticket asks for 403, and #2850's
sync endpoint has to authenticate the key over HTTP anyway. So
`refusePolicySyncKeys` (internal/api/policysync*gate.go) runs ahead of jwtmw.
It looks up an `atlas*`-prefixed bearer with `apikeystore.Identify`and
answers 403 when the key's purpose is`policy-sync`. Every other request
passes through unchanged, so an ordinary API key still gets the same 401.
Running ahead of jwtmw means a session cookie on the same request does not
let a policy-sync key through. #2850 adds its one path to the gate.

`Identify` is `Authenticate` without the `last_used_at` bump. Without it,
an ordinary key presented to `/v1` would show as used even though the
request was refused.

### D2 — gRPC refuses with PermissionDenied

`authInterceptor` refuses a policy-sync key on every RPC with
PermissionDenied, the gRPC counterpart of 403, next to the calendar-token
refusal. The calendar token gets Unauthenticated because it is no bearer at all. A policy-sync key is
a real bearer with no grant, so PermissionDenied fits it.

### D3 — a policy-sync key carries no other grant

`POST /v1/admin/credentials` refuses, with 400, a `policy-sync` key that
also asks for `is_admin`, `is_approver`, `owner_roles` or `allowed_kinds`.
Any of these would read in the key list as access the key does not have.
`allowed_kinds` would also let a key carry `calendar.read.v1`. The
calendar handler refuses policy-sync keys as well, as defence in depth.
Through this route, `purpose` takes only `""` or `policy-sync`.
`bootstrap-recovery` stays minted only by reset-bootstrap.

### D4 — Rotate keeps the purpose

`apikeystore.Rotate` built the successor without `pred.Purpose`, so
rotating a marked key would have minted an ordinary key. For a policy-sync
key that is a general key. The successor now inherits the purpose, as the
credstore's own rotate already did. This also fixes the same loss for
`bootstrap-recovery` keys rotated through the HTTP route.

### D5 — `oauth migrate-api-key` refuses a policy-sync key

The CLI turns an API key into an OAuth client in the key's tenant. From a
policy-sync key, that client would act in the tenant without the limit.
The CLI now refuses such a key.

### D6 — no web mint control in this ticket

"The normal key-issuing path" is `POST /v1/admin/credentials`. The web key
list shows a key's purpose as a badge, since a policy-sync key with no
kinds would otherwise read as "any kind". The issue form gains no purpose
control. That belongs with the connector setup flow, once #2850 gives the
key something to do.

### D7 — the gate fails closed on a lookup error (review)

When `Identify` fails with anything but `ErrUnknownKey`, such as an
unreachable database or a failed owner-status read, the gate answers 503
and stops. Passing the request on would let a later bearer path (the
legacy `httpAuthMiddlewareWithExemptions` fallback to
`apikeys.Authenticate`) admit a policy-sync key the gate could not check.
An unknown, revoked or expired key still passes through to the 401.

### D8 — no streaming RPCs; a test keeps it so (review)

`authInterceptor` is unary only, and there is no stream interceptor. No
service registers a streaming method: every generated `ServiceDesc` has an
empty `Streams`. A streaming RPC would skip every bearer check, not just
this one. `TestGRPCServices_HaveNoStreamingMethods` walks the real
server's services and fails when one appears, so the stream interceptor
has to land with it.
