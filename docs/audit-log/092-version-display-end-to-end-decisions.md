# Decisions log — Slice 092 (Version display end-to-end fix)

This is an AFK slice (per `Plans/prompts/04-per-slice-template.md` "Slice types"). The slice bundles two mechanical bug fixes that share one symptom (`v(none)` in the deployed UI footer). Notable judgment calls are recorded below.

## Build-time judgment calls

### D1 — The "auth middleware" is `web/proxy.ts`, not `web/middleware.ts` (HIGH confidence)

**Decision:** add the `/api/version` exemption to `web/proxy.ts` (the file the slice file calls `web/middleware.ts` does not exist).

**Rationale:** Next.js 16 renamed the request-interceptor convention from `middleware.ts` to `proxy.ts` (see the in-file comment: "Next.js 16 renamed this convention from `middleware` to `proxy`"). `web/proxy.ts` is the only top-level request interceptor; `web/(authed)/layout.tsx` checks the session cookie at render time but it scopes only the `(authed)` route group, not `/api/*` routes. The 307 to `/login` observed via `curl http://192.168.1.246:3015/api/version` comes from the proxy.ts top-level redirect (line 17–21), confirmed by reading the file.

The slice file's "Notes for the implementing agent" explicitly anticipated this ambiguity: "if `web/middleware.ts` doesn't exist or the auth gate lives in a layout/route wrapper instead, surface that as a question … the mechanism depends on the actual middleware layout the codebase chose." The actual mechanism is `web/proxy.ts` — same Next.js feature, new file name.

**Alternatives considered:**

- Add the exemption to `web/(authed)/layout.tsx`: rejected — the layout only fires for routes inside the `(authed)` group; `/api/version` is not in that group, so the layout never sees the request. The 307 is upstream of the layout.
- Move `/api/version` route under a different segment that bypasses proxy.ts: rejected — proxy.ts's matcher is `[(?!_next/static|_next/image|favicon.ico).*]`, so every URL except those three statics flows through it. Reorganizing the route doesn't escape the matcher.
- Tighten proxy.ts matcher to exclude `/api/version` at the matcher layer (i.e., `[(?!_next/static|_next/image|favicon.ico|api/version).*]`): rejected — the existing exemption pattern in proxy.ts uses early-return inside the function (`if (pathname.startsWith("/login") || …) return NextResponse.next()`); matching that pattern keeps the file consistent. Mixing matcher-level + function-level exemptions invites future drift.

### D2 — Exact-prefix match `pathname === "/api/version"` (HIGH confidence, P0-A1 gate)

**Decision:** the exemption check is `pathname === "/api/version"` — an exact-equality test, not a `startsWith("/api/v")` or regex.

**Rationale:** P0-A1 of the slice file explicitly forbids a broader match: "A `^/api/v` regex (typo or laziness) would silently expose `/api/vendors`, `/api/audit/period`, etc. as unauthenticated. The matcher must be exact-path or anchored prefix." `/api/version` is a single route — no sub-routes — so exact equality is the tightest possible match. `startsWith("/api/version")` would also work today (no children exist) but creates a latent risk if `/api/version/<anything>` is ever added; equality fails closed.

This is consistent with the existing proxy.ts pattern: `startsWith("/login")` is used because `/login` legitimately has sub-routes (`/login?from=...` is the query, but the path stays `/login` exactly today; `startsWith` future-proofs the `/login/*` namespace). `/api/version` has no equivalent namespace need.

**Alternatives considered:**

- `pathname.startsWith("/api/version")`: works today, but per the reasoning above, equality is the tighter contract. If `/api/version/build` is ever a thing it should be evaluated separately for whether it should be public.
- Regex anchor `/^\/api\/version$/.test(pathname)`: equivalent to equality at higher cost. Equality is the idiomatic JS form.

### D3 — Verification of AC-3 (real version in published image) reduces to "verify on first release after merge" (HIGH confidence)

**Decision:** do NOT add a post-publish smoke step to `container-publish.yml` in this PR. AC-3 reduces to the "first release after this PR merges shows the real version" fallback documented in the slice file.

