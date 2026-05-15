"use client";

// Slice 040 — recent activity feed (AC-6).
//
// The mockup shows an infinite-scroll feed of evidence-ingest, control
// state-change, and approval events, with All/Evidence/Controls/
// Approvals filter chips. The issue specifies it is "backed by a
// NATS-driven event stream archive".
//
// There is no event-stream archive read endpoint on main: a grep of
// `internal/api/` finds no activity / events / feed handler, and no
// NATS-archive read model exists. Per the slice 041 / 060 precedent
// this panel renders an endpoint-naming placeholder rather than
// blocking the slice or fabricating activity rows (anti-criterion
// P0-1). The filter chips render as a disabled, data-free scaffold so
// the layout matches the mockup; infinite scroll is wired when the
// endpoint lands.

import { MissingEndpointPanel } from "@/components/dashboard/panel-card";

const FILTER_CHIPS = ["All", "Evidence", "Controls", "Approvals"];

export function ActivityFeedPanel() {
  return (
    <MissingEndpointPanel
      title="Recent activity"
      description="Evidence ingest, control state changes, approvals"
      endpoint="GET /v1/activity"
      detail="A read model over the NATS-driven event-stream archive is needed to back the infinite-scroll feed; it is tracked as a follow-up backend slice."
      testid="activity-feed-panel"
    >
      <div
        className="mt-4 flex items-center gap-2"
        data-testid="activity-feed-filters"
      >
        {FILTER_CHIPS.map((chip) => (
          <span
            key={chip}
            data-testid="activity-filter-chip"
            aria-disabled="true"
            className="cursor-not-allowed rounded bg-muted px-2 py-1 text-xs text-muted-foreground"
          >
            {chip}
          </span>
        ))}
      </div>
    </MissingEndpointPanel>
  );
}
