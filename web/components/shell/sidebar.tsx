import Link from "next/link";

import { cn } from "@/lib/utils";

// Canonical order per Plans/canvas/12-ui-fill-in-design-decisions.md §1:
// Dashboard · Controls · Evidence · Risks · Audits · Policies · Vendors ·
// Board Packs · Settings · Admin.
//
// Post-093 additions kept (see Plans/canvas/13-ui-mockup-audit-2026-05-16.md
// F-2): Calendar (094) + Metrics (097) cluster with Dashboard as the cross-
// business "at-a-glance" group; Catalog · SCF sits after the core-5 as a
// reference-content top-level. Risk hierarchy stays in nav until slice 101
// ships /risks (which lifts hierarchy to a page-header link there).
const NAV = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/calendar", label: "Calendar" },
  { href: "/dashboards/metrics", label: "Metrics" },
  { href: "/controls", label: "Controls" },
  { href: "/evidence", label: "Evidence" },
  { href: "/risks", label: "Risks" },
  { href: "/risks/hierarchy", label: "Risk hierarchy" },
  { href: "/audits", label: "Audits" },
  { href: "/policies", label: "Policies" },
  { href: "/vendors", label: "Vendors" },
  { href: "/board-packs", label: "Board Packs" },
  { href: "/catalog/scf", label: "Catalog · SCF" },
  { href: "/settings", label: "Settings" },
  { href: "/admin", label: "Admin" },
];

export function Sidebar({ active }: { active?: string }) {
  return (
    <aside className="w-56 shrink-0 border-r bg-muted/30 p-4">
      <nav className="flex flex-col gap-1">
        {NAV.map((item) => {
          const isActive = active ? active === item.href : false;
          return (
            <Link
              key={item.href}
              href={item.href}
              className={cn(
                "rounded-md px-3 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "bg-foreground/10 text-foreground"
                  : "text-muted-foreground hover:bg-foreground/5 hover:text-foreground",
              )}
            >
              {item.label}
            </Link>
          );
        })}
      </nav>
    </aside>
  );
}