**Rationale:** Per AC-3: "If no straightforward post-publish smoke is feasible in this PR's scope, AC-3 reduces to 'first release after this PR merges shows the real version' — document the verification gap in the PR body." A `docker run --rm $IMAGE --version` step would need to (a) wait for the build step to complete + push (`steps.build.outputs.digest` is available), (b) pull the just-pushed image back, (c) run it with the right entrypoint, (d) grep the output. That's net-new logic in a workflow that already passed slice 037's verification gate, and adding it inflates the diff well beyond the slice's mechanical scope (just `build-args:`). The release cadence is high enough that the next release tag will validate within days; the verification gap is short-lived. The fallback is the right call.

**Alternatives considered:**

- Add a `docker run --rm` smoke step gated on `success()`: rejected per above — net-new workflow logic outside scope.
- Add a CI step that runs `docker buildx imagetools inspect` on the pushed tag and asserts `org.opencontainers.image.version` matches the release tag: cleaner than `docker run`, but still net-new workflow logic. Same scope concern.
- Spillover the smoke into its own slice: the verification-gap-on-merge is acceptable; not worth a separate slice.

### D4 — Use `steps.meta.outputs.version` (the metadata-action's semver-extracted primary version) (HIGH confidence)

**Decision:** `build-args` passes `VERSION=${{ steps.meta.outputs.version }}` — the `docker/metadata-action@v6` `version` output, which extracts the canonical semver from the release tag.

**Rationale:** `docker/metadata-action@v6` exposes three documented outputs: `tags` (newline-separated full image refs), `labels` (newline-separated OCI labels), and `version` (the single semver string extracted from the tag — e.g. `1.5.2` for tag `v1.5.2`). The slice file's "Notes for the implementing agent" hedge ("Confirm the exact metadata-action output name") is resolved: `version` is the documented field; it is the value the existing `org.opencontainers.image.version` label in the Dockerfile labels already consumes upstream of this slice (line 67 of `deploy/docker/atlas.Dockerfile` — `LABEL org.opencontainers.image.version="${VERSION}"`).

Single source of truth: the same value flows into the binary's `-X main.version=` ldflag AND the OCI image's `org.opencontainers.image.version` label AND (transitively, after this slice merges) the JSON returned by `/v1/version`. No other workflow step needs change.

**Alternatives considered:**

- `${{ github.ref_name }}`: equals the raw tag (`v1.5.2`), with the leading `v`. The metadata-action's `version` output strips the `v` and produces `1.5.2`. Either form works, but for consistency with the OCI label upstream (`org.opencontainers.image.version` is, by convention, the bare semver), use the metadata-action output.
- `${{ steps.meta.outputs.tags }}` and parse it: works but fragile (multi-line, ordering-dependent). The `version` output exists for exactly this case; use it.

### D5 — Same `build-args` block applies to all four images in the matrix (HIGH confidence)

**Decision:** the `build-args:` block is added once at the matrix-shared `Build + push` step, not gated on `matrix.image.name == 'atlas'`.

**Rationale:** the four images (atlas, atlas-cli, web, bootstrap) all benefit from baked version metadata:

- `atlas.Dockerfile` and `atlas-cli.Dockerfile` already declare `ARG VERSION` (atlas-cli mirrors atlas's contract; see slice 072).
- `web.Dockerfile` and `bootstrap.Dockerfile`: if they don't declare `ARG VERSION`, Docker's behavior is to silently ignore the build-arg (warning only). No build break, no functional change, no diff to those Dockerfiles required.

Gating the block on `matrix.image.name == 'atlas'` would require `if:` conditionals at the step level, which inflates complexity for zero benefit. The matrix-shared form is simpler AND right.

**Alternatives considered:**

- Per-image conditional: rejected per above.
- Add `ARG VERSION` to `web.Dockerfile` + `bootstrap.Dockerfile` and use the value in their OCI labels: scope creep. If a future slice wants those images to carry version metadata in labels, that's a separate, focused slice (or part of slice 080's release-pipeline rollup).

### D6 — Playwright spec follows the slice-073 skip-when-precondition-missing pattern (HIGH confidence)

**Decision:** the new e2e spec asserts `/api/version` is reachable unauthenticated AND that `/api/admin/me` returns 307/401 unauthenticated. It uses the `test` (not `authed`) Playwright runner — no fixture, no seed data, no `TEST_BEARER` required.

