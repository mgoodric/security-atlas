// Slice 042 — left nav listing controls in scope for the period (AC-2).
//
// BACKEND GAP (surfaced to orchestrator): there is no
// `GET /v1/controls` list endpoint and no period-scoped control-list
// endpoint (`GET /v1/audit-periods/{id}/controls`) on main as of this
// slice. The auditor's period carries `framework_version_id`, but there
// is also no `GET /v1/framework-versions/{id}/requirements` endpoint to
// walk requirement -> coverage -> controls.
//
// Until a follow-up backend slice lands that endpoint, the control set
// is supplied by the workspace from the populations the auditor draws
// within the session (each population carries `control_id`). When the
// set is empty, this component renders a documented empty-state that
// names the missing endpoint — the slice-060 SSO/Users stub pattern.

"use client";

import Link from "next/link";

import { cn } from "@/lib/utils";

export type NavControl = {
  controlId: string;
  // A human label if known; falls back to a truncated id.
  label?: string;
};

export function ControlNav({
  controls,
  selectedControlId,
}: {
  controls: NavControl[];
  selectedControlId?: string;
}) {
  return (
    <nav
      aria-label="Controls in scope"
      data-testid="control-nav"
      className="flex w-full shrink-0 flex-col gap-1 border-b p-3 sm:w-64 sm:border-r sm:border-b-0"
    >
      <p className="px-1 pb-1 text-xs font-medium tracking-wide text-muted-foreground uppercase">
        Controls in scope
      </p>
      {controls.length === 0 ? (
        <div
          data-testid="control-nav-empty"
          className="rounded-lg border border-dashed p-3 text-xs leading-relaxed text-muted-foreground"
        >
          No controls loaded yet. The period-scoped control list
          (<code className="rounded bg-muted px-1">
            GET /v1/audit-periods/&#123;id&#125;/controls
          </code>) is a pending backend slice. Until it lands, draw a
          population for a control and it appears here.
        </div>
      ) : (
        <ul className="flex flex-col gap-0.5">
          {controls.map((c) => {
            const active = c.controlId === selectedControlId;
            return (
              <li key={c.controlId}>
                <Link
                  href={`/audit/${encodeURIComponent(c.controlId)}`}
                  data-testid="control-nav-item"
                  data-active={active ? "true" : "false"}
                  className={cn(
                    "block truncate rounded-md px-2 py-1.5 text-sm transition-colors",
                    active
                      ? "bg-primary text-primary-foreground"
                      : "hover:bg-muted",
                  )}
                  title={c.label ?? c.controlId}
                >
                  {c.label ?? c.controlId}
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </nav>
  );
}
