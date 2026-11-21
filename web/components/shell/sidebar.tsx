import Link from "next/link";
import { cookies } from "next/headers";

import { badgeForHref } from "@/components/shell/nav-badges";
import {
  fetchAdminMe as fetchAdminMeServer,
  fetchCsfVersionId,
} from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { fetchEffectiveModules } from "@/lib/feature-nav.server";
import { buildNav, type NavGroup } from "@/lib/nav";
import { cn } from "@/lib/utils";
import { VersionFooter } from "@/components/version-footer";

// #2081 — the sidebar lists every top-level page, in six groups
// (Program · Controls · Risks · Audit · Library · Settings & admin), and
// each entry's label is the title of the page it opens. The groups, the
// order and the gates are pure data in `lib/nav.ts`; the labels come from
// `lib/page-titles.ts`, the same map the pages read their titles from.
// Before #2081 Exceptions, Framework scopes and the Implementation
// checklist were reachable only by URL, and labels such as
// "Personnel" and "Catalog · SCF" did not match their pages' titles.
//
// The gates are unchanged:
//
//   * Slice 186 / #2216 — the Admin entry renders only when
//     `shouldShowAdminEntry` admits the caller's `/v1/me` body. This is
//     UI chrome only; `app/admin/layout.tsx` is the authz gate. A probe
//     failure hides the entry (P0-186-4 fail-closed).
//   * Slice 660 / #2170 — `gateNavItems` drops every entry whose optional
//     module is switched off, for every user. Which pages a module owns
//     comes from `GET /v1/me/modules`, the server's registry. A probe
//     failure gates nothing here; the page's FeatureGate fails closed.
//
// One gate is new: CSF assessment renders only when a NIST CSF framework
// version is loaded, and links to it. A probe failure hides it.
//
// Slice 214 — the Controls and Risks rows carry a count badge
// (`badgeForHref`). Slice 277 — below `md` this aside is hidden and the
// mobile drawer renders the same groups, resolved once per request by
// `getAuthedNav()` in AppShell.

async function readBearer(): Promise<string | undefined> {
  const jar = await cookies();
  return jar.get(ATLAS_JWT_COOKIE)?.value;
}

/**
 * Resolve the nav groups for the current request. Shared by the desktop
 * `<Sidebar>` and the mobile drawer (AppShell passes the result to
 * `<MobileSidebar>`), so both apply the same gates.
 *
 * OE-549: the probes read the platform through the internal base
 * (`lib/api/self.server.ts`), never the app's own origin.
 */
export async function getAuthedNav(): Promise<NavGroup[]> {
  const bearer = await readBearer();
  const [me, modules, csfVersionId] = await Promise.all([
    bearer ? fetchAdminMeServer(bearer) : Promise.resolve({}),
    fetchEffectiveModules(),
    bearer ? fetchCsfVersionId(bearer) : Promise.resolve(null),
  ]);
  return buildNav({ me, modules, csfVersionId });
}

export async function Sidebar({ active }: { active?: string }) {
  const nav = await getAuthedNav();

  // Slice 277 — `hidden md:flex` collapses the desktop sidebar below
  // 768px, where the mobile drawer takes over.
  return (
    <aside
      className="hidden w-56 shrink-0 flex-col gap-4 overflow-y-auto border-r bg-muted/30 p-4 md:flex"
      data-testid="sidebar-desktop"
    >
      <nav className="flex flex-col gap-4" aria-label="Main navigation">
        {nav.map((group) => (
          <NavGroupList key={group.label} group={group} active={active} />
        ))}
      </nav>
      {/* #2080 — the version tag lives here and nowhere else on an
        authenticated page (it was a fixed bottom-right overlay plus a
        "v0 · self-host" chip in the top bar). */}
      <div className="mt-auto pt-2">
        <VersionFooter placement="sidebar" />
      </div>
    </aside>
  );
}

function NavGroupList({ group, active }: { group: NavGroup; active?: string }) {
  const headingId = `sidebar-group-${group.label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")}`;
  return (
    <div
      role="group"
      aria-labelledby={headingId}
      className="flex flex-col gap-1"
      data-testid={headingId}
    >
      <p
        id={headingId}
        className="px-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
      >
        {group.label}
      </p>
      {group.items.map((item) => {
        const isActive = active ? active === item.href : false;
        return (
          <Link
            key={item.href}
            href={item.href}
            className={cn(
              "flex items-center rounded-md px-3 py-2 text-sm font-medium transition-colors",
              isActive
                ? "bg-foreground/10 text-foreground"
                : "text-muted-foreground hover:bg-foreground/5 hover:text-foreground",
            )}
          >
            <span>{item.label}</span>
            {badgeForHref(item.href)}
          </Link>
        );
      })}
    </div>
  );
}
