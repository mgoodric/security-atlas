import type { ReactNode } from "react";

import Link from "next/link";
import { cookies, headers } from "next/headers";

import {
  ControlsCountBadge,
  RisksCountBadge,
} from "@/components/shell/sidebar-counts";
import { shouldShowAdminEntry } from "@/lib/admin-nav";
import { SESSION_COOKIE } from "@/lib/auth";
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
// response collapses to "hide the Admin entry" — never "show by default."
// The current user may briefly see the entry absent during the initial
// fetch; rendering ghost admin chrome would be worse than a brief gap.
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
type NavItem = {
  href: string;
  label: string;
  slot?: ReactNode;
};

const NAV_BASE: NavItem[] = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/calendar", label: "Calendar" },
  { href: "/dashboards/metrics", label: "Metrics" },
  { href: "/controls", label: "Controls", slot: <ControlsCountBadge /> },
  { href: "/evidence", label: "Evidence" },
  { href: "/risks", label: "Risks", slot: <RisksCountBadge /> },
  { href: "/audits", label: "Audits" },
  { href: "/policies", label: "Policies" },
  { href: "/vendors", label: "Vendors" },
  { href: "/board-packs", label: "Board Packs" },
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
    const bearer = jar.get(SESSION_COOKIE)?.value;
    if (!bearer) return {};
    const h = await headers();
    const host = h.get("host") ?? "localhost:3000";
    const proto = h.get("x-forwarded-proto") ?? "http";
    const res = await fetch(`${proto}://${host}/api/admin/me`, {
      headers: { Cookie: `${SESSION_COOKIE}=${bearer}` },
      cache: "no-store",
    });
    if (!res.ok) return {};
    return (await res.json()) as { is_admin?: unknown; roles?: unknown };
  } catch {
    return {};
  }
}

export async function Sidebar({ active }: { active?: string }) {
  const meBody = await fetchAdminMe();
  const showAdmin = shouldShowAdminEntry(meBody);
  const nav = showAdmin ? [...NAV_BASE, ADMIN_NAV_ITEM] : NAV_BASE;

  return (
    <aside className="w-56 shrink-0 border-r bg-muted/30 p-4">
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
