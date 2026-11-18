"use client";

// Slice 444 — AI gap-explanation card (Overview tab, right rail).
//
// Renders the DETERMINISTIC freshness rollup ALWAYS, plus a NON-BINDING,
// cited, local-Ollama explanation of that rollup when the backend returns one
// (AC-6). The explanation is a comprehension aid — there is NO
// approve/publish/export affordance anywhere in this component (AC-5,
// P0-444-3). #2140 changed the degraded case: when the explanation is
// suppressed (generation unavailable or a citation failed to verify), still
// loading, or the read failed, the card renders nothing. The control page's
// state strip carries the deterministic state and freshness instead.
//
// The render decisions live in the node-testable view-model
// (gap-explanation-view.ts); this component is a thin renderer over it so the
// non-binding contract is unit-covered on the fast vitest surface and the
// rendered DOM is covered by the Playwright e2e tier.

import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  buildGapExplanationView,
  formatRollupSummary,
} from "@/components/control/gap-explanation-view";
import { fetchControlGapExplanation } from "@/lib/api/control-detail";

export function GapExplanationCard({ id }: { id: string }) {
  const gapQ = useQuery({
    queryKey: ["control", id, "gap-explanation"],
    queryFn: () => fetchControlGapExplanation(id),
    // The explanation is regenerated on demand (P0-444-4) and the model call
    // is the slow part — do not refetch on every window focus.
    refetchOnWindowFocus: false,
  });

  // #2140: the card shows only when there is an explanation to show. While
  // it loads, when the read fails, or when generation was suppressed, it
  // renders nothing: no "unavailable" notice, and the state strip already
  // carries the deterministic state and freshness.
  if (!gapQ.data || !buildGapExplanationView(gapQ.data).showExplanation) {
    return null;
  }

  return (
    <Card data-testid="gap-explanation-section">
      <CardHeader>
        <CardTitle>Why this state</CardTitle>
        <CardDescription>Plain-language gap explanation</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <GapBody data={gapQ.data} />
      </CardContent>
    </Card>
  );
}

function GapBody({
  data,
}: {
  data: Parameters<typeof buildGapExplanationView>[0];
}) {
  const view = buildGapExplanationView(data);
  return (
    <div className="space-y-3" data-testid="gap-explanation-body">
      {/* Deterministic rollup — ALWAYS rendered (AC-7). */}
      <p className="text-sm" data-testid="gap-rollup-summary">
        {formatRollupSummary(data)}
      </p>

      {view.showExplanation ? (
        <div className="space-y-2" data-testid="gap-explanation-text-block">
          <p
            className="text-sm text-foreground"
            data-testid="gap-explanation-text"
          >
            {view.text}
          </p>
          {view.citations.length > 0 ? (
            <div
              className="flex flex-wrap gap-1"
              data-testid="gap-explanation-citations"
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
          {/* AC-6: visible non-audit-artifact disclosure naming the model.
              This is the only metadata shown — there is deliberately NO
              approve / publish / export control here (AC-5, P0-444-3). */}
          <p
            className="text-xs italic text-muted-foreground"
            data-testid="gap-explanation-disclosure"
          >
            {view.disclosure}
          </p>
        </div>
      ) : (
        <p
          className="text-xs text-muted-foreground"
          data-testid="gap-explanation-degraded"
        >
          {view.degradedNote}
        </p>
      )}
    </div>
  );
}
