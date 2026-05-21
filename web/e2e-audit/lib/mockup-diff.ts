// Slice 178 — mockup-diff categorization module (AC-4, AC-11).
//
// Takes (a) a live-route fingerprint captured by the harness against the
// running web app, and (b) the manifest entry for that route, and
// produces a categorized diff:
//
//   SHIP-GAP      — feature in the mockup that is NOT in the live UI.
//                   This is fine for in-flight slices; flag only if no
//                   open slice exists. (The harness can't read the
//                   issue tracker from inside the browser; the report
//                   includes a "suggested action" pointer for the
//                   reviewer to cross-check.)
//
//   HONESTY-GAP   — element in the live UI that is NEITHER in the
//                   manifest's `expectedTestIds` NOR its
//                   `allowedExtraTestIds`. This is the anti-pattern the
//                   slice catches: forward-looking placeholders without
//                   backing functionality.
//
//   MOCKUP-STALE  — element in the manifest's `staleMockupTestIds`
//                   that the slice owner has marked as
//                   indefinitely-deferred. The mockup describes a
//                   feature we will not ship; the mockup should be
//                   updated.
//
// The diff is deterministic: findings are sorted by (category,
// route, testidOrSelector) so the same inputs always produce the same
// JSON / markdown bytes (slice 178 AC-6 — stable diffs).

export type FindingCategory = "SHIP-GAP" | "HONESTY-GAP" | "MOCKUP-STALE";

export type LiveFingerprint = {
  /** Route path, e.g. `/dashboard`. */
  route: string;
  /** Sorted, deduplicated `[data-testid]` values present on the page. */
  testIds: string[];
  /**
   * Anchors whose `href` is a forward-looking dead link — either a
   * literal `#` (no destination), an unresolved internal route, or an
   * element flagged by an AC-5 heuristic (see `heuristics.ts`).
   */
  deadAnchors: Array<{ href: string; text: string }>;
  /** Buttons whose label/aria-label is a "coming soon" placeholder. */
  comingSoonButtons: Array<{ text: string; ariaLabel: string | null }>;
  /** Elements whose `data-feature-flag` references an unset flag. */
  unsetFeatureFlags: Array<{ flag: string; selector: string }>;
};

export type ManifestEntry = {
  route: string;
  /**
   * Relative path under `Plans/mockups/` (e.g. `dashboard.html`). The
   * file MUST exist — the manifest's JSON Schema check catches missing
   * files (P0-178-11). `null` is permitted for routes without a backing
   * mockup (e.g. `/calendar`, which has no `calendar.html` mockup); for
   * those routes the diff skips the SHIP-GAP / MOCKUP-STALE check and
   * runs only the HONESTY-GAP heuristics.
   */
  mockupPath: string | null;
  /** TestIds the manifest expects to see on the live page. */
  expectedTestIds: string[];
  /**
   * TestIds the live page is allowed to have without being flagged as
   * a HONESTY-GAP. Use for shared chrome (sidebar nav, topbar) and for
   * components that are honestly post-mockup additions.
   */
  allowedExtraTestIds: string[];
  /**
   * TestIds in the mockup that the slice owner has marked as
   * indefinitely deferred. The diff emits a MOCKUP-STALE finding so
   * the mockup gets updated rather than the live UI getting a
   * placeholder.
   */
  staleMockupTestIds?: string[];
  /**
   * Human-readable note about the route's mockup parity status. Surfaced
   * in the report so a reviewer reading a finding has context.
   */
  notes?: string;
};

export type Finding = {
  route: string;
  category: FindingCategory;
  /**
   * The element that caused the finding — usually a `data-testid`, but
   * for the AC-5 heuristics it can be a selector string (e.g.
   * `a[href="#"]`) or a flag name.
   */
  subject: string;
  mockupPath: string | null;
  /** Where the reviewer should look. Human-friendly. */
  suggestedAction: string;
  /** Auxiliary context (e.g. anchor text, button label). */
  details?: string;
};

/**
 * Compute findings for a single route.
 *
 * Pure function — no I/O. Determinism is enforced by `sortFindings`.
 */
