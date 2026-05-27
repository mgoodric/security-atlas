# 323 — README refresh: current release + accurate slice count + fresh screenshots

**Cluster:** Docs
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

`README.md` was last meaningfully refreshed on 2026-05-20 (mtime) as part
of slice 181 / 182 era work. Since then 6 releases have shipped (v1.11.0
through v1.16.0) and ~140 additional slices have merged, but the README
still claims:

- "current release is **v1.10.0** (2026-05-18)" — **stale by 6 releases**;
  actual latest is **v1.16.0** (2026-05-24, per `gh release list`).
- "120+ slices have shipped" — **stale**; canonical Status table now has
  ~250 merged rows with the highest merged slice at 312.
- The "What's new in v1.10.0" section frames the v1.10 release as current;
  it should either roll forward to v1.16.0 or restructure to point at the
  CHANGELOG for per-release notes.

Screenshots in `docs/images/` are dated **2026-05-14**, which **predates**:

- Slice 203 (dark-mode wiring fixes, 2026-05-22)
- Slice 277 (mobile-responsive baseline, 2026-05-24)
- Auth-substrate-v2 surfaces (slices 187-198) that changed login/admin flows
- Slice 210 (install-state tenant_id surface) that affects fresh-install
  first-screen experience

The hero dashboard + control-detail + audit-workspace + board-pack-preview
screenshots are the most visible operator-facing artifacts in the
public README. A new visitor sees stale UI before they see anything else.

**Disposition:** docs-only refresh with screenshot regen.

**Scope discipline.** This slice updates `README.md` + regenerates the
operator-facing screenshots in `docs/images/`. It does NOT:

