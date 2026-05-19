# Slice 150 — decisions log

Slice 150 is an `AFK` slice — the engineer makes the build-time calls
and records them here for the maintainer's post-deployment review. This
file captures the judgment calls surfaced during build that were not
specified in `docs/issues/150-empty-set-robustness-audit-across-list-
endpoints.md`.

## D1 — Three operator-confirmed broken endpoints were already correct on the integration harness

The slice doc names `GET /v1/controls/drift`, the dashboard metrics
panel, and `GET /v1/policies` as the operator-reported 500-on-fresh-
install paths. Per-package integration tests against a freshly-migrated
empty database (this slice's `*_empty_set_integration_test.go` files)
showed all three return 200 with a well-shaped empty envelope. The
handler code in each (`internal/api/freshnessdrift/handlers.go`,
`internal/api/dashboard/handler.go`, `internal/api/policies/handlers.go`)
was already defensive — `make(...)` slices initialised pre-query, no
`rows[0]` indexing, no division by zero on aggregates.

**Decision:** Pin the existing correct behaviour with per-package
integration tests so a future regression that reintroduces a 500-on-
empty fails the merge before the PR can land. Do NOT touch the three
handler implementations — they are already correct. The original
operator report's 500 was most likely produced by a different code
path (e.g. an interaction with a partially-migrated database, an
older binary against a newer schema, or a frontend BFF surfacing a
different upstream error), and the audit sweep + per-package tests
make any future regression of THIS specific failure mode merge-
blocking.

The cross-cutting audit at
`internal/api/emptyset/audit_integration_test.go` is the regression-
gate proof: every GET list / aggregate endpoint the platform
exposes is hit against an empty tenant, and a 5xx fails the suite.

## D2 — `/v1/me/acknowledgments` 500 on bootstrap credential is the real underlying bug

The audit sweep DID surface one real 500-on-fresh-install bug:
`GET /v1/me/acknowledgments` returned 500 with
`{"error":"list pending acks: policy_ack: parse user id: invalid UUID format"}`.

Root cause: the bootstrap-owner credential minted by
`credstore.Store.IssueOwner` carries `UserID = "key_<rand>"` (see
`internal/api/credstore/credstore.go:171`), not a UUID. The slice-023
`policy.AckStore.PendingForUser` flow parses `caller.UserID` as a UUID
and surfaces the parse error to the handler, which returns 500.

**Decision:** Fix it in the slice-023 handler
(`internal/api/policyacks/handlers.go MyAcknowledgments`) by detecting
the non-UUID case BEFORE calling into the store and returning the
empty envelope `{pending: [], count: 0, window_seconds: <int>}` with 200. The semantic is correct: a credential without a real users row
is a service account; service accounts have no human pending
acknowledgments. The store-level `PendingForUser` flow is left
unchanged — real human credentials (post-slice-034 OIDC) still go
through the existing path and get the real list.

**Rejected alternatives:**

- Return 401 "credential carries no user id" (parallel to
  `ErrAckMissingUser`): would have caused the dashboard panel to
  display an auth error on a fresh install, which is a worse UX than
  the empty list and conflicts with the operator's expectation that
  the dashboard panel renders cleanly.
- Fix in `PendingForUser` to map non-UUID to empty: rejected because
  the store's contract is "the caller has been validated"; pushing
  this defensive shape into every call site that constructs an
  `AckCaller` is a wider blast radius than the one handler fix.

## D3 — Audit set scope: GET list / aggregate endpoints only

The slice doc says "list + aggregate endpoints". The audit set in
`internal/api/emptyset/audit_integration_test.go` enumerates every
`root.Get("/v1/...")` from `internal/api/httpserver.go` that returns
a multi-row shape. Bare-`{id}` GETs are EXCLUDED — they have their
own well-defined `404 ErrNotFound` semantic that is orthogonal to
empty-set robustness (an unknown id is a 404, not an empty list).
POST / PATCH / DELETE routes are EXCLUDED — they validate inputs
and have their own per-handler error contracts.

`/v1/admin/credentials` is EXCLUDED from the audit sweep because the
route only mounts when `s.apikeyStore != nil`, which the
`api.New(api.Config{})` test harness does not provide. The empty-set
contract for that route belongs to its own per-package integration
test under its real wiring; that test does not exist today and is
beyond the scope of slice 150 (a content-coverage gap, not an
empty-set bug). Listed here so a future engineer who notices the
omission has the context.

## D4 — `looksLikeArrayKey` heuristic is intentionally conservative

The `looksLikeArrayKey` helper in
`internal/api/emptyset/audit_integration_test.go` uses an explicit
whitelist of plural top-level field names rather than a generic
`strings.HasSuffix("s")` rule. A heuristic that flags every key
ending in `s` would false-positive on `status`, `address`,
`next_cursor`, and similar singulars that legitimately hold a
string or object on the empty-set path.

**Decision:** Keep the explicit whitelist; require an engineer
adding a new list endpoint to add the plural top-level key to the
list. The discipline is documented in CONTRIBUTING.md's
"Empty-set robustness" section. A future regression that uses an
un-whitelisted plural key will fail to enforce the array-not-null
contract at the cross-cutting sweep level — but it WILL still be
caught by the per-package empty_set_integration_test, so the
defence-in-depth holds.
