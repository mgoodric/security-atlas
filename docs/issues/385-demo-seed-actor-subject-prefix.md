# 385 — Demo-seed 500: resolveActor rejects "user:" JWT subject prefix

**Cluster:** Backend / Auth
**Estimate:** 0.5d
**Type:** JUDGMENT (hotfix)
**Status:** `ready`

## Narrative

Surfaced live on `atlas-edge` 2026-05-29: clicking the admin demo-seed button
returned HTTP 500 `{"error":"internal error"}` on every attempt. Reproduced
end-to-end against the deployment; the backend log gives the exact cause:

```
op="resolve actor" path=/v1/admin/demo/seed status=500 error="actor user_id missing"
```

`internal/api/admindemo/handler.go` `resolveActor` extracts the acting admin's
user id with `uuid.Parse(claims.Subject)`. But under auth-substrate-v2 every
atlas JWT sets `Subject = "user:" + <uuid>` (see `internal/api/auth/http.go`
LocalLogin and `internal/api/oauth/pkce.go` `buildAtlasClaimsForUser`).
`uuid.Parse("user:<uuid>")` fails, `actorID` stays `uuid.Nil`, the legacy
credstore fallback is also `"user:<uuid>"` (jwtmw sets `cred.UserID =
claims.Subject`), and the handler returns `"actor user_id missing"` → 500
before any seeding runs.

### Why CI never caught it

The `demoseed` integration tests exercise `Seeder.Apply` directly, bypassing the
HTTP handler. The `admindemo` unit + integration tests build the actor only via
the legacy `authctx.WithCredential` path with a **bare-UUID** `cred.UserID` —
they never drive the real `jwtmw.FromContext` path with a `user:`-prefixed
subject. This is the test gap the regression test closes.

## What ships

1. `subjectUserID(s)` helper strips the `"user:"` prefix (no-op for bare UUIDs).
2. `resolveActor` applies it to both the `jwtmw` subject and the credstore
   fallback before `uuid.Parse`.
3. Regression unit tests driving the real JWT path
   (`TestResolveActor_JWTSubjectCarriesUserPrefix`) and the bare-UUID path
   (`TestResolveActor_BareUUIDSubjectStillWorks`).

## Acceptance criteria

- AC-1: `resolveActor` resolves the actor user id from a `user:<uuid>` JWT subject.
- AC-2: a bare-UUID subject still resolves (TrimPrefix no-op).
- AC-3: demo-seed POST no longer 500s on actor resolution for an authenticated admin.
- AC-4: no behavior change to the gate / rate-limit / audit-row paths.

## Out of scope (filed separately)

- metrics_catalog `source_slices` empty-array NULL bug → slice 386.
- metrics evaluator query drift (`framework_id`, `policy_versions`) → follow-up.
