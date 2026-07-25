"use client";

// Slice 749 — period-scoped (frozen) AI evidence-summary card (audit workspace).
//
// The audit-workspace sibling of slice 502's control-detail evidence-summary
// card. Renders the DETERMINISTIC bounded FROZEN-population evidence list ALWAYS
// (observed_at <= frozen_at — invariant #10, P0-749-1), plus a NON-BINDING,
// cited, local-default-Ollama summary of that FROZEN evidence when the backend
// returns one (AC-2). The card is clearly labeled period-scoped + frozen-as-of
// `frozen_at` (AC-4). It is a comprehension aid OVER the frozen sample — there is
// NO approve/publish/export affordance anywhere in this component (AC-4,
// P0-502-3). When the summary is suppressed (generation unavailable, no frozen
// evidence, or a citation failed to verify) the frozen evidence list still
// renders with a short honest note (graceful degradation, AC-7).
//
// When the tenant is on a cloud provider (slice 499 opt-in) the shared
// CloudRoutingBanner renders the "your data leaves this deployment" affordance —
// inherited for free, never re-implemented here (P0-502-6).
//
// The render decisions live in the node-testable view-model
// (period-evidence-summary-view.ts); this component is a thin renderer over it so
// the non-binding + frozen-population contract is unit-covered on the fast vitest
// surface and the rendered DOM is covered by the Playwright e2e tier.

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
  buildPeriodEvidenceSummaryView,
  formatFrozenAsOf,
  formatFrozenEvidenceBound,
} from "@/components/audit/period-evidence-summary-view";
import { fetchPeriodEvidenceSummary } from "@/lib/api/control-detail";

export function PeriodEvidenceSummaryCard({
  auditPeriodId,
  controlId,
}: {
  auditPeriodId: string;
  controlId: string;
}) {
  const summaryQ = useQuery({
    queryKey: [
      "audit",
      auditPeriodId,
      "control",
      controlId,
      "evidence-summary",
    ],
    queryFn: () => fetchPeriodEvidenceSummary(auditPeriodId, controlId),
    // The summary is regenerated on demand (P0-502-4) and the model call is the
    // slow part — do not refetch on every window focus.
    refetchOnWindowFocus: false,
  });

  return (
    <Card data-testid="period-evidence-summary-section">
      <CardHeader>
        <CardTitle>What this period&apos;s evidence shows</CardTitle>
        <CardDescription>
          Plain-language summary of this control&apos;s frozen audit-period
          evidence
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {/* P0-502-6: cloud-routing honesty affordance (renders only when the
            tenant opted into a cloud provider; nothing for the local default). */}
        <CloudRoutingBanner />
        {summaryQ.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : summaryQ.error || !summaryQ.data ? (
          <p
            className="text-sm text-muted-foreground"
            data-testid="period-evidence-summary-unavailable"
          >
            Period-scoped evidence summary is unavailable for this control right
            now.
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
  data: Parameters<typeof buildPeriodEvidenceSummaryView>[0];
}) {
  const view = buildPeriodEvidenceSummaryView(data);
  return (
    <div className="space-y-3" data-testid="period-evidence-summary-body">
      {/* AC-4: the load-bearing period-scoped + frozen-as-of label — ALWAYS
          rendered, so the operator can never mistake this for live evidence. */}
      <Badge
        variant="secondary"
        data-testid="period-evidence-summary-frozen-label"
      >
        {formatFrozenAsOf(data)}
      </Badge>

      {/* Deterministic bound — ALWAYS rendered (AC-7); frozen population only. */}
      <p
        className="text-sm text-muted-foreground"
        data-testid="period-evidence-summary-bound"
      >
        {formatFrozenEvidenceBound(data)}
      </p>

      {view.showSummary ? (
        <div
          className="space-y-2"
          data-testid="period-evidence-summary-text-block"
        >
          <p
            className="text-sm text-foreground"
            data-testid="period-evidence-summary-text"
          >
            {view.text}
          </p>
          {view.citations.length > 0 ? (
            <div
              className="flex flex-wrap gap-1"
              data-testid="period-evidence-summary-citations"
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
          {/* AC-4: visible non-audit-artifact disclosure naming the model. This
              is the only metadata shown — there is deliberately NO approve /
              publish / export control here (AC-4, P0-502-3). */}
          <p
            className="text-xs italic text-muted-foreground"
            data-testid="period-evidence-summary-disclosure"
          >
            {view.disclosure}
          </p>
        </div>
      ) : (
        <p
          className="text-xs text-muted-foreground"
          data-testid="period-evidence-summary-degraded"
        >
          {view.degradedNote}
        </p>
      )}
    </div>
  );
}
