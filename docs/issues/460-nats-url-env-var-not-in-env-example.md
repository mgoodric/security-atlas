# 460 — `NATS_URL` is read by the server but absent from `.env.example`

**Cluster:** Docs / Config hygiene
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`

## Surfaced during slice 430

While building the consolidated configuration-reference page (slice 430),
the cross-check of `.env.example` against the Go config-load paths (per
that slice's "Notes for the implementing agent") surfaced one variable the
**server reads** that the **template does not declare**:

- `cmd/atlas/main.go:194` — `if natsURL := os.Getenv("NATS_URL"); natsURL != "" && ...` gates the JetStream wiring; when unset, the platform runs in dev mode where push goes in-process (no durable substrate).
- `deploy/docker/docker-compose.yml:303` — the `atlas` service sets `NATS_URL: nats://nats:4222` **inline** (a fixed compose-network address), so it never flows through `.env.example`.

This is **not** a bug — the value is a fixed service-to-service address
inside the compose network, not an operator-tunable knob, so it was
deliberately hardcoded in compose rather than templated. The slice-430
reference page documents the operator-facing `.env.example` surface and
correctly omits it (the drift guard scopes to `.env.example` keys).

## Decision to make

Two reasonable options; this slice picks one:

1. **Leave as-is (recommended).** `NATS_URL` is infrastructure wiring, not
   operator config. Document the omission rationale in a one-line comment
   in `docker-compose.yml` near line 303 so the next config audit does not
   re-flag it. Lowest cost; preserves the "`.env.example` = operator
   knobs" boundary.
2. **Promote to `.env.example`** as an optional override (e.g. for pointing
   at an external NATS cluster). Only worth it if external-NATS support is
   a real near-term need; otherwise it adds a knob nobody turns. If chosen,
   the slice-430 drift guard automatically requires it on the config
   reference page (no extra work — the guard already covers commented
   opt-in keys).

## Acceptance criteria

- [ ] **AC-1.** A decision is recorded (leave-as-is vs promote) with rationale.
- [ ] **AC-2.** If leave-as-is: a clarifying comment lands at the `NATS_URL:` line in `docker-compose.yml` so the omission is intentional-on-its-face to a future auditor.
- [ ] **AC-3.** If promote: `NATS_URL` is added to `.env.example` (commented, with the dev-mode-when-unset note) and the slice-430 config reference page gains its row; `just config-reference-drift-check` passes.

## Notes

- No runtime behavior change in option 1; option 2 only adds an override
  path that defaults to today's hardcoded value.
- This is the only server-read env var found absent from `.env.example`
  during the slice-430 audit; all 25 active + 3 commented opt-in keys are
  now documented and drift-guarded.
