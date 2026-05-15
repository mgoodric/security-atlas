# 072 — Version string surfaced in the UI

**Cluster:** Frontend
**Estimate:** 1d
**Type:** AFK

## Narrative

The atlas server already gets its version baked in at build time via Go ldflags (`-X main.version={{.Version}}` per `.goreleaser.yaml` and the `justfile` `dev-build` recipe), but that version is invisible to the user. There's no `/v1/version` endpoint, no version in `/v1/me`, no display anywhere in the web UI. A user running the docker-compose self-host bundle (slice 037) or the Helm chart (slice 038) has no way to know what version they're actually on without `docker compose images atlas` or `kubectl describe pod`, neither of which is in the user's natural workflow.

This slice surfaces the version end-to-end: backend exposes it via a small read-only HTTP endpoint, frontend reads it once at app boot and displays it in a low-chrome footer that does not compete with the primary UI. The docker image, the Helm chart, and the OCI tags all get a `version` label so external tooling (registry browsers, `docker inspect`) sees it consistently.

**Three surfaces, one source of truth:**

1. **Backend:** `internal/version` (new package) exports `Version`, `Commit`, `BuildTime` populated from ldflags at build time. `cmd/atlas/main.go` reads them and exposes via `GET /v1/version` returning JSON `{ "version": "1.5.0", "commit": "1903818", "build_time": "2026-05-15T15:00:00Z", "go_version": "go1.26.1" }`. The endpoint is public (no auth required — same precedent as `/health`) because the version surface is metadata, not tenant data.
2. **Frontend:** a `web/lib/version.ts` client wraps the BFF route at `web/app/api/version/route.ts` which proxies `/v1/version`. Top-level layout (`web/app/(authed)/layout.tsx`) renders a footer component `VersionFooter` that reads `useVersion()` (TanStack Query, 24h stale time) and shows `v1.5.0 · 1903818` in muted small text at the bottom-right of the viewport. Clicking expands a popover with `build_time` and `go_version`. On the login page (which has no authed layout), the same footer renders without the popover.
3. **Container/Helm:** `deploy/docker/Dockerfile.atlas` adds `LABEL org.opencontainers.image.version="${VERSION}"` from the build arg; `deploy/helm/Chart.yaml` `appVersion` field is templated from the chart's release tag; both surfaces match what the Go binary reports.

**Why now, not earlier:** the v1 burn-down treated "version" as a release-please concern (the CHANGELOG header + git tag are authoritative), and that was correct for development. Now that v1 is shipped and self-hosted users exist, the runtime surface needs to answer "what am I running?" without making the user re-derive it from logs.

## Acceptance criteria

