# Slice 433 — readiness probe distinct from liveness · decisions log

**Type:** implementation follow-up · **Date:** 2026-08-04

## D1 — `/health` remains liveness

`GET /health` keeps the slice-037 contract: if the HTTP process is
serving it returns 200, even when the DB ping reports
`{"db":"degraded"}`. The docker-compose healthcheck still uses
`atlas --version`, and `atlas-bootstrap` still polls `/health`, so a
transient Postgres warm-up does not mark atlas unhealthy or restart-loop
the container.

## D2 — Readiness is `GET /ready`

`GET /ready` is the traffic-routing probe. It returns 200 only when
Postgres pings successfully; with no DB pool or a failed ping it returns
503 and a `not_ready` JSON body. The route is bearer- and authz-exempt so
load balancers and k8s readiness gates can call it without depending on
the very DB-backed authorization path being tested.

This first readiness check covers Postgres only. That is the dependency
behind the measured black-hole in Experiment 3: during the outage every
authenticated request failed because tenancy, authorization inputs,
feature flags, and API handlers need DB access. NATS and object storage
retain their existing startup gates and can receive separate readiness
checks when their user-facing outage modes are measured.

## D3 — `/healthz` divergence resolved by documentation correction

Slice 335's chaos design named `/healthz`, but the shipped liveness route
has always been `/health`. This slice does not add `/healthz` as another
liveness alias because it would expand the probe surface without creating
the readiness signal operators need. The design document is corrected to
probe `/health` for liveness and `/ready` for readiness.
