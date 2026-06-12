import type { ReactNode } from "react";

import Link from "next/link";
import { cookies, headers } from "next/headers";

import {
  ControlsCountBadge,
  RisksCountBadge,
} from "@/components/shell/sidebar-counts";
import { shouldShowAdminEntry } from "@/lib/admin-nav";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { gateNavItems } from "@/lib/feature-nav";
import { fetchEnabledModules } from "@/lib/feature-nav.server";
import { cn } from "@/lib/utils";

// Canonical order per Plans/canvas/12-ui-fill-in-design-decisions.md §1:
// Dashboard · Controls · Evidence · Risks · Audits · Policies · Vendors ·
// Board Packs · Settings · Admin.
//
// Post-093 additions kept (see Plans/canvas/13-ui-mockup-audit-2026-05-16.md
// F-2): Calendar (094) + Metrics (097) cluster with Dashboard as the cross-
// business "at-a-glance" group; Catalog · SCF sits after the core-5 as a
// reference-content top-level.
//
// Slice 100 closure of audit F-3 (Plans/canvas/13-ui-mockup-audit-2026-05-16.md):
// the `/risks/hierarchy` top-nav entry was REMOVED. The flat /risks list is
// the canonical default per design doc §5, and the hierarchy stays
// reachable via the `Hierarchy view ->` page-header link on /risks plus the
// reciprocal `List view ->` link on /risks/hierarchy.
//
// Slice 186 (F-178-6 closure): the `/admin` entry is now ROLE-GATED. The
// component is async (Next.js 16 App Router server component) and fetches
// the slice-130 BFF `/api/admin/me` to learn whether the current bearer is
// an admin (cred flag or role grant). When the predicate returns false,
// the Admin entry is filtered out before render — non-admin callers no
// longer see a sidebar entry they would bounce off the server-side authz
// gate at `web/app/admin/layout.tsx`.
//
// The server-side authz gate is unchanged (P0-186-1): this is UI chrome
// only. P0-186-4 fail-closed: a fetch failure / empty body / non-200
// response collapses to "hide the Admin entry." The current user may
// briefly see the entry absent during the initial fetch; rendering ghost
// admin chrome would be worse than a brief gap.
//
// Slice 214 — NAV item rows now carry an optional `slot` ReactNode
// rendered right-of-label (the existing flex row gives the slot's
// `ml-auto` a natural right-align). Mounted on the Controls + Risks
// rows only:
//
//   - `<ControlsCountBadge />` — mono muted aggregate count.
//   - `<RisksCountBadge />`    — mono rose count of severity-high
//     rows; hidden when zero (P0-214-2 silent absence).
//
// Both badges are client components driven by TanStack Query against
// the existing per-page BFFs. Slot composition stays a server-component
// concern; the slice 186 admin gate is unchanged because we only added
// to the NAV item shape, not the predicate.
//
// Slice 277 — mobile-responsive baseline. The desktop `<aside>` is
// hidden at viewport widths `< md` (768px) via `hidden md:flex`; a
// drawer (`<MobileSidebar>`) takes over below that breakpoint. The
// nav-item array + admin probe are exported so the mobile drawer
// renders the SAME nav without re-running the admin probe in two
// places. Desktop UX at `≥ md` is unchanged (P0-277-1).
export type NavItem = {
  href: string;
  label: string;
  slot?: ReactNode;
};

// Slice 270 (D4): the `/activity` entry sits alongside the
// dashboard / calendar / metrics cross-business "at-a-glance" cluster
// because that is the cluster it semantically belongs to (program-pulse
// surfaces). The link renders for every authed user — the page itself
// is non-admin-gated (slice 270 D1; the OPA admit covers all five
// tenant-member roles) so no role probe is needed.
const NAV_BASE: NavItem[] = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/calendar", label: "Calendar" },
  { href: "/dashboards/metrics", label: "Metrics" },
  { href: "/activity", label: "Activity" },
  { href: "/controls", label: "Controls", slot: <ControlsCountBadge /> },
  { href: "/evidence", label: "Evidence" },
  { href: "/risks", label: "Risks", slot: <RisksCountBadge /> },
  { href: "/audits", label: "Audits" },
  { href: "/policies", label: "Policies" },
  { href: "/vendors", label: "Vendors" },
  // Slice 263 — Questionnaires sits in the Operations cluster (matches
  // Calendar / Vendors). All authed users see the entry; per-tenant
  // write authz is enforced at the API layer (slice 155).
  { href: "/questionnaires", label: "Questionnaires" },
  { href: "/board-packs", label: "Board Packs" },
  // Slice 589 — operator review of imported vendor component-definition
  // CLAIMS (OSCAL component-definition import, slice 512). A vendor claim is
  // an assertion, not platform-verified evidence; the operator accepts /
  // rejects / parks each claim. Read + disposition authz enforced at the API.
  { href: "/oscal/component-definitions", label: "Vendor Claims" },
  { href: "/catalog/scf", label: "Catalog · SCF" },
  { href: "/settings", label: "Settings" },
];

