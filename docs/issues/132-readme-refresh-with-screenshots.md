# 132 — README refresh with fresh screenshots (v1.10.0+ baseline)

**Cluster:** Docs
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` from a maintainer-driven full-docs cleanup. The README was last screenshot-refreshed in slice 057 (early v1 era). Since then the platform has grown substantially — slices 058 (user docs scaffold) added mkdocs; slices 070-077 added onboarding walkthroughs + logo work; slices 091-130 added pages (audit-log, dashboards, settings, audit workspace, risk hierarchy, admin pages); slices 117 + 127 + 128 hardened CI; slices 124 + 125 + 126 + 129 + 130 shipped the audit-log trio. The current screenshots do not reflect the operator-grade UI that exists today.

Separately, the user reported on 2026-05-18 that the logo on the login page was broken on their v1.9.0 Unraid deployment. That bug (`web/proxy.ts` `PUBLIC_STATIC_FILES` exemption gap) was fixed in slice 123 (merged 2026-05-18); v1.10.0 ships the fix. Screenshots captured before v1.10.0 would re-introduce the visible regression (broken logo on login). This slice therefore explicitly gates screenshot capture on the v1.10.0+ release.

**What this slice ships:** a refreshed `README.md` with current screenshots captured from a running v1.10.0+ build (or `main` post-`ba49891`) against a sanitized demo-seed fixture. Every screenshot is regenerated. The text is refreshed where the platform has changed (current page inventory; current audit-log trio capability; current CI hardening posture). The README is the first impression for GitHub visitors who land on the repo; staleness here costs adoption.

**Scope discipline (what is OUT):**

- **mkdocs user docs refresh** — separate slice 133 (filed as spillover).
- **In-app walkthrough refresh** — separate slice 134 (filed as spillover).
- **CLAUDE.md update** — handled in a separate non-slice doc PR per the `/idea-to-slice` skill rule that "meta-process changes (CLAUDE.md edits, prompt-file edits, CI workflow changes) go through normal PR flow, not the slice convention".
- **Logo redesign** — slices 074 + 075 + 077 already shipped the canonical logo set; this slice CONSUMES that logo, does NOT touch it.
- **Adding new pages to the screenshot set** — capture exactly the page inventory README currently references (hero dashboard + whatever else is referenced); a broader catalog is a follow-on if needed.
- **Animated GIFs or video** — out of scope; static PNG only (a11y + diff-friendly + load-time discipline).

## Threat model

| STRIDE                       | Threat                                                                                                                                                                                                                                              | Mitigation                                                                                                                                                                                                                                                                                                                    |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a — no auth surface added                                                                                                                                                                                                                         | n/a                                                                                                                                                                                                                                                                                                                           |
| **T** Tampering              | Screenshots captured against live customer data would leak PII / tenant identities forever (the README is publicly cached + indexed)                                                                                                                | Capture script MUST point at a sanitized demo seed (`web/e2e/seed.ts` or equivalent demo fixture). Real customer data is BLOCKED via P0-A2 below.                                                                                                                                                                             |
| **R** Repudiation            | n/a — read-only capture                                                                                                                                                                                                                             | n/a                                                                                                                                                                                                                                                                                                                           |
| **I** Information disclosure | **HIGH.** A screenshot inadvertently capturing a session cookie in browser dev tools, a bearer token in a URL param, an email, an IP address, an internal SCF ID, or a real customer name becomes a permanent public leak the moment the PR merges. | Capture pipeline MUST: (a) run against the slice-082 demo seed only; (b) close browser dev tools BEFORE every capture; (c) strip query-string params from any visible URL bar; (d) NEVER capture the network tab; (e) human review of every PNG byte-for-byte before commit. Anti-criteria P0-A1 through P0-A4 below enforce. |
| **D** DoS                    | Large PNGs slow `git clone` + README load. README screenshots stored as files (not git LFS) bloat repo size.                                                                                                                                        | Per-image cap: ≤ 200 KB optimized PNG (`pngquant` or equivalent). Hero ≤ 350 KB. Total README image budget: ≤ 2 MB.                                                                                                                                                                                                           |
| **E** Elevation of privilege | n/a                                                                                                                                                                                                                                                 | n/a                                                                                                                                                                                                                                                                                                                           |

**Verdict:** HAS-MITIGATIONS — the information-disclosure risk is real and load-bearing; the P0 anti-criteria below are the load-bearing mitigation. Implementing engineer MUST read P0-A1 through P0-A4 before capturing the first screenshot.

## Acceptance criteria

### Capture pipeline

- [ ] **AC-1:** New script `scripts/capture-readme-screenshots.sh` (or `web/scripts/`) drives Playwright against a local docker-compose bring-up, navigating to each README-referenced page and writing PNGs to `docs/images/`. The script is idempotent + re-runnable.
- [ ] **AC-2:** Script REFUSES to run unless `ATLAS_DEMO_SEED=1` is in the environment AND the database hostname resolves to a localhost / private-range address. Prevents accidental capture against a remote tenant.
- [ ] **AC-3:** Script invokes `web/e2e/seed.ts`'s demo-seed path (or a documented equivalent) so the captured UI is deterministic across runs. Page state is identical between two consecutive captures (sha256 of every PNG matches across two runs, modulo the time-of-day chrome that the capture script masks).
- [ ] **AC-4:** Each captured PNG is run through `pngquant` (or sips `--setProperty formatOptions` on macOS) to a size budget: ≤ 200 KB per page screenshot; hero ≤ 350 KB; total README image budget ≤ 2 MB.

### Pages captured

- [ ] **AC-5:** Re-capture the existing hero dashboard image (`docs/images/hero-dashboard.png` + `hero-dashboard-dark.png`) against v1.10.0+. Logo MUST render correctly on the login page (defends against the v1.9 regression that motivated this slice).
- [ ] **AC-6:** Re-capture every other page README currently references (audit dropdown, control browser, etc. — enumerate by grepping README.md for `./docs/images/`). One PNG per page; light + dark variants where the current README has both.

### README text refresh

- [ ] **AC-7:** Refresh the "Why security-atlas" / pitch section to reflect the operator-grade state: audit-log trio (124 + 125 + 126 + 129 + 130), CI hardening trilogy (117 + 127 + 128), 126 v1 slices merged.
- [ ] **AC-8:** Refresh the "Try it locally" / quick-start section if any of the docker-compose / `just` commands have changed since slice 037. Verify each command works against a fresh checkout.
- [ ] **AC-9:** Add a "What's new" subsection or update the existing one to reference the latest tagged release (v1.10.0+) + a one-line per-major-batch summary (audit-log trio, CI hardening trilogy).
- [ ] **AC-10:** Verify all internal links resolve: `Plans/ARCHITECTURE_CANVAS.md`, `docs/issues/_INDEX.md`, `docs/issues/_STATUS.md`, `SELF_HOSTING.md`, every `docs/images/<file>` referenced from README. CI link-checker (markdown-link-check or equivalent) passes.

### Manual review gate

- [ ] **AC-11:** Implementing engineer (or maintainer at review time) eyeballs every PNG before merge. Specifically scans for: dev-tools panels, session-cookie banners, real emails, real IP addresses, real customer names, internal SCF IDs above 5 digits, bearer tokens in any visible URL. Captures a yes/no audit in the slice's decisions log.

### Decisions log

- [ ] **AC-12:** Create `docs/audit-log/132-readme-refresh-decisions.md` recording: (D1) the chosen seed-fixture, (D2) per-image size results vs budget, (D3) which README sections changed and why, (D4) any screenshots that were considered but cut (and why).

## Constitutional invariants honored

- **#9 Manual evidence is first-class.** The screenshots ARE manual evidence of platform state at version v1.10.0+. The decisions log captures the provenance.
- **AI-assist boundary.** No AI-generated screenshots, no AI-generated README copy without explicit human approval (the maintainer reviews the slice PR).
- **No vendor data leakage** (constitutional but unnumbered): demo-seed-only capture preserves tenant isolation even at the documentation layer.

## Canvas references

- `Plans/canvas/01-vision.md` — the operator persona the README is selling to.
- `Plans/canvas/10-roadmap.md` — context for the "What's new" subsection (v1 complete; v2 in progress).
- `Plans/canvas/11-open-questions.md` item 20 (RESOLVED 2026-05-14): mkdocs Material is the docs-site choice — informs the README link to the docs site once slice 133 ships.

## Dependencies

- **#057** README screenshots (merged) — this slice's predecessor; the file structure (`docs/images/`) is inherited.
- **#123** Logo render fix (merged, `97e3eb4`) — load-bearing. Without it, screenshots of the login page would show the broken logo. The user's 2026-05-18 v1.9 bug report is the trigger for re-capturing.
- **#082** Playwright seed-data harness (merged) — the AC-3 demo-seed path reuses this.
- **v1.10.0 release tag** — should exist before capture begins. PR #278 (release-please's pending `chore(main): release 1.10.0`) needs to merge first. If still pending at slice pickup time, the engineer either (a) waits, or (b) captures against `main` post-`ba49891` and notes the explicit commit SHA in the decisions log.

## Anti-criteria (P0 — block merge)

- **P0-A1: NO real customer data.** Every screenshot MUST come from the demo seed. If the engineer cannot confirm the rendered data is demo-seed-origin, the screenshot is rejected. Implementing engineer documents per-screenshot provenance in the decisions log.
- **P0-A2: NO emails, NO IP addresses, NO bearer tokens, NO session cookies, NO real-org names.** Browser address bar visible in screenshots MUST show only `localhost:3000` paths with no query-string params containing tokens. Dev tools MUST be closed (cmd+opt+i toggle off) before every capture. Network tab MUST never be captured.
- **P0-A3: NO PII the demo seed itself contains.** Even the demo seed may have placeholder names like "Alice Johnson"; review each PNG to confirm the visible names are obviously synthetic (`Demo User`, `test@example.com`, etc.). If demo seed values look real, the seed gets fixed first (file as spillover; do NOT proceed with capture).
- **P0-A4: NO `git lfs` migration.** Stay under the 2 MB total README image budget. If a page screenshot cannot compress to ≤ 200 KB, crop it tighter or split it into two narrower images. Repo-bloat compounds across forks; the README must remain `git clone`-cheap.
- **P0-A5: NO scope creep into mkdocs or walkthrough content.** This slice refreshes README only. Touching `docs/getting-started/` or `docs/walkthroughs/` is out of scope (slice 133 + slice 134 own those).
- **P0-A6: NO CLAUDE.md edits in this PR.** CLAUDE.md gets a separate non-slice doc PR per the `/idea-to-slice` skill rule.
- **P0-A7: NO new logo work.** The canonical logo set is what slices 074/075/077 shipped; this slice consumes only.
- **P0-A8: NO vendor-prefixed test fixture tokens** if any test artifacts ship as part of the capture script (neutral `test-*` only).

## Skill mix (3-5)

- **`grill-with-docs`** — apply at engineer pickup to verify terminology + scope discipline against this slice doc + the predecessor slices (057, 058, 123).
- **Playwright (web/e2e)** — drive the capture pipeline against the running app.
- **`pngquant` or `sips`** — image optimization step to meet the size budget.
- **`markdown-link-check` (or equivalent)** — link validation step in AC-10.
- **Manual visual review** — irreducible human-in-the-loop gate for the information-disclosure threat (P0-A1 through P0-A3).

## Notes for the implementing agent

**Recommended capture environment.** Spin up the full self-host docker-compose stack against `main` (or v1.10.0+ release tag), seeded with `ATLAS_DEMO_SEED=1`. Capture from a fresh Chromium instance in a clean Playwright project (no extensions, no auto-fill, no saved sessions from prior dev work). Use Playwright's `page.screenshot({fullPage: false, clip: <viewport>})` with a fixed viewport (e.g. 1440×900) so screenshots are diff-friendly across runs.

**The v1.9 logo bug context.** A maintainer-reported 2026-05-18 issue triggered this slice: the login-page logo was broken on Unraid v1.9 deployments because `web/proxy.ts`'s matcher config excluded only `_next/static`, `_next/image`, and `favicon.ico` from the auth check — the directly-referenced `logo-light.svg` / `logo-dark.svg` (plus `og-image.png`, `twitter-card.png`, `icon-192.png`, `icon-512.png`, `apple-touch-icon.png`) fell through to the cookie check and got redirected to `/login` for unauthenticated browsers. Slice 123 (commit `97e3eb4`, merged 2026-05-18) fixed it by adding a `PUBLIC_STATIC_FILES` Set in `web/proxy.ts`. **Verify the fix is live before screenshot capture begins** by hitting `http://localhost:3000/logo-light.svg` on a fresh load and confirming it returns the SVG (not HTML).

