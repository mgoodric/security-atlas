# 430 — Consolidated environment-variable / configuration reference page

**Cluster:** Docs
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**Why.** `deploy/docker/.env.example` documents ~25 environment keys piecemeal, in inline comments, in copy order rather than reference order. There is no single configuration-reference page on the docs site. An operator tuning host ports, deciding whether to set `ATLAS_SECURE_COOKIES`, wondering what `ATLAS_TEST_MODE` does (and that it must stay off in production), or wiring `DATABASE_URL_APP` / `DATABASE_URL_MIGRATE` / NATS / MinIO has to grep the `.env.example` comments or the Go source. The piecemeal-comment approach also makes it easy to miss a security-critical toggle (`ATLAS_METRICS_FALLBACK_ENABLE` opens an unauthenticated read surface; `ATLAS_TEST_MODE` mints admin JWTs).

**What.** One published page `docs-site/docs/configuration.md` — a single reference table over every configuration variable, with columns: **Variable · Default · Required? · Scope (server / web / bootstrap) · Description**. The page is grouped (Required · Database · Object store / NATS · Cookies & security · Observability · Test-mode · Ports) and explicitly flags the security-critical variables. An acceptance criterion keeps the page **in sync** with `.env.example` — either a test/script that diffs the documented variable set against the keys in `.env.example`, or a generation step that derives the table from it — so the page does not silently drift.

**Scope discipline.** Documentation + one sync mechanism. It does NOT change any variable's behavior, default, or name. It does NOT introduce new config. It consolidates and publishes what `.env.example` + the Go source already define, and adds a drift guard. The `.env.example` file stays as the copy-and-edit template; the new page is the human-readable reference.

## Threat model

Docs slice STRIDE pass. The brief calls this one out explicitly: a configuration reference MUST NOT document a secret's value or recommend an insecure default. A config page is uniquely dangerous because operators treat it as authoritative — if it shows a weak default as "fine", deployments inherit the weakness.

**S — Spoofing.** N/A (docs). The page documents `BEARER_HASH_KEY`, `ATLAS_BOOTSTRAP_TOKEN`, `ATLAS_DEFAULT_USER_PASSWORD` as **required, high-entropy, operator-supplied** — never with a real value.

**T — Tampering.** N/A (read-only docs). The sync guard (AC) protects the page from drifting away from the real config surface.

**R — Repudiation.** N/A.

**I — Information disclosure (load-bearing).** _Threat:_ a config reference that prints a real secret value, or a copy-pasteable "default" for a credential, leaks a secret into the published site. _Mitigation:_ secret-typed variables show `CHANGE_ME` / "(operator-supplied, high entropy)" / `openssl rand -hex 32`, never a value. The page MUST call out **which** `CHANGE_ME` values are security-critical (the `*_PASSWORD`, `*_KEY`, `*_TOKEN` set) so an operator does not leave a guessable one.

**D — Denial of service.** The page documents the port variables; it MUST NOT recommend binding admin/internal ports to `0.0.0.0` on a public interface without a note.

**E — Elevation of privilege (load-bearing).** Two variables are privilege-relevant and MUST be documented with their warnings intact:

- `ATLAS_TEST_MODE` — when set, mounts `POST /v1/test/issue-jwt`, which mints arbitrary admin-claim JWTs. The page MUST document "DO NOT set in production" prominently (matching the `.env.example` warning).
- `ATLAS_METRICS_FALLBACK_ENABLE` — when on, exposes an **unauthenticated** `/metrics` read surface that must be network-gated. The page MUST carry that warning.
- `ATLAS_SECURE_COOKIES` — the page MUST note it should be `true` behind TLS (a Secure cookie over plain HTTP is dropped), so operators don't ship session cookies without the Secure flag in production.

## Acceptance criteria