export function diffRoute(
  live: LiveFingerprint,
  manifest: ManifestEntry,
): Finding[] {
  const findings: Finding[] = [];

  const expected = new Set(manifest.expectedTestIds);
  const allowed = new Set(manifest.allowedExtraTestIds);
  const stale = new Set(manifest.staleMockupTestIds ?? []);
  const liveSet = new Set(live.testIds);

  // 1. SHIP-GAP — expected testids missing from live (skip for
  //    no-mockup routes).
  if (manifest.mockupPath !== null) {
    for (const tid of manifest.expectedTestIds) {
      if (stale.has(tid)) continue; // marked stale; falls to MOCKUP-STALE
      if (!liveSet.has(tid)) {
        findings.push({
          route: manifest.route,
          category: "SHIP-GAP",
          subject: tid,
          mockupPath: manifest.mockupPath,
          suggestedAction:
            "Cross-check `docs/issues/_INDEX.md` for an open slice covering this element. " +
            "If a slice is in flight, no action. If not, file a spillover slice via `/idea-to-slice`.",
        });
      }
    }
  }

  // 2. MOCKUP-STALE — staleMockupTestIds the manifest declared.
  if (manifest.mockupPath !== null) {
    for (const tid of manifest.staleMockupTestIds ?? []) {
      findings.push({
        route: manifest.route,
        category: "MOCKUP-STALE",
        subject: tid,
        mockupPath: manifest.mockupPath,
        suggestedAction:
          `Update \`Plans/mockups/${manifest.mockupPath}\` to remove or annotate the stale element. ` +
          "If the maintainer reconsiders shipping this feature, revert this manifest entry.",
      });
    }
  }

  // 3. HONESTY-GAP — testids in live that are neither expected nor
  //    allowed. This is the load-bearing signal of the audit.
  for (const tid of live.testIds) {
    if (expected.has(tid)) continue;
    if (allowed.has(tid)) continue;
    findings.push({
      route: manifest.route,
      category: "HONESTY-GAP",
      subject: tid,
      mockupPath: manifest.mockupPath,
      suggestedAction:
        "Either: (a) the element backs a feature we shipped after the mockup — add it to `allowedExtraTestIds`; " +
        "or (b) the element is a forward-looking placeholder — file a spillover slice to remove it OR ship the backing feature.",
    });
  }

  // 4. HONESTY-GAP — dead anchors (AC-5a).
  for (const a of live.deadAnchors) {
    findings.push({
      route: manifest.route,
      category: "HONESTY-GAP",
      subject: `a[href="${a.href}"]`,
      mockupPath: manifest.mockupPath,
      details: `text="${a.text}"`,
      suggestedAction:
        "Dead anchor — the href resolves to a 404 or `#`. Either remove the link or ship the destination route.",
    });
  }

  // 5. HONESTY-GAP — "coming soon" buttons (AC-5b).
  for (const b of live.comingSoonButtons) {
    findings.push({
      route: manifest.route,
      category: "HONESTY-GAP",
      subject: `button[aria-label*="coming soon"]`,
      mockupPath: manifest.mockupPath,
      details: `text="${b.text}", aria-label="${b.ariaLabel ?? ""}"`,
      suggestedAction:
        '"Coming soon" button — remove until the backing feature ships, OR add the testid to `allowedExtraTestIds` with a slice link in `notes`.',
    });
  }

  // 6. HONESTY-GAP — unset feature flags (AC-5c).
  for (const f of live.unsetFeatureFlags) {
    findings.push({
      route: manifest.route,
      category: "HONESTY-GAP",
      subject: `[data-feature-flag="${f.flag}"]`,
      mockupPath: manifest.mockupPath,
      details: f.selector,
      suggestedAction:
        "Element references a feature flag not in the current flag set. Either remove the element or enable the flag in the seeded data.",
    });
  }

  return sortFindings(findings);
}

/** Deterministic sort: category, route, subject. */
export function sortFindings(findings: Finding[]): Finding[] {
  const order: Record<FindingCategory, number> = {
    "HONESTY-GAP": 0,
    "SHIP-GAP": 1,
    "MOCKUP-STALE": 2,
  };
  return [...findings].sort((a, b) => {
    if (order[a.category] !== order[b.category]) {
      return order[a.category] - order[b.category];
    }
    if (a.route !== b.route) return a.route.localeCompare(b.route);
    return a.subject.localeCompare(b.subject);
  });
}

/** Aggregate counts for the report header. */
export function summarize(findings: Finding[]): {
  total: number;
  shipGap: number;
  honestyGap: number;
  mockupStale: number;
  byRoute: Record<string, number>;
} {
  const byRoute: Record<string, number> = {};
  let shipGap = 0;
  let honestyGap = 0;
  let mockupStale = 0;
  for (const f of findings) {
    byRoute[f.route] = (byRoute[f.route] ?? 0) + 1;
    if (f.category === "SHIP-GAP") shipGap++;
    else if (f.category === "HONESTY-GAP") honestyGap++;
    else mockupStale++;
  }
  return {
    total: findings.length,
    shipGap,
    honestyGap,
    mockupStale,
    byRoute,
  };
}