**Information-disclosure paranoia.** Every screenshot the engineer commits is **public forever** the moment the PR merges. GitHub caches + CDNs index README assets aggressively; even a `git rm + force-push` does not retroactively scrub. Treat each PNG like a release-binary signing decision: review it twice, including a final pass right before merge.

**Predecessor-slice reading.** Before drafting the README text-refresh diff, read slice 057 (the original screenshots slice) to understand the structural convention this slice continues. Read slice 058 (user-docs scaffold) to know where the mkdocs site will land post-slice-133 (so the README can correctly link to it). Read slice 123 (the bug fix) to understand the specific defect the screenshots must not re-introduce.

**Spillover slices already filed.** Slice 133 (mkdocs user docs content refresh) + slice 134 (in-app walkthrough refresh) — both `not-ready` pending slice 132 to establish the screenshot-capture pipeline they will reuse. Do NOT bundle their content into this slice's PR.

**CLAUDE.md is intentionally NOT in this slice.** Per `/idea-to-slice` skill rule, CLAUDE.md updates go through normal PR flow, not the slice convention. The maintainer will file a separate plain doc-PR after this slice merges so the CLAUDE.md content can reference the freshly-captured screenshots.

**Grill output from the design phase (Phase 2).** Terminology: the canonical name is "walkthrough" (slices 027 + 070), NOT "showboat" (user's informal term). The README MAY reference the walkthrough; if so, use "walkthrough". Scope: bundled 4 surfaces (README + mkdocs + walkthrough + CLAUDE.md) → decomposed via AskUserQuestion into 3 slices (132 primary + 133 + 134 spillover) + 1 non-slice doc PR (CLAUDE.md). Already-built check: slice 057 IS the predecessor; this slice is explicitly a refresh.

**Threat-model context.** STRIDE Phase 3 surfaced the information-disclosure risk as the load-bearing concern. P0-A1 through P0-A4 are the mitigations; treat them as merge-blocking, not nice-to-have. If any of them cannot be satisfied within this slice's scope, file a spillover slice for the gap and proceed only on the safe subset.
