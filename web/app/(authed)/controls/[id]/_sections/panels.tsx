"use client";

// The control-detail tab panels (`/controls/[id]`).
//
// #2140 (front door, #2072): the Overview panel is the page's main
// reading path, three `control-detail-section`s in order, each with its
// `data-section` name:
//
//   - evidence: the recent records, each row opening its evidence detail
//     (#2137), and the AI evidence summary when one is available;
//   - coverage: the frameworks and requirements reached, with the org
//     mapping (#1800), Statement of Applicability exclusion and
//     covered-by-exception markers, and the effective scope per framework;
//   - linked: risks, action plans, exceptions (new here) and policies.
//
// The framework graph is on the Framework map tab (MapPanel) and nowhere
// on the overview. History holds the evaluation history and the
// freshness clock.
//
// Each section reads its own queries and shows its own error, so one
// failing read never blanks the rest. A Linked part into a module the
// caller cannot see (switched off, or outside their module access) is
// left out and never fetched (#2174); the exceptions part is also left
// out when the platform refuses the caller the exceptions read.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import type { ReactNode } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { LinkedActionPlans } from "@/components/action-plans/linked-action-plans";
import { CoverageTable } from "@/components/control/coverage-table";
import { FreshnessClock } from "@/components/control/freshness-clock";
import { EvidenceSummaryCard } from "@/components/control/evidence-summary";
import { UcfMiniViz } from "@/components/control/ucf-mini-viz";

import {
  type ControlCoverage,
  type ControlHistoryResponse,
  type ControlLinkedPoliciesResponse,
  type ControlLinkedRisksResponse,
  type ControlStateResponse,
} from "@/lib/api/control-detail";
import { type EvidenceListResponse } from "@/lib/api/evidence";
import { type ExceptionsListResponse } from "@/lib/api/exceptions";

import {
  EffectiveScopeRows,
  EvidenceStreamBody,
  ExceptionsBody,
  HistoryBody,
  PoliciesBody,
  RisksBody,
} from "./bodies";

type ScopeQueries = ReadonlyArray<{
  data?: unknown;
  isLoading: boolean;
  error: unknown;
}>;

/** One named section of the overview (#2140), the risk page's pattern. */
function ControlSection({
  name,
  label,
  children,
}: {
  name: string;
  label: string;
  children: ReactNode;
}) {
  return (
    <section
      data-testid="control-detail-section"
      data-section={name}
      aria-label={label}
      className="space-y-4"
    >
      <h2 className="text-lg font-semibold">{label}</h2>
      {children}
    </section>
  );
}

