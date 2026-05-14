"use client";

// Slice 040 — framework posture tiles (AC-2).
//
// The mockup shows one tile per framework (SOC 2, ISO 27001, NIST CSF,
// HIPAA, PCI DSS, GDPR) with a coverage percentage, a signed trend
// arrow, and an in-scope count. That data is a per-framework
// coverage+freshness composite with a historical trend.
//
// There is no per-framework posture endpoint on main: `internal/api/
// ucfcoverage` serves only per-control coverage (`GET /v1/controls/{id}/
// coverage`), and there is no coverage-trend or posture-aggregate
// handler anywhere in `internal/api/`. Per the slice 041 / 060
// precedent, this panel renders an endpoint-naming placeholder rather
// than blocking the slice or fabricating percentages and trend arrows
// (anti-criterion P0-1). The six framework slots are rendered as a
// labelled, data-free scaffold so the layout matches the mockup.

import { MissingEndpointPanel } from "@/components/dashboard/panel-card";

const FRAMEWORK_SLOTS = [
  "SOC 2",
  "ISO 27001",
  "NIST CSF",
  "HIPAA",
  "PCI DSS",
  "GDPR",
];

export function FrameworkPosturePanel() {
  return (
    <MissingEndpointPanel
      title="Framework posture"
      description="Coverage and trend per framework"
      endpoint="GET /v1/frameworks/posture"
      detail="A per-framework coverage + freshness composite endpoint with a 90-day trend is needed; it is tracked as a follow-up backend slice."
      testid="framework-posture-panel"
    >
      <div
        className="mt-4 grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6"
        data-testid="framework-posture-slots"
      >
        {FRAMEWORK_SLOTS.map((name) => (
          <div
            key={name}
            data-testid="framework-tile"
            className="rounded-xl bg-muted/40 p-4 ring-1 ring-foreground/5"
          >
            <div className="text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
              {name}
            </div>
            <div className="mt-3 text-sm text-muted-foreground italic">
              awaiting posture endpoint
            </div>
          </div>
        ))}
      </div>
    </MissingEndpointPanel>
  );
}