- [ ] AC-1: `internal/version/version.go` exports `Version string`, `Commit string`, `BuildTime string`, `GoVersion string` (the last from `runtime.Version()`). All four are populated from package-level vars overrideable via `-X` ldflags (`-X internal/version.Version=$(git describe ...)` etc.). Defaults when unset: `Version="dev"`, `Commit="unknown"`, `BuildTime=""`, `GoVersion` always from runtime.
- [ ] AC-2: `cmd/atlas/main.go` registers `GET /v1/version` returning JSON of the four fields. No auth required. Same pattern as `GET /health` (slice 037). `Content-Type: application/json`, `Cache-Control: public, max-age=300` (5-minute browser cache — version doesn't change between binary restarts).
- [ ] AC-3: `cmd/atlas-cli/main.go` gains a `version` subcommand that prints the four fields in `key=value` form (machine-readable). Existing `--version` flag, if present, is wired through this same package; if not present, this AC adds it.
- [ ] AC-4: `.goreleaser.yaml` ldflags expanded to set all four fields: `-X internal/version.Version={{.Version}} -X internal/version.Commit={{.ShortCommit}} -X internal/version.BuildTime={{.Date}}`. `justfile` `dev-build` recipe mirrors the same.
- [ ] AC-5: `deploy/docker/Dockerfile.atlas` adds `ARG VERSION` + `ARG COMMIT` + `ARG BUILD_TIME` and bakes them into the `go build` ldflags. The image gets `LABEL org.opencontainers.image.version="${VERSION}"`, `LABEL org.opencontainers.image.revision="${COMMIT}"`, `LABEL org.opencontainers.image.created="${BUILD_TIME}"` — the [OCI image annotations](https://github.com/opencontainers/image-spec/blob/main/annotations.md) standard set.
- [ ] AC-6: `deploy/helm/Chart.yaml` `appVersion` is `1.5.0` (or whatever HEAD is at chart-render time). The chart's `_helpers.tpl` exposes `atlas.appVersion` as a template helper; deployments and pods get `app.kubernetes.io/version` labels populated from it.
- [ ] AC-7: `web/lib/version.ts` exports `useVersion()` (TanStack Query, key `["version"]`, staleTime 24h, gcTime 7d — version doesn't change between binary restarts so aggressive caching is correct). Throws with a typed error code on transport failure; the UI degrades gracefully (footer shows "v?" rather than blocking render).
- [ ] AC-8: `web/app/api/version/route.ts` proxies `/v1/version` via the `bff.ts` helper. No bearer cookie needed (the upstream endpoint is public, AC-2); the proxy still sets `cache: "no-store"` upstream and reformats the cache headers for the browser.
- [ ] AC-9: `web/components/version-footer.tsx` renders a fixed-bottom-right `<footer>` with class `text-muted-foreground text-xs` (shadcn semantic colors) containing the rendered version. The footer is rendered by `web/app/(authed)/layout.tsx` AND by `web/app/login/page.tsx`. Click expands a `Popover` with `commit`, `build_time`, `go_version`. The popover does NOT contain any link that requires authentication.
- [ ] AC-10: `web/app/api/version/route.test.ts` covers the BFF route: happy path returns the upstream JSON shape; transport failure returns the typed error; cache headers are set correctly. Lands in the vitest seed from slice 069.
- [ ] AC-11: A new `web/e2e/version-footer.spec.ts` Playwright spec asserts the footer renders on both `/login` and `/dashboard`, and that the popover opens on click and shows the four fields. Follows slice-069 fixture pattern; `expect` is genuinely used (no unused-import drift like slice-069's pre-fix state).
- [ ] AC-12: README's "Self-hosting" section gains a "Verifying your version" subsection: shows `docker compose exec atlas atlas-cli version`, `curl http://localhost:8080/v1/version`, and "the version is also displayed in the bottom-right of every page in the UI" with a screenshot reference (forward-references slice 057's screenshot pipeline — if the screenshot is added in this slice, it follows the same `<picture>` pattern; if not, the reference is left as a TODO and recorded in the decisions log).
- [ ] AC-13: `docs-site/docs/install.md` (slice 058) gets a "Verifying your install" callout in the troubleshooting style — same three checks as AC-12.

## Constitutional invariants honored

- **AI-assist boundary**: nothing about the version surface is AI-generated; it's mechanical metadata read from build-time injection
- **Working norms — Style** (CLAUDE.md): footer is muted, low-chrome, doesn't compete with content; no emojis
- **Tenant isolation (invariant 6)**: the `/v1/version` endpoint is intentionally public — it is metadata about the binary, NOT tenant data. No RLS context required. Auth-bypass is INTENTIONAL and documented in the handler comment.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (deployment row — version surfacing is part of operational story)
- `Plans/canvas/10-roadmap.md §10.1` — the v1 binary success test's "knowability" extension (the user needs to know what they're running to evaluate it)

## Dependencies

- **005** (frontend bootstrap) — the layout pattern this slice's footer renders into
- **037** (docker-compose self-host bundle) — `Dockerfile.atlas` to update
- **038** (Helm chart for K8s) — `Chart.yaml` + helpers.tpl to update
- **039** (CLI release pipeline) — `.goreleaser.yaml` ldflags pattern already established; this slice extends it
- **058** (user docs scaffold) — `install.md` callout (AC-13)
- **069** (verification suite) — the vitest + Playwright surfaces this slice ships tests into

## Anti-criteria (P0 — block merge)

- **P0-A1**: The `/v1/version` endpoint does NOT require authentication. Adding auth to a metadata endpoint defeats the purpose ("what am I running?" should not depend on a working login) AND would silently re-bias the surface toward only authed users. This is a deliberate, recorded design decision — flagged in the handler doc comment.
- **P0-A2**: The version footer does NOT show on full-screen modal dialogs, the print-CSS for board pack export (slice 043 used `print:hidden` for similar reasons), or in any context where it would visually clutter a focused workflow. The footer is `position: fixed` with `bottom-2 right-3` and `pointer-events-auto` only on hover for the click target; everywhere else the click target is `pointer-events-none`.
- **P0-A3**: The popover does NOT include a link to "check for updates" or any kind of network call to a registry. We do not phone home; the user opted into a self-hosted install for that reason. The popover is read-only metadata.
- **P0-A4**: Does NOT change `web/package.json`'s `"version"` field (currently "0.0.0" — that's a npm-workspace artifact, not the user-facing version). The user-facing version source of truth is the Go binary's ldflags-injected `Version`, never the frontend package.json.
- **P0-A5**: Does NOT couple the displayed version to a network round-trip on every page load. The 24h staleTime + 7d gcTime in AC-7 means a typical session reads the version once; the BFF route + browser cache mean SSR can read it server-side for instant first-paint.
- **P0-A6**: Does NOT use shell `git rev-parse` or `date` at server runtime to compute these values. They are baked in at build time. Runtime computation would (a) produce the wrong commit hash (it'd be whatever `git` returns where the binary happens to be invoked, NOT the build commit), and (b) break the goreleaser/Docker build chain.

## Skill mix (3–5)

- Go HTTP handler ergonomics (small, public, no-auth endpoint; correct cache headers)
- TanStack Query staleTime/gcTime tuning (over-fetching is the failure mode here, not stale data)
- shadcn/ui `Popover` + responsive `<footer>` layout (low-chrome, low-priority screen real-estate)
- ldflags wiring (Go + goreleaser + Dockerfile build args; getting all three to agree is the load-bearing part)
- Playwright assertion patterns with real `expect` calls (close the unused-import gap engineer-069 had to patch)

## Notes for the implementing agent

- **Single source of truth — Go ldflags.** Every other surface (Docker label, Helm `appVersion`, frontend display) reads from Go. Resist any temptation to duplicate the source of truth into a `VERSION` file or a `package.json` field; drift on those is exactly what this slice prevents.
- The shadcn `Popover` semantics: keep it click-only (not hover-open), keep the trigger keyboard-accessible (`<button>` with `aria-label="Show build info"`), and ensure the popover content gets `role="region"` with the same aria-label. Accessibility regressions in a footer-of-every-page surface are high-blast-radius.
- For AC-11's Playwright spec, do NOT add it to the required-check list in `.github/branch-protection.json`. The seed-data harness gap from slice 069's AC-5 PARTIAL still applies — if the spec needs seed data to render the authed view, a fixture pattern from slice 069 is the right reuse, and the spec stays non-required-but-running until the seed-data harness lands.
- The `LABEL org.opencontainers.image.*` set in AC-5 is intentionally the OCI-standard names, not custom labels. This makes the surface inspectable by every registry browser and image scanner without bespoke knowledge of security-atlas.