const ADMIN_NAV_ITEM: NavItem = { href: "/admin", label: "Admin" };

async function fetchAdminMe(): Promise<{
  is_admin?: unknown;
  roles?: unknown;
}> {
  // Slice 186: read the bearer cookie + call the BFF self-introspection
  // endpoint (same pattern as `app/admin/layout.tsx` + `app/audit-log/
  // layout.tsx`). Self-referential fetch via host + proto so the call
  // resolves whether we're rendering on the server (where
  // NEXT_PUBLIC_API_BASE_URL points at the platform) or in dev with
  // the proxy. P0-186-4 fail-closed — any error collapses to `{}` and
  // the predicate returns false (hide the Admin entry).
  try {
    const jar = await cookies();
    const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
    if (!bearer) return {};
    const h = await headers();
    const host = h.get("host") ?? "localhost:3000";
    const proto = h.get("x-forwarded-proto") ?? "http";
    const res = await fetch(`${proto}://${host}/api/admin/me`, {
      headers: { Cookie: `${ATLAS_JWT_COOKIE}=${bearer}` },
      cache: "no-store",
    });
    if (!res.ok) return {};
    return (await res.json()) as { is_admin?: unknown; roles?: unknown };
  } catch {
    return {};
  }
}

/**
 * Resolve the nav list for the current request, applying the slice 186
 * admin-role gate. Shared by the desktop `<Sidebar>` server component
 * and the mobile drawer (`<MobileSidebar>` consumes the resolved list
 * via the authed layout) so the admin probe runs once per request.
 *
 * Slice 277 — exposed for the mobile-drawer path. The probe and gate
 * logic are unchanged from slice 186.
 */
export async function getAuthedNav(): Promise<NavItem[]> {
  // Slice 660 — resolve the admin gate (186) and the feature-flag gate
  // (660) server-side, once per request. The two probes are independent;
  // fetch them in parallel. `gateNavItems` then drops the nav entries
  // whose gating flag is off (Vendor Claims when `oscal.export` is off;
  // Board Packs when `board.reporting` is off) for ALL users — the mobile
  // drawer consumes this same gated list via the authed layout, so there
  // is a single source of truth for which items hide.
  const [meBody, modules] = await Promise.all([
    fetchAdminMe(),
    fetchEnabledModules(),
  ]);
  const showAdmin = shouldShowAdminEntry(meBody);
  const base = gateNavItems(NAV_BASE, modules);
  return showAdmin ? [...base, ADMIN_NAV_ITEM] : base;
}

export async function Sidebar({ active }: { active?: string }) {
  const nav = await getAuthedNav();

  // Slice 277 — `hidden md:flex` collapses the desktop sidebar at
  // viewport widths `< 768px` (Tailwind `md`). At `≥ md` the element
  // renders inline exactly as it did pre-277 (P0-277-1 — no desktop
  // UX regression). The mobile drawer (`<MobileSidebar>`) is rendered
  // from the authed layout and takes over below the breakpoint.
  return (
    <aside
      className="hidden w-56 shrink-0 border-r bg-muted/30 p-4 md:block"
      data-testid="sidebar-desktop"
    >
      <nav className="flex flex-col gap-1">
        {nav.map((item) => {
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
              {/*
                Slice 214 — optional right-aligned count badge slot.
                The badge component carries `ml-auto` so it floats
                right inside this flex row; rows without a slot just
                render the label.
              */}
              {item.slot}
            </Link>
          );
        })}
      </nav>
    </aside>
  );
}
