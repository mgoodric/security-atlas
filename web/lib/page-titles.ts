// #2081 — the one route -> page-title map.
//
// Every top-level page reads its H1 and its document title from here, and
// the sidebar reads its labels from here, so a sidebar label and the
// title of the page it opens cannot drift apart: there is only one string.
//
// Keys are the route a sidebar entry opens. `/csf` stands for the CSF
// assessment, whose URL carries the loaded framework version
// (`/csf/<framework_version_id>`); the title is the same for any version.
//
// Pure and client-safe: imported by server layouts (metadata), client
// pages (PageShell / ListPage titles) and the nav builder alike.
//
// This is not the breadcrumb's section map (`lib/page-names.ts`), which
// names the URL's first segment so detail pages roll up to their section.

export const PAGE_TITLES = {
  "/dashboard": "Dashboard",
  "/calendar": "Calendar",
  "/dashboards/metrics": "Metrics",
  "/activity": "Activity",
  "/controls": "Controls",
  "/evidence": "Evidence",
  "/exceptions": "Exceptions",
  "/controls/checklist": "Implementation checklist",
  "/risks": "Risk register",
  "/risks/hierarchy": "Risk hierarchy",
  "/risks/taxonomy": "Risk taxonomy",
  "/action-plans": "Action plans",
  "/audits": "Audit periods",
  "/framework-scopes": "Framework scopes",
  "/board-packs": "Board packs",
  "/csf": "CSF assessment",
  "/oscal/component-definitions": "Vendor claims",
  "/policies": "Policies",
  "/vendors": "Vendors",
  "/personnel-security": "Personnel security",
  "/questionnaires": "Questionnaires",
  "/catalog/scf": "SCF catalog",
  "/settings": "Settings",
  "/admin": "Admin",
} as const;

export type TitledRoute = keyof typeof PAGE_TITLES;

/** pageTitle returns the title of the page at `route`. */
export function pageTitle(route: TitledRoute): string {
  return PAGE_TITLES[route];
}
