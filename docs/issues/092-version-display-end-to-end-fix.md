# 092 — Version display end-to-end fix (publish-arg + middleware exemption)

**Cluster:** Infra / verification
**Estimate:** 0.5d
**Type:** AFK

## Narrative

The version-footer in the deployed UI displays `v(none)` / `unknown`, and the `/v1/version` endpoint on a deployed atlas binary returns:

```
{"version":"dev","commit":"none","build_time":"unknown","go_version":"go1.26.3"}
```

Two distinct bugs combine to produce this:

### Bug A — `container-publish.yml` doesn't pass `VERSION` build-arg

`deploy/docker/atlas.Dockerfile` already accepts `ARG VERSION=dev` and bakes it via `-ldflags "-s -w -X main.version=${VERSION}"`. The `container-publish.yml` workflow's `docker/build-push-action@v5` step does NOT pass `build-args:` for `VERSION`, so the Dockerfile's `dev` default is what gets baked into every released image. Every Watchtower-pulled image — including v1.5.x already in production — therefore reports `version=dev`. The same gap applies to `commit` and `build_time` if `main.go` references them via similar variables (verify during implementation).

### Bug B — Next.js middleware redirects `/api/version` to `/login`

`web/app/api/version/route.ts` is documented as intentionally public — the route comment says "this one does NOT forward a bearer cookie. The upstream `/v1/version` is intentionally public (anti-criterion P0-A1) — it returns metadata about the binary, not tenant data." But the auth middleware intercepts every `/api/*` request before the route handler runs, returning `HTTP 307 → /login?from=%2Fapi%2Fversion` for an unauthenticated browser. Direct curl from inside the network confirms:

```
$ curl -s http://192.168.1.246:3015/api/version
/login?from=%2Fapi%2Fversion
```

The exemption list in the middleware (likely `web/middleware.ts` or equivalent — verify path during implementation) needs `/api/version` added next to whatever existing public-path entries it has (`/login`, `/api/auth/*`, `/health`, etc.).

### Why one slice (not two)

Both bugs manifest as the same symptom ("version doesn't display"). A user investigating only sees one problem. Fixing only one of them leaves the symptom present — Bug A alone fixes the upstream value but the BFF still 307s; Bug B alone exposes a value that's permanently `dev`. The fixes are mechanical and small; bundle them so the symptom goes green in one PR. The implementing agent may still split into two commits within the same PR if review clarity demands.

## Acceptance criteria

### Bug A — bake VERSION into the published image

- [ ] AC-1: `.github/workflows/container-publish.yml` `docker/build-push-action@v5` step adds `build-args:` block setting `VERSION=${{ steps.meta.outputs.version }}` (the release-tag-derived version metadata-action produces, e.g. `1.5.2`). Confirm the metadata-action output name in the actual workflow before wiring — it may be `version`, `tags`, or accessible via the `meta` step's labels.
- [ ] AC-2: The Dockerfile's `ARG VERSION=dev` default remains for local builds — only the CI workflow overrides it. No source-level change to `deploy/docker/atlas.Dockerfile` required; it already accepts the ARG.
- [ ] AC-3: Manual verification (or, preferably, a tiny CI smoke step): after a release tag is pushed, the published image's `/v1/version` returns `version=<release-tag>`, not `version=dev`. Acceptable form: a follow-up workflow step that does `docker run --rm $IMAGE --version` and `grep -v 'dev'`. If no straightforward post-publish smoke is feasible in this PR's scope, AC-3 reduces to "first release after this PR merges shows the real version" — document the verification gap in the PR body.
- [ ] AC-4: Same treatment for `commit` and `build_time` if `cmd/atlas/main.go` (or wherever the version values are declared) accepts `-X main.commit=...` and `-X main.build_time=...` ldflags. If those vars don't exist in source today, this slice does NOT add them — it ratchets only what the binary already supports. File the source-side extension as a spillover slice.

### Bug B — exempt /api/version from auth middleware

- [ ] AC-5: Locate the auth middleware (likely `web/middleware.ts`) and add `/api/version` to its public-path exemption list. The match should be exact-path or prefix `^/api/version$` — not a broader `^/api/v` glob that would accidentally pass other routes.
- [ ] AC-6: After the change, `curl -sI http://<host>/api/version` returns HTTP 200 with the JSON body (with `Cache-Control: public, max-age=300` as the BFF route already sets) — NOT a 307 to `/login`.
- [ ] AC-7: An existing tenant-scoped route (e.g. `/api/admin/me`) continues to return 307 → `/login` for unauthenticated requests, confirming the exemption is scoped to exactly `/api/version`.
- [ ] AC-8: Add (or extend) a Playwright spec that asserts `/api/version` is reachable without sign-in. If `web/e2e/` is still un-wired (slice 069 not yet merged), the test lives as a comment-stub spec following the existing `ifPlaywright`-shim convention.

