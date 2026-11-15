// #2081 — the sidebar's grouped nav, as pure data.
//
// The desktop sidebar and the mobile drawer both render the groups this
// module builds; `getAuthedNav()` (components/shell/sidebar.tsx) supplies
// the three per-request facts it needs. Pure and client-safe, so the
// whole shape — which entries show for which role, flag and catalog — is
// vitest-covered without a server.
//
// Labels are never written here: each entry's label is the title of the
// page it opens, read from `PAGE_TITLES` (lib/page-titles.ts).
//
// The two existing gates are applied unchanged:
//
//   * modules — `gateNavItems` (lib/feature-nav.ts, slice 660 / #2170)
//     drops the entries of every optional module that is not on for the
//     caller, using the pages `GET /v1/me/modules` lists per module;
//   * the Admin entry — `shouldShowAdminEntry` (lib/admin-nav.ts, slice
//     186 / #2216) decides whether it is appended.
//
// One gate is new: CSF assessment is listed only when a NIST CSF
// framework version is loaded, and links to that version.

import { shouldShowAdminEntry } from "@/lib/admin-nav";
import { type EffectiveModules, gateNavItems } from "@/lib/feature-nav";
import { PAGE_TITLES, type TitledRoute } from "@/lib/page-titles";

export type NavEntry = {
  /** The URL the entry opens. Differs from `route` only for CSF. */
  href: string;
  /** The route whose title is the label. */
  route: TitledRoute;
  label: string;
};

export type NavGroup = {
  label: string;
  items: NavEntry[];
};

// The order is the sidebar's, top to bottom.
export const NAV_GROUPS: readonly {
  label: string;
  routes: readonly TitledRoute[];
}[] = [
  {
    label: "Program",
    routes: ["/dashboard", "/calendar", "/dashboards/metrics", "/activity"],
  },
  {
    label: "Controls",
    routes: ["/controls", "/evidence", "/exceptions", "/controls/checklist"],
  },
  {
    label: "Risks",
    routes: ["/risks", "/risks/hierarchy", "/risks/taxonomy", "/action-plans"],
  },
  {
    label: "Audit",
    routes: [
      "/audits",
      "/framework-scopes",
      "/board-packs",
      "/csf",
      "/oscal/component-definitions",
    ],
  },
  {
    label: "Library",
    routes: [
      "/policies",
      "/vendors",
      "/personnel-security",
      "/questionnaires",
      "/catalog/scf",
      "/catalog/audited-under",
    ],
  },
  {
    label: "Settings & admin",
    routes: ["/settings", "/admin"],
  },
];

export type NavFacts = {
  /** The caller's `/v1/me` body; decides the Admin entry. */
  me: { is_admin?: unknown; roles?: unknown };
  /** The caller's effective modules; decides each module's entries. */
  modules: EffectiveModules;
  /** The loaded NIST CSF framework version, or null when none is. */
  csfVersionId: string | null;
};

/**
 * buildNav returns the sidebar's groups for one request. Entries a gate
 * hides are dropped, and a group left with no entries is dropped too.
 */
export function buildNav({ me, modules, csfVersionId }: NavFacts): NavGroup[] {
  const showAdmin = shouldShowAdminEntry(me);
  const groups: NavGroup[] = [];
  for (const group of NAV_GROUPS) {
    const entries: NavEntry[] = [];
    for (const route of group.routes) {
      if (route === "/admin" && !showAdmin) continue;
      let href: string = route;
      if (route === "/csf") {
        if (!csfVersionId) continue;
        href = `/csf/${encodeURIComponent(csfVersionId)}`;
      }
      entries.push({ href, route, label: PAGE_TITLES[route] });
    }
    const items = gateNavItems(entries, modules);
    if (items.length > 0) {
      groups.push({ label: group.label, items });
    }
  }
  return groups;
}