export function OverviewPanel({
  id,
  coverage,
  outOfScopeFvIds,
  evidenceQ,
  excepted,
  fvIds,
  scopeQueries,
  policiesQ,
  risksQ,
  exceptionsQ,
  showActionPlans,
}: {
  id: string;
  coverage: ControlCoverage;
  outOfScopeFvIds: Set<string>;
  evidenceQ: ReturnType<typeof useQuery<EvidenceListResponse>>;
  // Some scope cell's evaluated result is `excepted`: an active exception
  // covers it, so its credit stands on the exception.
  excepted: boolean;
  fvIds: string[];
  scopeQueries: ScopeQueries;
  // null when the caller cannot see that module (#2174), or, for
  // exceptions, may not read them: the part is then left out entirely.
  policiesQ: ReturnType<typeof useQuery<ControlLinkedPoliciesResponse>> | null;
  risksQ: ReturnType<typeof useQuery<ControlLinkedRisksResponse>> | null;
  exceptionsQ: ReturnType<typeof useQuery<ExceptionsListResponse>> | null;
  showActionPlans: boolean;
}) {
  const { anchor, requirements } = coverage;
  return (
    <div className="space-y-8">
      {/* ============ EVIDENCE ============ */}
      <ControlSection name="evidence" label="Evidence">
        <Card data-testid="evidence-stream-section">
          <CardHeader className="border-b">
            <CardTitle>Recent evidence</CardTitle>
            <CardDescription>
              Append-only ledger, last 30 days. Open a record to see what it
              says.{" "}
              <Link
                href={`/evidence?control_id=${encodeURIComponent(id)}`}
                className="underline"
                data-testid="evidence-list-link"
              >
                All evidence for this control
              </Link>
            </CardDescription>
          </CardHeader>
          <CardContent>
            <EvidenceStreamBody id={id} evidenceQ={evidenceQ} />
          </CardContent>
        </Card>
        {/* AI evidence summary (slice 502): renders only when available. */}
        <EvidenceSummaryCard id={id} />
      </ControlSection>

      {/* ============ COVERAGE ============ */}
      <ControlSection name="coverage" label="Coverage">
        <Card data-testid="coverage-section">
          <CardHeader className="border-b">
            <CardTitle>Frameworks and requirements</CardTitle>
            <CardDescription>
              Routed through{" "}
              <span className="font-mono">
                {anchor ? anchor.scf_id : "no anchor"}
              </span>{" "}
              · weighted by STRM strength × 30-day effectiveness, intersected
              with FrameworkScope
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {excepted ? (
              <div
                className="flex flex-wrap items-center gap-2 text-sm"
                data-testid="coverage-excepted"
              >
                <Badge variant="info">Covered by exception</Badge>
                <span className="text-muted-foreground">
                  An active exception covers part of this control&apos;s scope,
                  so that part&apos;s credit stands on the exception, not on
                  evidence.
                </span>
              </div>
            ) : null}
            <CoverageTable
              requirements={requirements}
              outOfScopeFvIds={outOfScopeFvIds}
            />
          </CardContent>
        </Card>

        <Card data-testid="effective-scope-section">
          <CardHeader>
            <CardTitle>Effective scope</CardTitle>
            <CardDescription>
              applicability ∩ FrameworkScope, per framework
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2.5">
            <EffectiveScopeRows
              fvIds={fvIds}
              scopeQueries={scopeQueries}
              requirements={requirements}
            />
          </CardContent>
        </Card>
      </ControlSection>

      {/* ============ LINKED ============ */}
      <ControlSection name="linked" label="Linked">
        <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
          {risksQ ? (
            <Card data-testid="risks-section">
              <CardHeader>
                <CardTitle>Risks treated</CardTitle>
              </CardHeader>
              <CardContent>
                <RisksBody risksQ={risksQ} />
              </CardContent>
            </Card>
          ) : null}

          {exceptionsQ ? (
            <Card data-testid="exceptions-section">
              <CardHeader>
                <CardTitle>Exceptions</CardTitle>
              </CardHeader>
              <CardContent>
                <ExceptionsBody exceptionsQ={exceptionsQ} />
              </CardContent>
            </Card>
          ) : null}

          {policiesQ ? (
            <Card data-testid="policies-section">
              <CardHeader>
                <CardTitle>Policies</CardTitle>
              </CardHeader>
              <CardContent>
                <PoliciesBody policiesQ={policiesQ} />
              </CardContent>
            </Card>
          ) : null}
        </div>

        {/* Slice 384 (AC-26): the linked action plans. #2174: hidden with
            Risk. */}
        {showActionPlans ? (
          <LinkedActionPlans target="control" targetId={id} />
        ) : null}
      </ControlSection>
    </div>
  );
}

// MapPanel — the Framework map tab (#2140): the control through its SCF
// anchor to the framework requirements it satisfies. It is the only place
// the graph renders.
export function MapPanel({
  coverage,
  outOfScopeFvIds,
}: {
  coverage: ControlCoverage;
  outOfScopeFvIds: Set<string>;
}) {
  return (
    <Card data-testid="ucf-viz-section">
      <CardHeader className="border-b">
        <CardTitle>Framework map</CardTitle>
        <CardDescription>
          This control through the SCF anchor to the framework requirements it
          satisfies
        </CardDescription>
      </CardHeader>
      <CardContent>
        <UcfMiniViz coverage={coverage} outOfScopeFvIds={outOfScopeFvIds} />
      </CardContent>
    </Card>
  );
}

// HistoryPanel — the control's evaluation history, and the freshness
// clock that reads the same evaluated state.
export function HistoryPanel({
  historyQ,
  stateQ,
}: {
  historyQ: ReturnType<typeof useQuery<ControlHistoryResponse>>;
  stateQ: ReturnType<typeof useQuery<ControlStateResponse>>;
}) {
  return (
    <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
      <Card data-testid="history-tab-panel" className="lg:col-span-2">
        <CardHeader className="border-b">
          <CardTitle>Evaluation history</CardTitle>
          <CardDescription>
            One entry per scope cell per evaluation pass.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div data-testid="audit-log-section">
            <HistoryBody historyQ={historyQ} />
          </div>
        </CardContent>
      </Card>

      <Card data-testid="freshness-section">
        <CardHeader>
          <CardTitle>Freshness</CardTitle>
        </CardHeader>
        <CardContent>
          {stateQ.isLoading ? (
            <Skeleton className="h-24 w-full" />
          ) : stateQ.data ? (
            <FreshnessClock state={stateQ.data} />
          ) : (
            <p className="text-sm text-muted-foreground">
              No evaluation state available for this control yet.
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
