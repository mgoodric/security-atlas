// Slice 223 — pure helper for deriving the visible page-name segment
// of the shared-shell breadcrumb (`<tenant> > <page>`).
//
// Why a pure helper: the consuming `<Breadcrumb />` is a server
// component (reads /v1/me/tenants via the cookie jar, parallels the
// slice 186 sidebar pattern) and is hostile to a vitest unit test. The
// route-to-name table is extracted here so the AC-3 + AC-4 obligation
// (table-driven cases for known routes + a humanizing fallback for
// unknown ones) can be pinned in isolation. Same split slice 213's
// `display-name.ts` settled on.
//
// AC-3 (spillover 271): "Page-name derivation is centralized in a
// single helper (`web/lib/page-names.ts`) keyed by URL prefix.
// Unrecognized routes fall back to humanizing the first segment."
//
// The map is intentionally keyed on the FIRST URL segment so detail
// pages and subroutes roll up to the section name — operators on
// `/controls/<scf_id>` see `Controls` in the breadcrumb, not the raw
// UUID. Two-segment routes (`/dashboards/metrics`, `/catalog/scf`)
// are handled by explicit prefix entries; the order matters because
// `splitFirst` returns the first segment only, so `/catalog/scf` and
// `/catalog/somethingelse` both resolve to `Catalog`.

const PAGE_NAMES: Record<string, string> = {
  // Cross-business cluster
  dashboard: "Dashboard",
  calendar: "Calendar",
  dashboards: "Metrics", // /dashboards/metrics
  activity: "Activity",
  // Core five
  controls: "Controls",
  evidence: "Evidence",
  risks: "Risks",
  audits: "Audits",
  policies: "Policies",
  vendors: "Vendors",
  // Reference + ops
  "board-packs": "Board Packs",
  catalog: "Catalog",
  settings: "Settings",
  admin: "Admin",
  "audit-log": "Audit log",
};

/**
 * derivePageName returns the user-visible page name for a Next.js
 * pathname. The pathname is normalized (query string stripped, hash
 * stripped, leading/trailing slashes trimmed). If the first URL
 * segment matches a known entry in PAGE_NAMES, that label is returned.
 * Otherwise the first segment is humanized — kebab-case becomes
 * sentence-case ("some-new-thing" → "Some new thing").
 *
 * Root path ("/") and empty path return "".
 */
export function derivePageName(pathname: string): string {
  if (!pathname) return "";
  const cleaned = pathname.split("?")[0].split("#")[0];
  const trimmed = cleaned.replace(/^\/+|\/+$/g, "");
  if (trimmed.length === 0) return "";
  const first = trimmed.split("/")[0];
  if (first in PAGE_NAMES) {
    return PAGE_NAMES[first];
  }
  return humanize(first);
}

function humanize(segment: string): string {
  const spaced = segment.replace(/-/g, " ");
  if (spaced.length === 0) return "";
  return spaced[0].toUpperCase() + spaced.slice(1);
}
