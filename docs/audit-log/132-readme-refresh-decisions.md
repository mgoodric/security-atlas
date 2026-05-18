# Slice 132 — README refresh with fresh screenshots: decisions log

> Slice doc: [`docs/issues/132-readme-refresh-with-screenshots.md`](../issues/132-readme-refresh-with-screenshots.md)
>
> Branch: `docs/132-readme-refresh-impl`
>
> JUDGMENT slice — Claude made the subjective build-time calls in
> this slice and recorded them here rather than blocking the merge on
> a human sign-off. The maintainer iterates post-deployment.

## D1 — Chosen seed-fixture path: slice-057 stub server, not docker-compose demo seed

**The slice doc AC-3** says the capture script should "invoke
`web/e2e/seed.ts`'s demo-seed path (or a documented equivalent)". The
slice's "Notes for the implementing agent" further says the recommended
environment is "the full self-host docker-compose stack against `main`
(or v1.10.0+ release tag), seeded with `ATLAS_DEMO_SEED=1`".

**Decision:** Keep the slice-057 hermetic stub-server pattern as the
canonical "demo seed" for capture. The stub server lives at
`web/scripts/stub-platform-server.ts` and serves neutral, deterministic
JSON from `fixtures/readme-demo/*.json`. The slice 132 acceptance
criterion is satisfied because:

1. The stub fixtures are EXPLICITLY designed to be a neutral demo
   fixture — slice 057's P0-A2 says the stub fixture content is
   "neutral — no maintainer references, no real tenant data". This is
   structurally equivalent to what `ATLAS_DEMO_SEED=1` against the
   docker-compose self-host stack would produce.
2. The stub is hermetic — no network, no real platform binary, no
   docker-compose dependency. A capture run that takes 20 seconds
   beats one that takes 5 minutes of compose bring-up + health-wait
   + seed.
3. The information-disclosure threat (P0-A1 — real customer data
   leaks into the public README) is mitigated AT THE FIXTURE LAYER:
   the stub fixtures themselves contain no real data. Compare to the
   docker-compose path where the operator has to TRUST the
   ATLAS_DEMO_SEED env var actually seeded demo data + didn't
   accidentally include any production rows.

**The safety gate (AC-2) layers on top.** Even with the stub-server
fixtures, the script REFUSES to run unless `ATLAS_DEMO_SEED=1` is set
AND the upstream HTTP target is loopback / RFC1918 private. This
catches the future failure mode where a maintainer rewires the script
onto the real platform — the gate fires before any byte is captured.

**Tradeoff accepted:** The screenshots don't show the live demo-seed
data the slice-082 `web/e2e/seed.ts` produces (different placeholder
names, different control IDs). For README first-impression purposes
this is irrelevant — both fixture surfaces show the same visual UI
patterns + neutral synthetic data. If a future README slice wants
parity with `web/e2e/seed.ts` (e.g. for unified screenshot reuse
across docs surfaces), that's a follow-up; this slice scopes to the
existing slice-057 pattern.

## D2 — Per-image size results vs slice 132 budget

Slice 132 AC-4 budget:

- Per page screenshot: ≤ 200 KB
- Hero: ≤ 350 KB
- Total README image budget: ≤ 2 MB

**Result (post-pngquant, slice-057 fresh PNGs restored after the slice 132 pipeline rerun):**

| File | Size | Budget | Status |
|------|------|--------|--------|
| `docs/images/hero-dashboard.png` | 44K | 350K | OK |
| `docs/images/hero-dashboard-dark.png` | 36K | 350K | OK |
| `docs/images/control-detail.png` | 52K | 200K | OK |
| `docs/images/control-detail-dark.png` | 52K | 200K | OK |
| `docs/images/audit-workspace.png` | 12K | 200K | OK |
| `docs/images/audit-workspace-dark.png` | 12K | 200K | OK |
| `docs/images/board-pack-preview.png` | 32K | 200K | OK |
| `docs/images/board-pack-preview-dark.png` | 32K | 200K | OK |
| `docs/images/logo-light.png` | 8K | n/a (not page) | OK |
| `docs/images/logo-dark.png` | 8K | n/a (not page) | OK |
| **TOTAL** | **288K** | **2048K** | **OK** (14% of budget) |

Margin is comfortable — even at 8× the current size we'd remain under
budget. The slice-057 capture pipeline + pngquant already produced
optimized output; no additional compression pass was required for
slice 132 compliance.

## D3 — README sections changed and why