**Rationale:** the slice asserts a public path's reachability, which by definition needs no auth. There's nothing to seed. AC-8 says: "If `web/e2e/` is still un-wired (slice 069 not yet merged), the test lives as a comment-stub spec following the existing `ifPlaywright`-shim convention." Slice 069's e2e runner is wired (per the CLAUDE.md testing-discipline table); the spec is real, not a comment stub.

The companion assertion ("an existing tenant-scoped route continues to 307/401 for unauth requests") satisfies AC-7. Choosing `/api/admin/me` (the route the slice file names) over a different tenant-scoped route keeps the spec aligned with the AC text. `/api/admin/me` returns JSON 401 (not 307) when unauth via the BFF — the proxy.ts redirect happens first in browser context because `web/app/api/admin/me/route.ts` is matched by the proxy matcher BEFORE the route handler runs. Verified: `/api/admin/me` is NOT exempted in proxy.ts, so the proxy returns 307 to /login.

So the spec asserts:

- `/api/version` → 200 OK with JSON body (NOT 307).
- `/api/admin/me` → 307 (the proxy.ts redirect-to-login). This confirms the exemption is scoped and other routes still gate.

**Alternatives considered:**

- Test against `/api/vendors` instead of `/api/admin/me`: equivalent. The slice file picks `/api/admin/me` as the example; use it for consistency with the AC text.
- Skip the AC-7 negative test ("the exemption is scoped"): rejected — P0-A1 is the load-bearing constraint of this slice; the negative test is the live proof.

### D7 — vitest unit test for the proxy.ts exemption (HIGH confidence, defense in depth)

**Decision:** add a unit test at `web/proxy.test.ts` that calls the `proxy` function with synthetic `NextRequest`s and asserts exemption-vs-redirect behavior. Belt-and-suspenders with the Playwright spec.

**Rationale:** the slice file's testing-discipline section (CLAUDE.md `Testing discipline`) lists frontend vitest as the module-level gate, separate from Playwright. The exemption is a five-line edit but it's the load-bearing P0-A1 enforcement; a unit test that explicitly enumerates "`/api/version` exempt, `/api/version/anything` NOT exempt, `/api/vendors` NOT exempt, `/api/version2` NOT exempt" is the right gate against future regression (someone "improving" the matcher and breaking the equality contract).

**Alternatives considered:**

- Skip the unit test, rely on the Playwright spec only: rejected — Playwright spec is slower (browser boot per run) and the AC-1 exemption-shape check is best done as a fast unit test. Both are cheap.

## Acceptance criteria status

- [x] AC-1: `.github/workflows/container-publish.yml` adds `build-args:` to the `Build + push` step (VERSION, COMMIT, BUILD_TIME).
- [x] AC-2: `deploy/docker/atlas.Dockerfile` ARG defaults preserved — no source-level change required.
- [x] AC-3: Reduced to "verify on first release after merge" per D3. Verification gap documented in the PR body.
- [x] AC-4: COMMIT and BUILD_TIME are also passed as build-args; `cmd/atlas/version.go` already exposes `version`, `commit`, `date` ldflag-targets (verified — P0-A5 is satisfied; no spillover needed).
- [x] AC-5: `web/proxy.ts` (the Next.js 16 proxy — see D1) adds `/api/version` to its exemption list with exact-equality match.
- [x] AC-6: Verified locally with the vitest unit test (D7) — synthetic request to `/api/version` returns `NextResponse.next()`, not a redirect. Live `curl -sI` verification deferred to deployment.
- [x] AC-7: The vitest unit test (D7) AND the Playwright spec (D6) both verify `/api/admin/me` still 307s for unauthenticated requests.
- [x] AC-8: Playwright spec lives at `web/e2e/version-public.spec.ts` (real, not stubbed — see D6).
- [x] AC-9: First release after merge will validate the deployed UI footer. Captured in PR body.

## Open follow-ons (not for this slice)

- D3 verification gap closes at the next release. If the first release after this PR still shows `dev`, file a follow-on slice to add the post-publish smoke step that D3 explicitly deferred.
- If a future need arises for `web` or `bootstrap` images to carry baked version metadata in their OCI labels (D5), that's a separate slice — likely part of slice 080's continuation.

## Revisit-once-in-use list

- **D2 (exact-equality match):** if `/api/version/<something>` becomes a thing (e.g., `/api/version/changelog`), the equality match will deny it. The right answer at that time is to explicitly evaluate per-subroute whether public access is correct — NOT to relax to `startsWith` reflexively.
