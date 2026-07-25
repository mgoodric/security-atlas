"use client";

// Slice 041 — control detail view (`/controls/[id]`).
// Slice 253 — re-pointed the four endpoint-pending placeholders.
// Slice 254 — re-foldered the page behind a sticky seven-tab strip
//   (Overview / Evidence / Mappings / Effective scope / Policies /
//   Risks / History). Tab state is URL-bound via `?tab=<name>` so
//   deep links resolve to the right tab; default is Overview when
//   the param is missing or unrecognised. The tab strip mirrors
//   `Plans/_archive/mockups/control.html` lines 139-152.
//
// Built per `Plans/_archive/mockups/control.html`. Renders, in mockup order:
//   - control header: CTRL id, SCF anchor pill, lifecycle badge, family,
//     title, owner/implementation/freshness-class meta
//   - KPI strip: effectiveness 30d, frameworks satisfied, evidence
//     records 30d (slice 253 — bound to GET /v1/evidence?control_id=…),
//     effective-scope cells
//   - tab strip: Overview / Evidence / Mappings / Effective scope /
//     Policies / Risks / History (slice 254)
//   - per-tab panels — see the Tabs / panel components below
//
// Slice 253 backstory: slice 041 shipped five "endpoint not on main yet"
// placeholders for the evidence-list KPI, the evidence-stream card, and
// the three right-rail Policies / Risks / Audit-log cards. Each named a
// specific upstream that DID land later (slice 106 for the evidence
// list; slice 064 for per-control history / policies / risks). The
// placeholders were not re-walked when the backends shipped, so the
// page denied real platform capability. Slice 253 binds all four to
// their live upstreams; empty states still render when the response is
// genuinely empty (fresh-install tenant, no linked policies, etc.) —
// the empty-state copy now reflects "no data" honestly, not "endpoint
// pending". No fabricated rows anywhere (anti-criterion P0-253-2).
//
// Slice 254 backstory: the page previously inlined every section on a
// single scroll. The mockup's information density assumes a tabbed
// view (each tab's content is page-sized once #253 wired real
// backends). The slice re-folders the rendered sections behind seven
// tabs without changing any data path or backend contract — anti-
// criterion P0-254-3: the Overview tab's data layout is preserved
// verbatim. All TanStack Query keys, fetchers, error branches, and
// data-testids from slices 152 / 253 / 255 / 256 / 257 are unchanged
// — slices that touched the page recently keep working because the
// re-folder is purely a render-shape change.
//
// Data sync: server values live ONLY in TanStack Query cache and are read
// during render — there is NO useEffect that seeds state from a server
// value (React 19 set-state-in-effect lint, slice 063 learned this). The
// single useEffect is the 401 -> /login redirect, matching the
// `catalog/scf/[id]` precedent exactly.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { CoverageTable } from "@/components/control/coverage-table";
import { FreshnessClock } from "@/components/control/freshness-clock";
import { EvidenceSummaryCard } from "@/components/control/evidence-summary";
import { GapExplanationCard } from "@/components/control/gap-explanation";

import { UcfMiniViz } from "@/components/control/ucf-mini-viz";

import {
  type ControlCoverage,
  type ControlHistoryResponse,
  type ControlLinkedPoliciesResponse,
  type ControlLinkedRisksResponse,
  type ControlStateResponse,
} from "@/lib/api/control-detail";
import { type EvidenceListResponse } from "@/lib/api/evidence";

import {
  EffectiveScopeRows,
  EvidenceStreamBody,
  HistoryBody,
  PoliciesBody,
  RisksBody,
} from "./bodies";