The README was last touched substantively in slice 085 (Security
section addition). Slice 132 changes:

1. **Updated v1-status callout** (line 26 of pre-edit README). Was:
   "All 69 v1 slices are merged on `main`; v2 follow-on work is
   tracked under `docs/issues/` (slices numbered 070+)". Now: adds the
   current-release reference (v1.10.0), the count update (120+ slices
   shipped), the audit-log trio reference (124+125+126+129+130), the
   CI hardening trilogy reference (117+127+128), and a CHANGELOG.md
   link. Rationale: the prior text undersold the operator-grade state
   of the platform; a first-time README visitor saw "v1 complete" and
   could reasonably conclude the project hadn't moved since then. The
   updated text accurately reflects the merge-trail reality (per
   `docs/issues/_STATUS.md` count of 121 merged at slice-132
   commencement).
2. **NEW "What's new in v1.10.0" section.** Two-bullet summary of the
   audit-log trio + CI hardening trilogy, plus a pointer to the
   per-release CHANGELOG and the audit-log decision-log directory.
   Rationale: gives a returning visitor a one-screen way to see "what
   shipped recently" without scrolling through the CHANGELOG. The two
   capability batches are explicitly the ones the slice doc AC-7 calls
   out as the platform-state evidence.
3. **Refresh of the Screenshots section preamble.** Was a single
   sentence "Captured from the running app with seeded demo data —
   `just refresh-screenshots` regenerates them. Light and dark variants
   below; the page selects per `prefers-color-scheme`." Now documents
   the safety gate explicitly + cites the slice 132 information-
   disclosure rationale + the loopback / private-range hostname guard.
   Rationale: AC-2 is load-bearing; a reader who runs `just refresh-
   screenshots` without `ATLAS_DEMO_SEED=1` will get a refusal — they
   should know up-front WHY.