### End-to-end smoke

- [ ] AC-9: After both bugs are fixed and a new release publishes, the deployed UI's `version-footer.tsx` displays a real version string (e.g. `v1.5.2`) instead of `unknown`. Manual verification on `atlas.home.gmoney.sh` or equivalent dev/staging URL. Capture a one-line screenshot or curl receipt in the PR body for the audit trail.

## Constitutional invariants honored

- **CLAUDE.md "no Vercel/Next-template branding":** indirect — a footer that says `version=dev` reads as "this is unfinished software." Fixing it is part of making the platform feel shipped.
- **Slice 037 acceptance criterion AC-1 (5-min bring-up):** the version-footer is the most-visible "deploy worked" signal on the dashboard chrome. Today it tells the operator "you got dev, this is broken." After this slice, it tells them "you got v1.5.2, you're current."

## Canvas references

- `Plans/canvas/09-tech-stack.md` (build/release pipeline — confirm the VERSION variable lives where expected)
- `deploy/docker/atlas.Dockerfile` (lines 34, 38 — the `ARG VERSION=dev` and `-X main.version=${VERSION}` it controls)
- `web/app/api/version/route.ts` (the BFF route comment that explicitly says "intentionally public")
- `web/components/version-footer.tsx` (the UI surface that reads the version via `useVersion()`)

## Dependencies

- #037 (merged) — provides the `container-publish.yml` workflow this slice patches
- #069 (in flight) — if merged first, AC-8's Playwright spec uses the real runner; if not, lives behind the existing shim convention

## Anti-criteria (P0 — block merge)

- **P0-A1:** Does NOT broaden the middleware exemption beyond exactly `/api/version`. A `^/api/v` regex (typo or laziness) would silently expose `/api/vendors`, `/api/audit/period`, etc. as unauthenticated. The matcher must be exact-path or anchored prefix.
- **P0-A2:** Does NOT change the `Cache-Control: public, max-age=300` value the BFF route already sets. The 5-minute cache is intentional (the route comment explains why); raising or lowering it is a separate decision.
- **P0-A3:** Does NOT change the `version-footer.tsx` rendering or hook (`web/lib/version.ts`). The frontend is already correct; the data it consumes is the broken layer.
- **P0-A4:** Does NOT touch any other CI workflow (`release.yml`, `docs-publish.yml`, etc.). The fix is scoped to `container-publish.yml`. Other release-pipeline gaps belong to slice 080 or its successors.
- **P0-A5:** Does NOT add new ldflag-injected source variables (`main.commit`, `main.build_time`) if they don't exist already. Spillover slice if they're missing.
- **P0-A6:** Does NOT use vendor-prefixed tokens in any new test fixture — neutral `test-*` only.

## Skill mix (3–5)

- GitHub Actions workflow editing (`docker/build-push-action@v5` build-args, `docker/metadata-action@v5` output names)
- Next.js middleware exemption patterns (path-matcher syntax in `web/middleware.ts`)
- Verification via `curl -I` + `docker run --rm $IMAGE --version` smoke
- Spillover discipline (file `commit`/`build_time` extension as a separate slice rather than expanding this one)

## Notes for the implementing agent

- Reference for the build-args wiring in `container-publish.yml`:
  ```yaml
  - name: Build + push
    uses: docker/build-push-action@v5
    with:
      context: .
      file: ${{ matrix.image.dockerfile }}
      platforms: linux/amd64,linux/arm64
      push: true
      build-args: |
        VERSION=${{ steps.meta.outputs.version }}
      # ... existing tags / labels / cache-from / cache-to ...
  ```
  Confirm the exact metadata-action output name (`version`, `tags`, etc.) by reading the `meta` step's current shape in the workflow before committing — different versions of `docker/metadata-action` expose differently named outputs.
- For the middleware exemption: if `web/middleware.ts` doesn't exist or the auth gate lives in a layout/route wrapper instead, surface that as a question in the per-slice grill rather than guessing where to wire the exemption. The intent ("make `/api/version` reachable unauthenticated") is clear; the mechanism depends on the actual middleware layout the codebase chose.
- Surfaced during the 2026-05-15 deploy-walkthrough session at `~/.claude/MEMORY/WORK/20260514-064726_security-atlas-unraid-deploy/`. The user landed on the dashboard after login and noticed the version-footer didn't show a real version. Captured per the continuous-batch spillover-as-slice convention (`Plans/prompts/07-continuous-batch-loop.md` Amendment 2).