- [ ] **AC-1.** New file `docs-site/docs/configuration.md` exists with a reference table covering every variable in `deploy/docker/.env.example`.
- [ ] **AC-2.** The table has columns: Variable · Default · Required? · Scope (server / web / bootstrap) · Description.
- [ ] **AC-3.** The page groups variables into logical sections (Required · Database · Object store + NATS · Cookies & security · Observability · Test-mode · Ports), not a single flat dump.
- [ ] **AC-4.** Secret-typed variables (`POSTGRES_PASSWORD`, `ATLAS_APP_PASSWORD`, `MINIO_ROOT_PASSWORD`, `BEARER_HASH_KEY`, `ATLAS_BOOTSTRAP_TOKEN`, `ATLAS_DEFAULT_USER_PASSWORD`, the password segments of `DATABASE_URL_*`) show no real value — placeholder + `openssl rand -hex 32` guidance only.
- [ ] **AC-5.** The page explicitly flags which variables are **security-critical** (the set above) so an operator knows which `CHANGE_ME` values must not be left weak.
- [ ] **AC-6.** `ATLAS_TEST_MODE` is documented with the "DO NOT set in production — mints admin JWTs" warning.
- [ ] **AC-7.** `ATLAS_METRICS_FALLBACK_ENABLE` is documented with the "unauthenticated `/metrics`, must be network-gated, default off" warning.
- [ ] **AC-8.** `ATLAS_SECURE_COOKIES` is documented with the "set true behind TLS" guidance.
- [ ] **AC-9.** A sync mechanism exists: either (a) a test/script that fails when a key present in `.env.example` is absent from the page (or vice-versa), or (b) a generation step that derives the table from `.env.example`. The chosen approach is documented at the top of the page.
- [ ] **AC-10.** The sync mechanism runs in CI or as a `just` recipe / pre-commit hook, so drift is caught mechanically, not by eye.
- [ ] **AC-11.** A nav entry pointing at `configuration.md` is added to `docs-site/mkdocs.yml`.
- [ ] **AC-12.** `mkdocs build --strict` passes from `docs-site/`.
- [ ] **AC-13.** The page cross-links to `docs/SELF_HOSTING.md` (the deploy guide) and to `deploy/docker/.env.example` (the editable template) so the relationship between reference and template is clear.
- [ ] **AC-14.** Running the sync check against the current `.env.example` passes (the page is complete and accurate at merge time).

## Constitutional invariants honored

- **Tenant isolation via RLS** (#6) — the page documents `DATABASE_URL_APP` (atlas_app role) vs `DATABASE_URL_MIGRATE` (atlas_migrate role) accurately, reinforcing the role separation RLS depends on.
- **Manual evidence first-class / observability honesty** — documents the OTEL variables truthfully (no-op by default; opt-in export) rather than overstating "continuous monitoring".
- No invariant is altered; this is a documentation-discipline slice.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — the tech-stack choices each variable configures (Postgres, NATS, MinIO, OTEL, cookies).
- `docs/SELF_HOSTING.md` — the deploy guide the reference complements.

## Dependencies

- **#037** (docker-compose self-host bundle + `.env.example`) — `merged`. The source of truth the page consolidates.
- **#121** (OTEL) — `merged`. Source of the `OTEL_*` / metrics-fallback variables.
- **#201** (test-mode JWT issuance) — `merged`. Source of `ATLAS_TEST_MODE`.

## Anti-criteria (P0 — block merge)

- **P0-430-1.** Does NOT print a real secret value or a copy-pasteable credential default — secret-typed variables show placeholders + generation guidance only (threat-model I).
- **P0-430-2.** Does NOT recommend an insecure default: `ATLAS_TEST_MODE`, `ATLAS_METRICS_FALLBACK_ENABLE`, and `ATLAS_SECURE_COOKIES` ship their security warnings (threat-model E).
- **P0-430-3.** Does NOT change any variable's name, default, or behavior — documentation + sync guard only.
- **P0-430-4.** Does NOT let the page drift silently from `.env.example` — a mechanical sync check (AC-9/AC-10) is required, not a "remember to update both" note.
- **P0-430-5.** Does NOT document variables that do not exist or omit ones that do — AC-14's sync check against `.env.example` is the completeness guard.

## Skill mix (3-5)

- `grill-with-docs` — align the table against `.env.example` + the Go config-load source.
- `Security` — verify no secret value is printed and every security-critical toggle carries its warning.
- `database-designer` / `tdd` — if the sync mechanism is a test (AC-9 option a), write it test-first.
- `simplify` — keep the table scannable; group rather than flat-dump.

## Notes for the implementing agent

- The authoritative variable set is `deploy/docker/.env.example` (25 keys today). Cross-check against the Go config-load paths (`cmd/atlas/main.go`, `os.Getenv` call sites) in case any variable is read by the server but absent from `.env.example` — if so, that is a finding (document it and consider whether it belongs in `.env.example`, which may be a spillover).
- Sync-mechanism recommendation: option (a) a small Go or shell test that parses `^[A-Z_]+=` from `.env.example` and asserts each key appears in `configuration.md` (and warns on page keys absent from the example). This is cheaper and more robust than generation and reuses the project's existing test surfaces. Record the chosen approach + rationale in the decisions log.
- The `.env.example` already separates "REQUIRED — the stack refuses to start" from "OPTIONAL — sensible defaults"; carry that Required? distinction into the table's column rather than re-deriving it.
- `DATABASE_URL_MIGRATE` authenticates via trust on the container network and carries no password (atlas_migrate role) — document that nuance so an operator doesn't try to set a password segment on it.
- Detection-tier: a missing-variable drift bug would be `target=unit/integration` (the sync check). That is precisely the tier this slice adds, which is the point.
