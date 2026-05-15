import Link from "next/link";

import { cn } from "@/lib/utils";

const NAV = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/catalog/scf", label: "Catalog · SCF" },
  { href: "/controls", label: "Controls" },
  { href: "/evidence", label: "Evidence" },
  { href: "/risks", label: "Risks" },
  { href: "/risks/hierarchy", label: "Risk hierarchy" },
  { href: "/policies", label: "Policies" },
  { href: "/vendors", label: "Vendors" },
  { href: "/audits", label: "Audits" },
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