export function OverviewPanel({
  id,
  coverage,
  outOfScopeFvIds,
  evidenceQ,
  stateQ,
  state,
  fvIds,
  scopeQueries,
  requirements,
  policiesQ,
  risksQ,
  historyQ,
  anchorSCF,
}: {
  id: string;
  coverage: ControlCoverage;
  outOfScopeFvIds: Set<string>;
  evidenceQ: ReturnType<typeof useQuery<EvidenceListResponse>>;
  stateQ: ReturnType<typeof useQuery<ControlStateResponse>>;
  state: ControlStateResponse | undefined;
  fvIds: string[];
  scopeQueries: ReadonlyArray<{
    data?: unknown;
    isLoading: boolean;
    error: unknown;
  }>;
  requirements: ControlCoverage["requirements"];
  policiesQ: ReturnType<typeof useQuery<ControlLinkedPoliciesResponse>>;
  risksQ: ReturnType<typeof useQuery<ControlLinkedRisksResponse>>;
  historyQ: ReturnType<typeof useQuery<ControlHistoryResponse>>;
  anchorSCF: string | null;
}) {
  return (
    <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
      {/* LEFT 2/3 */}
      <div className="space-y-5 lg:col-span-2">
        {/* COVERAGE BY FRAMEWORK */}
        <Card data-testid="coverage-section">
          <CardHeader className="border-b">
            <CardTitle>Coverage by framework</CardTitle>
            <CardDescription>
              Routed through{" "}
              <span className="font-mono">{anchorSCF ?? "no anchor"}</span> ·
              weighted by STRM strength × 30-day effectiveness, intersected with
              FrameworkScope
            </CardDescription>
          </CardHeader>
          <CardContent>
            <CoverageTable
              requirements={requirements}
              outOfScopeFvIds={outOfScopeFvIds}
            />
          </CardContent>
        </Card>

        {/* UCF GRAPH MINI VIEW */}
        <Card data-testid="ucf-viz-section">
          <CardHeader className="border-b">
            <CardTitle>UCF graph · neighborhood</CardTitle>
            <CardDescription>
              This control through the SCF anchor to the framework requirements
              it satisfies
            </CardDescription>
          </CardHeader>
          <CardContent>
            <UcfMiniViz coverage={coverage} outOfScopeFvIds={outOfScopeFvIds} />
          </CardContent>
        </Card>

        {/* EVIDENCE STREAM — slice 253: bound to GET /v1/evidence?control_id=… */}
        <Card data-testid="evidence-stream-section">
          <CardHeader className="border-b">
            <CardTitle>Evidence stream · recent</CardTitle>
            <CardDescription>Append-only ledger · last 30 days</CardDescription>
          </CardHeader>
          <CardContent>
            <EvidenceStreamBody id={id} evidenceQ={evidenceQ} />
          </CardContent>
        </Card>
      </div>

      {/* RIGHT 1/3 */}
      <aside className="space-y-5 lg:col-span-1">
        {/* FRESHNESS CLOCK */}
        <Card data-testid="freshness-section">
          <CardHeader>
            <CardTitle>Freshness</CardTitle>
          </CardHeader>
          <CardContent>
            {stateQ.isLoading ? (
              <Skeleton className="h-24 w-full" />
            ) : state ? (
              <FreshnessClock state={state} />
            ) : (
              <p className="text-sm text-muted-foreground">
                No evaluation state available for this control yet.
              </p>
            )}
          </CardContent>
        </Card>

        {/* AI GAP-EXPLANATION — slice 444: non-binding, cited, local-Ollama
            plain-language explanation of the deterministic freshness rollup.
            Self-contained card (own query); renders the rollup always and the
            explanation when available + non-suppressed (AC-6/AC-7). */}
        <GapExplanationCard id={id} />

        {/* AI EVIDENCE-SUMMARY — slice 502: non-binding, cited,
            local-default-Ollama plain-language summary of the control's bounded
            CURRENT LIVE evidence set. Sibling of slice 444's gap-explanation.
            Self-contained card (own query); renders the bounded evidence label
            always and the summary when available + non-suppressed (AC-6/AC-7);
            no approve/publish/export affordance (AC-5/P0-502-3). */}
        <EvidenceSummaryCard id={id} />

        {/* EFFECTIVE SCOPE */}
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

        {/* LINKED POLICIES — slice 253: GET /v1/controls/{id}/policies */}
        <Card data-testid="policies-section">
          <CardHeader>
            <CardTitle>Policies</CardTitle>
          </CardHeader>
          <CardContent>
            <PoliciesBody policiesQ={policiesQ} />
          </CardContent>
        </Card>

        {/* LINKED RISKS — slice 253: GET /v1/controls/{id}/risks */}
        <Card data-testid="risks-section">
          <CardHeader>
            <CardTitle>Risks treated</CardTitle>
          </CardHeader>
          <CardContent>
            <RisksBody risksQ={risksQ} />
          </CardContent>
        </Card>

        {/* AUDIT LOG — slice 253: GET /v1/controls/{id}/history */}
        <Card data-testid="audit-log-section">
          <CardHeader>
            <CardTitle>Audit log</CardTitle>
          </CardHeader>
          <CardContent>
            <HistoryBody historyQ={historyQ} />
          </CardContent>
        </Card>
      </aside>
    </div>
  );
}

// EvidencePanel — slice 254 AC-4: the Evidence tab is the full evidence-
// stream pane. Today the Overview's stream and the Evidence tab share
// the same TanStack query (EVIDENCE_STREAM_LIMIT = 5 rows); the full
// paginated browse experience lives at `/evidence?control_id=<id>`,
// which the panel surfaces prominently. When a future slice paginates
// in-line on the Evidence tab, the panel grows here without touching
// Overview.
export function EvidencePanel({
  id,
  evidenceQ,
}: {
  id: string;
  evidenceQ: ReturnType<typeof useQuery<EvidenceListResponse>>;
}) {
  return (
    <Card data-testid="evidence-tab-panel">
      <CardHeader className="border-b">
        <CardTitle>Evidence stream</CardTitle>
        <CardDescription>
          Append-only ledger · last 30 days · the dedicated{" "}
          <Link
            href={`/evidence?control_id=${encodeURIComponent(id)}`}
            className="underline"
            data-testid="evidence-tab-list-link"
          >
            /evidence
          </Link>{" "}
          list page is the full paginated browse surface.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <EvidenceStreamBody id={id} evidenceQ={evidenceQ} />
      </CardContent>
    </Card>
  );
}