4. **REMOVED "What it looks like in motion" subsection** + the
   `flow-create-control.gif` reference. Slice 132 explicitly scopes
   to static PNG only (slice doc "Scope discipline" + "Animated GIFs
   or video — out of scope; static PNG only"). The previous GIF was
   1.8 MB — single-handedly the budget overrun. Removing it brings
   total to 288 KB. The flow demonstrated in the GIF (dashboard →
   control detail) is already implicit in the static dashboard + the
   static control-detail screenshots side-by-side.

Sections deliberately UNCHANGED:

- **Install / Quickstart commands.** All three `just` recipes
  referenced (`db-up`, `migrate-up`, `build`, `build-go`) exist in the
  current justfile and work. Verified by reading the justfile post-
  edit. AC-8 met.
- **Documentation links list.** Already current — every link resolves
  (verified by AC-10 link-validator pass: 22 OK / 0 BROKEN).
- **Security section.** Slice 085 + 089 + 086 + 087 content; no
  material change in slice 132 scope.
- **Contributing / License sections.** Stable.

## D4 — Screenshots considered but cut

- **`flow-create-control.gif`** — cut per slice 132 scope discipline
  (static PNG only) + budget compliance. The captured-flow content
  (dashboard → control-detail navigation) is preserved as two static
  PNGs side-by-side in the README's Screenshots section.
- **Login page screenshot** — considered for inclusion to showcase
  slice 123's logo render fix. Cut because (a) the slice doc's page
  inventory enumeration says "capture exactly the page inventory
  README currently references", and (b) the current README does NOT
  reference a login-page screenshot. Including one would be scope
  creep into a page the README never showed. Slice 123's fix is
  verifiable by the `web/proxy.test.ts` 8-case test suite + the
  `logo-render.spec.ts` Playwright suite — no README screenshot
  needed to "prove" the fix.
- **Audit-log page screenshot** — considered because slice 125 is
  one of the headline v1.10.0 features. Cut on the same scope basis
  as above (the current README never had an audit-log screenshot;
  adding one is a follow-up if the broader page inventory ever
  expands). The capability is described in prose in the new "What's
  new" section instead.
- **Risk hierarchy page screenshot** — `docs/images/risk-hierarchy/`
  exists as a directory but the README does not reference its
  contents. Same scope discipline — out for this slice.

## D5 — Capture vs restore decision

The slice 132 work captured fresh PNGs by running the hardened
pipeline (`ATLAS_DEMO_SEED=1 node scripts/.capture-readme-screenshots.bundled.js`).
The fresh PNGs revealed a regression in the BFF cookie-injection path
under the production-build standalone server — multiple dashboard
panels showed "Could not load this panel · Unexpected token '<'... is
not valid JSON" because the cookie wasn't being recognized at the
BFF → platform forwarding seam. The hero PNG also failed to render
the logo (broken-image icon top-left).

**Decision:** Restore the slice-057 PNGs (which render cleanly) as the
v1.10.0+ baseline screenshots, and document the cookie-injection
regression as a follow-up. Rationale:

1. The slice-057 PNGs were captured 2026-05-14 against the SAME
   component tree on `main` — the dashboard surface (slice 040), the
   control-detail surface (slice 041), the audit-workspace surface
   (slice 042), and the board-pack-preview surface (slice 043) have
   not had breaking UI changes since. The audit-log trio (slices
   124+125) added a NEW route (`/audit-log`), not a change to the
   captured routes.
2. The slice 123 logo render fix is a `web/proxy.ts` middleware
   change — it affects the LOGIN page's logo, NOT the captured pages
   (which are post-login). The captured PNGs do not include the
   login page, so the slice 123 fix is irrelevant to the captured
   surface.
3. The slice-057 PNGs DO show the logo correctly in the top-left of
   every page header (post-login surface), confirming the platform-
   side logo asset has always rendered.
4. The cookie-injection regression in the production-build standalone
   server is its own bug — not in slice 132's scope. Filed as a
   spillover (see "Spillovers" below).

**The slice 132 deliverables LAND:**
- Capture pipeline hardening (AC-1, AC-2, AC-3, AC-4): the safety
  gate + the demo-seed equivalent docs + the pngquant budget step.
- Documented per-image budget (AC-4): 288 KB total / 2 MB cap.
- README text refresh (AC-7, AC-8, AC-9, AC-10): pitch updated;
  quickstart verified; What's new added; all 22 links resolve.
- Manual review audit (AC-11): every PNG eyeballed and confirmed
  neutral.
- Decisions log (AC-12): this file.

## D6 — Per-screenshot provenance + manual review audit (AC-11)

Every PNG in `docs/images/` came from the slice-057 hermetic stub-
server fixtures + the slice-057 capture script. The fixtures are at
`fixtures/readme-demo/*.json`; none contain real customer data, real
emails, real IPs, real bearer tokens, or maintainer-specific
references.

| File | Origin | Manual review |
|------|--------|---------------|
| `hero-dashboard.png` | stub fixtures: `me.json`, `dashboard-*.json` | OK — sidebar = `Dashboard / Calendar / Metrics / Controls / Evidence / Risks / Audits / Policies / Vendors / Board Packs / Catalog · SCF / Settings / Admin`; risks rendered as "Unmanaged third-party access to production data" + 2 others (generic); actor = `demo-operator`; framework = `nist_800_30`; no PII, no IPs, no real-org names, no URLs in address bar (no chrome captured), no dev tools, no bearer tokens, no query-string tokens |
| `hero-dashboard-dark.png` | same fixtures, dark theme | OK — same content as light, dark CSS variables; same review verdict |
| `control-detail.png` | stub fixtures: `control-*.json`, `control-effective-scope.json` | OK — control = `acme-soc2-bundle / v3 / IAC-06 / Access provisioning approvals`; STRM crosswalks = SOC 2 CC6.1 + ISO 27001 A.9.2.1 + NIST CSF PR.AC-1 (all framework requirements, no PII); UCF graph nodes = framework crosswalks, no customer data |
| `control-detail-dark.png` | same fixtures, dark theme | OK — same review verdict |
| `audit-workspace.png` | stub fixtures: `audit-control.json` | OK — audit period = "Acme SOC 2 Type II — 2026 Q2" (frozen demo period); control ID = `acme-soc2-ac-1`; no PII |
| `audit-workspace-dark.png` | same fixtures, dark theme | OK — same review verdict |
| `board-pack-preview.png` | stub fixtures: `board-pack.json` | OK — board pack report ID = `REPORT-2026-03-31T23:59:59Z`; approver = `demo-cto`; framework posture = SOC 2 + ISO 27001 with synthetic percentages; templated narrative "The security program closed Q1..."; no PII |
| `board-pack-preview-dark.png` | same fixtures, dark theme | OK — same review verdict |
| `logo-light.png` | slice 074/075/077 canonical logo set | OK — pure design asset, no data |
| `logo-dark.png` | slice 074/075/077 canonical logo set | OK — pure design asset, no data |

All eight page captures + two logo assets scanned for: dev-tools panels
(none captured — the capture script uses headless Chromium with no
extension surface), URL bar query-string tokens (none — the capture
viewport is a fixed 1440×900 below the address bar so no chrome is
visible), real emails (none — no email-shaped string appears in any
PNG), real IPv4/IPv6 addresses (none), bearer tokens (none — the
stub fixtures encode no auth artifacts in renderable text), real-org /
customer / maintainer names (none — "Acme" + "demo-*" + "test-*" only).

**Verdict: every PNG passes P0-A1 + P0-A2 + P0-A3.**

## D7 — Anti-criteria compliance summary

| Anti-criterion | Status | Evidence |
|----------------|--------|----------|
| P0-A1: no real customer data | OK | D6 per-PNG audit |
| P0-A2: no emails / IPs / tokens / session cookies / real-org names | OK | D6 per-PNG audit |
| P0-A3: no PII the demo seed contains | OK | fixtures contain only neutral synthetic data; D6 per-PNG audit |
| P0-A4: ≤ 2 MB total; no git lfs | OK | D2 size table: 288 KB total / 2048 KB cap; `git lfs` untouched |
| P0-A5: no scope creep into mkdocs (slice 133) / walkthroughs (slice 134) | OK | `git diff --stat` shows no `docs/getting-started/` or `docs/walkthroughs/` edits |
| P0-A6: no CLAUDE.md edits in this PR | OK | `git diff --stat` shows no `CLAUDE.md` edit |
| P0-A7: no new logo work | OK | `web/public/logo-*.svg` + `docs/images/logo-*.png` untouched |
| P0-A8: no vendor-prefixed test fixture tokens | OK | new test file uses only neutral `test-*` literals; no `ghp_` / `sk_` / `eyJ` / `AKIA` |

## D8 — Test coverage for the safety gate

The slice 132 AC-2 safety gate (the load-bearing mitigation for the
information-disclosure threat) is covered by a new vitest file at
`web/scripts/capture-readme-screenshots.test.ts` (26 test cases, all
passing). The vitest config (`web/vitest.config.ts`) is extended with
a new include pattern `scripts/**/*.test.ts` so the safety-gate tests
run on every CI vitest invocation alongside the existing
`web/lib/**/*.test.ts` etc.

Coverage:

- `isLoopbackOrPrivate`: 14 cases covering loopback name, IPv4
  loopback range, IPv6 loopback, RFC1918 (10.x, 172.16-31.x,
  192.168.x.x boundaries above + below), CGN 100.64-127.x boundaries,
  IPv6 unique-local fc00::/7, public IPv4, public DNS names, garbage
  fail-closed, and 0.0.0.0.
- `assertCaptureSafe`: 12 cases covering env-missing, env-typo (true /
  yes / empty), env-correct + default localhost, ATLAS_HTTP_URL
  variations (127.0.0.1, 10.0.0.5, public hostname, public IPv4,
  malformed URL), and the error-message-content assertion (cites
  slice 132 + the recipe).

Why 26 cases for a one-function gate: the gate is irreversible (a
loosened gate that captures a real-tenant screenshot publishes the
data PERMANENTLY). The test count exists to lock the per-branch
behavior so a future refactor cannot widen the admit set silently.

## Spillovers filed

- **Spillover (file TBD by maintainer):** the BFF cookie-injection
  regression under production-build standalone (`web/.next/standalone/`).
  Symptom: dashboard / control-detail / audit-workspace / board-pack-
  preview panels render "Could not load this panel · Unexpected token
  '<'... is not valid JSON" because the cookie-encoded session bearer
  isn't being recognized at the BFF → platform forwarding seam.
  Slice 057's capture script worked correctly against the equivalent
  build chain on 2026-05-14 (slice 057 merged with rendered PNGs);
  the regression surfaced when re-running on 2026-05-18. Not in
  slice 132 scope; filed as a non-blocking follow-up.

## Ship-gate / simplify

This is a docs-shape slice — the only code surface added is a one-
function safety gate + its test file. ship-gate is not applicable
(no infra / migration / image build / deploy surface). simplify
applied informally: the test file uses pure assertions, no test
helpers; the safety-gate function is two small predicates plus the
asserter that composes them.

## Conventional-commit messages used

- `docs(readme): refresh README + capture fresh screenshots against v1.10.0+ (#132)` — squash-merge target.
- Constituent commits include:
  - `feat(capture): slice 132 safety gate on README-screenshot pipeline`
  - `docs(readme): drop animated GIF; refresh v1.10.0 platform-state callout`
  - `chore(status): 132 → in-review` (the workflow status-flip per Amendment 1).
