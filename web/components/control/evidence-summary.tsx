"use client";

// Slice 502 — AI evidence-summary card (control-detail, Overview tab).
//
// Renders the DETERMINISTIC bounded CURRENT LIVE evidence list ALWAYS, plus a
// NON-BINDING, cited, local-default-Ollama summary of that evidence when the
// backend returns one (AC-6). The summary is a comprehension aid — there is NO
// approve/publish/export affordance anywhere in this component (AC-5,
// P0-502-3). When the summary is suppressed (generation unavailable, no
// evidence, or a citation failed to verify) the evidence list still renders with
// a short honest note (graceful degradation, AC-7).
//
// When the tenant is on a cloud provider (slice 499 opt-in) the shared
// CloudRoutingBanner renders the "your data leaves this deployment" affordance —
// inherited for free, never re-implemented here (P0-502-6).
//
// The render decisions live in the node-testable view-model
// (evidence-summary-view.ts); this component is a thin renderer over it so the
// non-binding contract is unit-covered on the fast vitest surface and the
// rendered DOM is covered by the Playwright e2e tier. Sibling of slice 444's
// gap-explanation.tsx.

import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { CloudRoutingBanner } from "@/components/llm/cloud-routing-banner";
import {
  buildEvidenceSummaryView,
  formatEvidenceBound,
} from "@/components/control/evidence-summary-view";
import { fetchControlEvidenceSummary } from "@/lib/api/control-detail";

export function EvidenceSummaryCard({ id }: { id: string }) {
  const summaryQ = useQuery({
    queryKey: ["control", id, "evidence-summary"],
    queryFn: () => fetchControlEvidenceSummary(id),
    // The summary is regenerated on demand (P0-502-4) and the model call is the
    // slow part — do not refetch on every window focus.
    refetchOnWindowFocus: false,
  });

  return (
    <Card data-testid="evidence-summary-section">
      <CardHeader>
        <CardTitle>What this evidence shows</CardTitle>
        <CardDescription>
          Plain-language summary of current live evidence
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {/* P0-502-6: cloud-routing honesty affordance (renders only when the
            tenant opted into a cloud provider; nothing for the local default). */}
        <CloudRoutingBanner />
        {summaryQ.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : summaryQ.error || !summaryQ.data ? (
          <p className="text-sm text-muted-foreground">
            Evidence summary is unavailable for this control right now.
          </p>
        ) : (
          <SummaryBody data={summaryQ.data} />
        )}
      </CardContent>
    </Card>
  );
}

function SummaryBody({
  data,
}: {
  data: Parameters<typeof buildEvidenceSummaryView>[0];
}) {
  const view = buildEvidenceSummaryView(data);
  return (
    <div className="space-y-3" data-testid="evidence-summary-body">
      {/* Deterministic bound — ALWAYS rendered (AC-7); current live evidence
          only, clearly labeled (P0-502-5). */}
      <p
        className="text-sm text-muted-foreground"
        data-testid="evidence-summary-bound"
      >
        {formatEvidenceBound(data)}
      </p>

      {view.showSummary ? (
        <div className="space-y-2" data-testid="evidence-summary-text-block">
          <p
            className="text-sm text-foreground"
            data-testid="evidence-summary-text"
          >
            {view.text}
          </p>
          {view.citations.length > 0 ? (
            <div
              className="flex flex-wrap gap-1"
              data-testid="evidence-summary-citations"
            >
              {view.citations.map((c) => (
                <Badge
                  key={`${c.kind}:${c.id}`}
                  variant="outline"
                  className="font-mono text-xs"
                >
                  {c.kind}:{c.id.slice(0, 8)}
                </Badge>
              ))}
            </div>
          ) : null}
          {/* AC-6: visible non-audit-artifact disclosure naming the model. This
              is the only metadata shown — there is deliberately NO approve /
              publish / export control here (AC-5, P0-502-3). */}
          <p
            className="text-xs italic text-muted-foreground"
            data-testid="evidence-summary-disclosure"
          >
            {view.disclosure}
          </p>
        </div>
      ) : (
        <p
          className="text-xs text-muted-foreground"
          data-testid="evidence-summary-degraded"
        >
          {view.degradedNote}
        </p>
      )}
    </div>
  );
}