- Touch `CHANGELOG.md` (release-please owns that)
- Touch the mkdocs site at `docs/` (slice 058's surface)
- Touch the architecture canvas at `Plans/canvas/*` (system-of-record;
  separate slice if it drifts)
- Touch mockups at `Plans/mockups/*` (iteration-1 reference, not production)
- Roll a new release tag (release-please owns that)

## Threat model

README is unauthenticated public-facing docs. STRIDE pass:

- **S (Spoofing):** No new auth surface. CLEAN.
- **T (Tampering):** README has no user-input flow. CLEAN.
- **R (Repudiation):** No audit-log surface. CLEAN.
- **I (Information disclosure):** Screenshots show the dev tenant by
  default. Risk: regenerated screenshots could accidentally include real
  customer data (vendor names, control IDs, evidence excerpts) if taken
  against a non-demo tenant. **Mitigation: regen screenshots against the
  demo seed tenant only** (the dataset slice 205 ships) AND visually
  verify no PII / customer-attributable strings before commit. Also: do
  not include internal IPs (`atlas-edge.home.gmoney.sh`), dev-only URLs,
  or local-host browser chrome in screenshots. Crop to the app frame.
- **D (Denial of service):** Static markdown; CLEAN.
- **E (Elevation of privilege):** Not an authz surface. CLEAN.

## Acceptance criteria

- [ ] **AC-1.** `README.md` "Project status" section reflects the actual
      latest release (v1.16.0 as of slice-filing; whatever `gh release
view --json tagName,publishedAt` returns at PR-time).
- [ ] **AC-2.** Slice count claim updated to a current accurate number
      (compute via `grep -cE "^\| [0-9]+ \|.*\\\`merged\\\`" docs/issues/\_STATUS.md`
      at PR time; round down to a marketing-friendly bucket like "250+").
- [ ] **AC-3.** "What's new in v<X>" section either: - (a) rolls forward to v1.16.0 with a punchy 3-5 bullet summary
      of the substantive v1.11→v1.16 additions, OR - (b) is replaced with a one-line pointer to `CHANGELOG.md`
      ("See [CHANGELOG.md] for per-release notes").
      Either path is fine; pick (a) if the engineer has clear narrative
      coverage of the new features (auth-substrate-v2, atlas-edge ops,
      MCP write-proposals, demo seed, mobile-responsive baseline,
      coverage audit rounds 1-3), pick (b) otherwise.
- [ ] **AC-4.** All 4 operator-facing hero screenshots regenerated: - `docs/images/hero-dashboard.png` (+ dark) - `docs/images/control-detail.png` (+ dark) - `docs/images/audit-workspace.png` (+ dark) - `docs/images/board-pack-preview.png` (+ dark)
- [ ] **AC-5.** Screenshots taken against the **demo seed tenant**
      (slice 205 dataset) — visually verified by the engineer that no
      real-tenant data is present.
- [ ] **AC-6.** Screenshot capture method documented in
      `docs/audit-log/323-readme-refresh-decisions.md` (browser used,
      viewport size, light/dark capture sequence, any cropping) so
      future refreshes are reproducible.
- [ ] **AC-7.** README's references to in-flight v2 work that has since
      landed (the unified audit-log trio, CI hardening trilogy) are
      restructured to past-tense / "shipped in v1.X". Anything still
      in-flight stays in-flight.
- [ ] **AC-8.** Link checks: every relative link in README still
      resolves to an existing file. Run `markdown-link-check` or a
      simple `grep -oE '\([./][^)]+\.md\)' README.md | sort -u` + verify each path exists.
- [ ] **AC-9.** `pre-commit run --files README.md` passes (prettier
      reformatting accepted).

## Constitutional invariants honored

- **Manual evidence is first-class (canvas §4.5):** the README is the
  first thing a self-hosting operator reads; staleness here is the
  highest-leverage trust signal.
- **Survive a third-party security review (canvas §6):** a stale
  README erodes the credibility of the diligence-the-diligence-tool
  thesis even before a reviewer opens the codebase.
- **No marketing-y framing (CLAUDE.md AI-assist boundary tone rules):**
  the "What's new" copy stays measured, factual, slightly defensive.
  Banned phrases ("proud to report", "industry-leading", "robust",
  unprompted superlatives) apply to the README as much as to board
  narratives.

## Canvas references

- `Plans/canvas/01-vision.md` §6 (survive a third-party security review)
- `Plans/canvas/10-roadmap.md` (current v1/v2 framing)

## Dependencies

None. All prior work referenced by the refresh is already merged.

## Anti-criteria (P0 — block merge)

- **P0-323-1.** Does NOT include real customer / tenant data in the
  regenerated screenshots. Demo-seed tenant only; visually verify
  before commit.
- **P0-323-2.** Does NOT include internal infrastructure URLs in
  screenshots (`atlas-edge.home.gmoney.sh`, internal IPs, dev-only
  browser chrome, OS-specific window decorations that leak local
  hostnames).
- **P0-323-3.** Does NOT touch `CHANGELOG.md` — release-please owns it.
- **P0-323-4.** Does NOT touch `Plans/canvas/*` from inside this slice.
  Canvas drift is a separate slice if surfaced.
- **P0-323-5.** Does NOT add marketing-y framing. Tone discipline from
  the CLAUDE.md board-narrative banned-phrase list applies.
- **P0-323-6.** Does NOT modify `_INDEX.md` — orchestrator's surface.
- **P0-323-7.** Does NOT auto-merge — the maintainer reviews the
  screenshots + copy before merge (the screenshot verification is
  fundamentally a human-eyes step that AI-assist cannot ratify).

## Skill mix

- **Markdown editing:** measured-tone copy, accurate version refs,
  link verification
- **Web operation:** spin up local dev server, sign in to demo tenant,
  navigate to each surface
- **Screenshot capture:** Playwright via the existing `web/e2e-audit/`
  harness OR manual browser capture; either way crop to the app frame
- **Slice 050 reference** (`docs/issues/050-public-release-readiness.md`)
  — the slice that established the current README shape; refresh stays
  consistent with that template

## Notes for the implementing agent

**Reproduce screenshots reproducibly.** The audit-log entry at
`docs/audit-log/323-readme-refresh-decisions.md` must capture:

- Browser + version
- Viewport size (suggested: 1440 × 900 for desktop hero; the existing
  screenshots look ~1440px wide)
- Light + dark capture order (capture light first, toggle theme, capture
  dark from same scroll position)
- Tenant used (must be demo seed)
- Any cropping applied (border, browser chrome removal)

**Demo seed prerequisite.** Slice 205 ships a demo seed dataset that
populates the demo tenant with controls / risks / evidence / policies
/ vendors / questionnaires. Verify the seed has been loaded before
capture (`POST /v1/admin/demo/seed` against a local install). The
dashboard + control-detail + audit-workspace + board-pack-preview
surfaces all depend on this seed.

**Existing screenshots for visual diff.** `docs/images/hero-dashboard.png`

- `-dark.png` are the highest-priority refreshes (the README hero).
  Before regenerating, view the existing image to understand framing +
  crop. The new screenshot should preserve framing — same surface,
  fresher state.

**"What's new" copy options.** If you go with AC-3 path (a) — roll
forward to v1.16.0 — the substantive v1.11→v1.16 themes are:

- **Auth-substrate-v2** (OAuth AS scaffolding per ADR-0003; slices
  187-198) — JWT-with-tenant-claim sessions; super-admin management;
  multi-tenant login + switcher; LocalLogin first-install flow
- **Atlas-edge operability** (slices 207-211) — per-commit images,
  Watchtower update path, install-state surface
- **Mobile-responsive baseline** (slice 277) — viewport meta + sidebar
  drawer + per-page audit
- **Coverage discipline** (slices 069 + 279 + 305 + 312) — monotonic
  ratchet contract; ~73 packages at-target merged ≥70%
- **MCP write-proposals stack** (slices ~199-205) — AI-assist
  foundation primitives

Keep each bullet 1-2 sentences, factual, no superlatives. Mirror the
"What's new in v1.10.0" voice that the README currently uses.

**Link discipline.** Existing relative links in README that may have
moved:

- `./Plans/ARCHITECTURE_CANVAS.md` — still exists
- `./docs/issues/_INDEX.md` — still exists
- `./docs/issues/_STATUS.md` — still exists
- `./CHANGELOG.md` — still exists
- `./LICENSE` — still exists
- `./docs/RELEASE_READINESS.md` — verify still exists at this path
- `./docs/audit-log/*` — verify referenced audit logs still exist
- Any anchor links (`./README.md#section`) — verify the anchor still
  matches the heading

**Spillover discipline.** If during the refresh you find:

- A broken external link → file as a 0.25d hotfix slice (next slot)
- A misclaim about features that don't actually work as described →
  surface immediately; that's a load-bearing finding, NOT a refresh
  problem. The refresh assumes the README's substantive claims are
  accurate; a broken claim is a documentation-vs-product drift bug.
- A canvas section that's drifted from the merged code → file a
  separate canvas-refresh slice at the next slot.

Do NOT bundle spillover findings into this PR.