// MappingsPanel — slice 254 AC-5: the deep-dive view for the
// control's STRM edges. Today the surface is the same coverage table +
// UCF mini-viz that shipped on Overview pre-tab-strip; the per-
// requirement inspector + edge-level drilldown lands in a follow-up
// slice (the chevron in the coverage table is already wired as
// non-interactive per slice 256 D2).
export function MappingsPanel({
  coverage,
  outOfScopeFvIds,
  anchorSCF,
}: {
  coverage: ControlCoverage;
  outOfScopeFvIds: Set<string>;
  anchorSCF: string | null;
}) {
  return (
    <div className="space-y-5" data-testid="mappings-tab-panel">
      <Card>
        <CardHeader className="border-b">
          <CardTitle>Coverage by framework</CardTitle>
          <CardDescription>
            Routed through{" "}
            <span className="font-mono">{anchorSCF ?? "no anchor"}</span> ·
            weighted by STRM strength × 30-day effectiveness, intersected with
            FrameworkScope
          </CardDescription>
        </CardHeader>
        <CardContent>
          <CoverageTable
            requirements={coverage.requirements}
            outOfScopeFvIds={outOfScopeFvIds}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b">
          <CardTitle>UCF graph · neighborhood</CardTitle>
          <CardDescription>
            This control through the SCF anchor to the framework requirements it
            satisfies
          </CardDescription>
        </CardHeader>
        <CardContent>
          <UcfMiniViz coverage={coverage} outOfScopeFvIds={outOfScopeFvIds} />
        </CardContent>
      </Card>
    </div>
  );
}

// ScopePanel — slice 254 AC-6: per-framework effective-scope
// breakdown. Same fan-out the Overview right-rail consumes, here
// rendered as a full-width card so each framework's row has room for
// the cells/applicable totals + the out-of-scope reason copy.
export function ScopePanel({
  fvIds,
  scopeQueries,
  requirements,
}: {
  fvIds: string[];
  scopeQueries: ReadonlyArray<{
    data?: unknown;
    isLoading: boolean;
    error: unknown;
  }>;
  requirements: ControlCoverage["requirements"];
}) {
  return (
    <Card data-testid="scope-tab-panel">
      <CardHeader className="border-b">
        <CardTitle>Effective scope · per framework</CardTitle>
        <CardDescription>
          applicability ∩ FrameworkScope — one call per framework version
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <EffectiveScopeRows
          fvIds={fvIds}
          scopeQueries={scopeQueries}
          requirements={requirements}
        />
      </CardContent>
    </Card>
  );
}

// PoliciesPanel — slice 254 AC-7: full Policies list. Shares the same
// query the Overview right-rail card consumes; renders the same rows
// in a wider full-width layout.
export function PoliciesPanel({
  policiesQ,
}: {
  policiesQ: ReturnType<typeof useQuery<ControlLinkedPoliciesResponse>>;
}) {
  return (
    <Card data-testid="policies-tab-panel">
      <CardHeader className="border-b">
        <CardTitle>Linked policies</CardTitle>
        <CardDescription>Policies linked to this control.</CardDescription>
      </CardHeader>
      <CardContent>
        <PoliciesBody policiesQ={policiesQ} />
      </CardContent>
    </Card>
  );
}

// RisksPanel — slice 254 AC-7: full Risks list. Same query as the
// right-rail; renders the link-weight detail per row that the
// right-rail card already surfaces.
export function RisksPanel({
  risksQ,
}: {
  risksQ: ReturnType<typeof useQuery<ControlLinkedRisksResponse>>;
}) {
  return (
    <Card data-testid="risks-tab-panel">
      <CardHeader className="border-b">
        <CardTitle>Risks treated</CardTitle>
        <CardDescription>
          Risks this control treats via the risk_control_links table (slice
          020).
        </CardDescription>
      </CardHeader>
      <CardContent>
        <RisksBody risksQ={risksQ} />
      </CardContent>
    </Card>
  );
}

// HistoryPanel — slice 254 AC-7: full evaluation history. Same query
// as the right-rail audit-log card; the tab gives the list room to
// breathe + makes deep-linking to the History tab trivial. Paginated
// keyset history is a follow-up slice (the BFF returns one page; the
// dedicated tab makes that paged surface easy to extend without
// touching Overview).
export function HistoryPanel({
  historyQ,
}: {
  historyQ: ReturnType<typeof useQuery<ControlHistoryResponse>>;
}) {
  return (
    <Card data-testid="history-tab-panel">
      <CardHeader className="border-b">
        <CardTitle>Evaluation history</CardTitle>
        <CardDescription>
          Per-evaluation history rows — one entry per scope cell per evaluation
          pass.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <HistoryBody historyQ={historyQ} />
      </CardContent>
    </Card>
  );
}

// =================== SHARED PANEL BODIES ===================

// EvidenceStreamBody — extracted so the Overview card and the
// Evidence tab render identical content from the same query.
