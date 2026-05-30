"use client";

// Slice 041 — control detail view (`/controls/[id]`).
// Slice 253 — re-pointed the four endpoint-pending placeholders.
// Slice 254 — re-foldered the page behind a sticky seven-tab strip
//   (Overview / Evidence / Mappings / Effective scope / Policies /
//   Risks / History). Tab state is URL-bound via `?tab=<name>` so
//   deep links resolve to the right tab; default is Overview when
//   the param is missing or unrecognised. The tab strip mirrors
//   `Plans/mockups/control.html` lines 139-152.
//
// Built per `Plans/mockups/control.html`. Renders, in mockup order:
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

import { useQueries, useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
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
import { ControlHeaderActions } from "@/components/control/header-actions";
import { UcfMiniViz } from "@/components/control/ucf-mini-viz";
import { cn } from "@/lib/utils";
import {
  fetchControlCoverage,
  fetchControlEffectiveScope,
  fetchControlEffectiveness,
  fetchControlHistory,
  fetchControlPolicies,
  fetchControlRisks,
  fetchControlState,
  type ControlCoverage,
  type ControlHistoryResponse,
  type ControlLinkedPoliciesResponse,
  type ControlLinkedRisksResponse,
  type ControlStateResponse,
  type EffectiveScopeResponse,
} from "@/lib/api/control-detail";
import {
  fetchEvidenceList,
  type EvidenceListResponse,
} from "@/lib/api/evidence";
import { formatResidualScore } from "@/app/(authed)/risks/filters";

import { classifyControlDetailError } from "../error-classifier";
import { CONTROL_TABS, formatTabCount, isTabKey, type TabKey } from "./tabs";

// Slice 253 — page size for the evidence stream card. Five rows mirrors
// the mockup (`Plans/mockups/control.html` lines 389-440). The KPI sub-
// text reads "in last 30 days" (the upstream default window) — when the
// stream's count maxes at the limit AND a next_cursor is present we
// surface a "<limit>+ in last 30 days" hint rather than fabricating a
// total the backend never returned. The dedicated /evidence list page
// is the destination for full paginated browsing.
const EVIDENCE_STREAM_LIMIT = 5;

// Slice 254 — Tab strip definitions + count formatting live in
// `./tabs.ts` for vitest unit-test coverage. See that file for the
// AC-2 + D3 rules around chip rendering. The page re-uses the
// literal-union TabKey + `isTabKey` validator so URL hydration and
// panel-selection share one source of truth.

// ControlDetailPageInner holds the entire client-side body. The outer
// default export wraps this in Suspense so `useSearchParams` is
// satisfied per Next 16's App Router strict-mode contract (the
// calendar page sets the same precedent — see
// `web/app/(authed)/calendar/page.tsx`).
function ControlDetailPageInner() {
  const router = useRouter();
  const { id } = useParams<{ id: string }>();
  const searchParams = useSearchParams();

  // Slice 254 — read the active tab from `?tab=<key>`. Unknown /
  // missing values default to "overview" (D2). The tab setter writes
  // back through router.replace so the browser back/forward stack
  // navigates between tabs without re-mounting the page (AC-8).
  const rawTab = searchParams.get("tab");
  const activeTab: TabKey = isTabKey(rawTab) ? rawTab : "overview";

  const setTab = useCallback(
    (next: TabKey) => {
      const params = new URLSearchParams(searchParams.toString());
      if (next === "overview") {
        // Default tab — strip the param so the canonical URL on first
        // visit stays clean (`/controls/<id>` rather than
        // `/controls/<id>?tab=overview`).
        params.delete("tab");
      } else {
        params.set("tab", next);
      }
      const qs = params.toString();
      const href = qs
        ? `/controls/${encodeURIComponent(id)}?${qs}`
        : `/controls/${encodeURIComponent(id)}`;
      // router.replace (vs push) — clicking a tab is not a navigation
      // event the back-button should treat as a separate page; the
      // browser back stack should still navigate between tabs via the
      // hash-style replace contract. This mirrors the calendar page's
      // search-param mutation pattern.
      router.replace(href, { scroll: false });
    },
    [id, router, searchParams],
  );

  const coverageQ = useQuery<ControlCoverage>({
    queryKey: ["control", id, "coverage"],
    queryFn: () => fetchControlCoverage(id),
    enabled: Boolean(id),
  });

  const stateQ = useQuery({
    queryKey: ["control", id, "state"],
    queryFn: () => fetchControlState(id),
    enabled: Boolean(id),
  });

  const effectivenessQ = useQuery({
    queryKey: ["control", id, "effectiveness"],
    queryFn: () => fetchControlEffectiveness(id),
    enabled: Boolean(id),
  });

  // Slice 253 — bind the four formerly-"endpoint pending" surfaces to
  // their real upstreams. Errors are tolerated independently: a 404 on,
  // say, `/policies` should NOT mask the rest of the page; the per-card
  // empty-state still renders honestly. Only `coverageQ`'s error drives
  // the page-level branch (the page makes no sense without a control to
  // anchor to).
  const evidenceQ = useQuery({
    queryKey: ["control", id, "evidence", EVIDENCE_STREAM_LIMIT],
    queryFn: () =>
      fetchEvidenceList({ controlID: id, limit: EVIDENCE_STREAM_LIMIT }),
    enabled: Boolean(id),
  });

  const policiesQ = useQuery({
    queryKey: ["control", id, "policies"],
    queryFn: () => fetchControlPolicies(id),
    enabled: Boolean(id),
  });

  const risksQ = useQuery({
    queryKey: ["control", id, "risks"],
    queryFn: () => fetchControlRisks(id),
    enabled: Boolean(id),
  });

  const historyQ = useQuery({
    queryKey: ["control", id, "history"],
    queryFn: () => fetchControlHistory(id),
    enabled: Boolean(id),
  });

  // Distinct framework_version_ids from the coverage requirements drive
  // the effective-scope fan-out — one /effective-scope call per framework
  // version (slice 018 takes a single framework_version UUID per call).
  const fvIds = distinctFrameworkVersionIds(coverageQ.data);
  const scopeQueries = useQueries({
    queries: fvIds.map((fvId) => ({
      queryKey: ["control", id, "effective-scope", fvId],
      queryFn: () => fetchControlEffectiveScope(id, fvId),
      enabled: Boolean(id) && fvIds.length > 0,
    })),
  });

  // Slice 152: error-class discriminates `notfound` (empty-state),
  // `unauthorized` (/login redirect), `other` (destructive Alert).
  // The classifier is pure logic + vitest-covered (8 cases) so a
  // regression that mis-routes 404 vs 5xx fails before merge. Full
  // trail: docs/adr/0004-control-detail-404-empty-state.md.
  const firstError =
    coverageQ.error ?? stateQ.error ?? effectivenessQ.error ?? null;
  const errorClass = classifyControlDetailError(firstError);
  useEffect(() => {
    if (errorClass === "unauthorized") {
      router.push(`/login?from=/controls/${id}`);
    }
  }, [errorClass, router, id]);

  if (coverageQ.isLoading) {
    return (
      <div className="space-y-4" data-testid="control-detail-loading">
        <Skeleton className="h-32 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  // Slice 152 D1-c: 404 on the coverage call means the id in the URL
  // does not resolve to a tenant control. On a fresh install the most
  // common cause is that the operator clicked an SCF anchor row from
  // the /controls list (which renders ~1,400 catalog-global anchors via
  // /v1/anchors) whose anchor.id has no instantiated control in their
  // tenant. The friendly empty-state names the cause honestly — slice
  // 150 D3 pinned that bare-{id} 404 is a load-bearing PLATFORM
  // contract; the UI's job is to render it humanely, not to convert it
  // to 200.
  if (coverageQ.error && errorClass === "notfound") {
    return (
      <div
        className="rounded-xl border bg-card py-16 px-6 text-center"
        data-testid="control-detail-empty"
      >
        <div className="mx-auto mb-3 text-muted-foreground">
          <svg
            className="w-12 h-12 mx-auto"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            aria-hidden
          >
            <path
              d="M9 3.75H6.912a2.25 2.25 0 00-2.15 1.588L2.35 13.177a2.25 2.25 0 00-.1.661V18a2.25 2.25 0 002.25 2.25h15a2.25 2.25 0 002.25-2.25v-4.162c0-.224-.034-.447-.1-.661L19.24 5.338a2.25 2.25 0 00-2.15-1.588H15M2.25 13.5h3.86a2.25 2.25 0 012.012 1.244l.256.512a2.25 2.25 0 002.013 1.244h3.218a2.25 2.25 0 002.013-1.244l.256-.512a2.25 2.25 0 012.013-1.244h3.859"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </div>
        <div className="text-sm font-semibold text-foreground mb-1">
          This SCF anchor has no control instantiated in your tenant yet
        </div>
        <div className="text-xs text-muted-foreground mb-4">
          The id <span className="font-mono">{id}</span> resolves in the global
          SCF catalog but no tenant control is bound to it. This is the expected
          state on a fresh install — controls are tenant-scoped and authored
          separately from the catalog.
        </div>
        <Link
          href="/controls"
          className={buttonVariants({ size: "sm" })}
          data-testid="control-detail-empty-cta"
        >
          Back to controls list
        </Link>
      </div>
    );
  }

  if (coverageQ.error && errorClass === "other") {
    return (
      <Alert variant="destructive" data-testid="control-detail-error">
        <AlertTitle>Could not load control</AlertTitle>
        <AlertDescription>
          {(coverageQ.error as Error).message}
        </AlertDescription>
      </Alert>
    );
  }

  if (!coverageQ.data) {
    return null;
  }

  // out-of-scope set: a framework_version is out of scope when its
  // effective-scope call resolved with in_scope === false. Computed
  // here (after the early-return branches above) so the empty-state +
  // destructive-error paths don't pay for an iteration that their
  // render does not consume.
  const outOfScopeFvIds = new Set<string>();
  scopeQueries.forEach((q) => {
    const data = q.data as EffectiveScopeResponse | undefined;
    if (data && data.in_scope === false) {
      outOfScopeFvIds.add(data.framework_version_id);
    }
  });

  const coverage = coverageQ.data;
  const { control, anchor, requirements } = coverage;
  const effectiveness = effectivenessQ.data;
  const state = stateQ.data;

  const frameworksSatisfied = fvIds.length;
  const inScopeFrameworks = fvIds.length - outOfScopeFvIds.size;

  // Slice 254 — tab-count chips read from each tab's underlying query
  // payload. AC-2: when a query is loading or errored, the chip
  // renders "—" (not a placeholder integer). History has no chip per
  // the mockup (line 149 — `History` with no trailing count).
  //
  // Evidence chip semantics: the page's evidence query is bounded to
  // EVIDENCE_STREAM_LIMIT (5 rows) for the Overview-tab stream card,
  // so `count` is at most 5 and `next_cursor` indicates "more
  // available". When more rows exist the chip surfaces "5+" rather
  // than fabricating a higher total the backend hasn't returned. The
  // /evidence list page is the authoritative count surface (slice 236
  // exposes the tenant-wide total).
  const tabCounts: Record<Exclude<TabKey, "overview" | "history">, string> = {
    evidence: evidenceCount(
      evidenceQ.data,
      evidenceQ.isLoading,
      evidenceQ.error,
    ),
    mappings:
      coverageQ.isLoading || coverageQ.error
        ? "—"
        : formatTabCount(requirements.length),
    scope:
      scopeQueries.some((q) => q.isLoading) || scopeQueries.some((q) => q.error)
        ? "—"
        : formatTabCount(scopeCellSum(scopeQueries)),
    policies: policiesCount(
      policiesQ.data,
      policiesQ.isLoading,
      policiesQ.error,
    ),
    risks: risksCount(risksQ.data, risksQ.isLoading, risksQ.error),
  };

  return (
    <div className="space-y-6" data-testid="control-detail">
      {/* breadcrumb */}
      <div className="text-sm">
        <Link
          href="/controls"
          className="text-muted-foreground hover:underline"
        >
          ← All controls
        </Link>
      </div>

      {/* ============ CONTROL HEADER ============ */}
      {/* Slice 255 — header now splits into a left "title + meta" well
          and a right "action buttons + last-evaluated" well. The flex
          row collapses to a stacked column at mobile widths so the
          action well drops below the title rather than wrapping
          mid-button-row. */}
      <header
        className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between"
        data-testid="control-header"
      >
        <div className="min-w-0 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs text-muted-foreground">
              {control.bundle_id}
              {control.version ? ` · v${control.version}` : ""}
            </span>
            {anchor ? (
              <Link
                href={`/catalog/scf/${encodeURIComponent(anchor.id)}`}
                className="inline-flex items-center gap-1 rounded bg-primary/10 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-primary hover:bg-primary/20"
                data-testid="scf-anchor-pill"
              >
                {anchor.scf_id}
              </Link>
            ) : (
              <span
                className="font-mono text-[11px] text-muted-foreground"
                data-testid="scf-anchor-pill"
              >
                unanchored
              </span>
            )}
            <Badge
              variant="secondary"
              data-testid="lifecycle-badge"
              className="capitalize"
            >
              {control.lifecycle_state}
            </Badge>
            <span className="text-xs text-muted-foreground">
              {control.control_family}
            </span>
          </div>

          <h1
            className="text-2xl font-semibold tracking-tight"
            data-testid="control-title"
          >
            {control.title}
          </h1>

          <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-sm">
            <span className="text-muted-foreground">
              Owner role{" "}
              <span className="text-foreground">{control.owner_role}</span>
            </span>
            <span className="text-muted-foreground">
              Implementation{" "}
              <span className="text-foreground">
                {control.implementation_type}
              </span>
            </span>
            <span className="text-muted-foreground">
              Freshness class{" "}
              <span className="font-mono text-foreground">
                {control.freshness_class ?? "—"}
              </span>
            </span>
          </div>
        </div>

        {/* Slice 255 — header actions well: Run query + Edit YAML +
            Request exception, with "last evaluated <relative-time>"
            sub-line. See web/components/control/header-actions.tsx for
            the JUDGMENT decisions on disabled-vs-link semantics per
            button. */}
        <ControlHeaderActions controlID={id} state={state} />
      </header>

      {/* ============ KPI STRIP ============ */}
      <div
        className="grid grid-cols-2 gap-3 lg:grid-cols-4"
        data-testid="kpi-strip"
      >
        <KpiCard
          label="Effectiveness · 30d"
          value={effectiveness ? effectiveness.pass_rate.toFixed(2) : "—"}
          sub={
            effectiveness
              ? `${effectiveness.pass_count}/${effectiveness.total_count} evaluations`
              : effectivenessQ.isLoading
                ? "loading…"
                : "no data"
          }
        />
        <KpiCard
          label="Frameworks satisfied"
          value={String(frameworksSatisfied)}
          sub="via SCF anchor"
        />
        <KpiCard
          label="Evidence records · 30d"
          value={
            evidenceQ.isLoading
              ? "…"
              : evidenceQ.data
                ? evidenceQ.data.next_cursor
                  ? `${evidenceQ.data.count}+`
                  : String(evidenceQ.data.count)
                : "—"
          }
          sub={
            evidenceQ.isLoading
              ? "loading…"
              : evidenceQ.error
                ? "error"
                : "in last 30 days"
          }
        />
        <KpiCard
          label="In-scope frameworks"
          value={
            scopeQueries.some((q) => q.isLoading)
              ? "…"
              : String(inScopeFrameworks)
          }
          sub={`of ${frameworksSatisfied} mapped`}
        />
      </div>

      {/* ============ TAB STRIP ============ */}
      {/* Slice 254 — sticky seven-tab strip per mockup
          (Plans/mockups/control.html lines 139-152). The strip uses
          the slice 044 inlined-tab-list pattern (the codebase has no
          shared tabs primitive; introducing one would violate
          P0-254-1's "do not introduce a new component primitive"). The
          tab key is URL-bound (`?tab=<key>`) and the count chip reads
          from each tab's underlying query payload. */}
      <div
        role="tablist"
        aria-label="Control detail sections"
        data-testid="control-tabs"
        className="sticky top-12 z-10 -mx-4 flex items-center gap-1 overflow-x-auto border-b bg-background px-4 md:mx-0 md:px-0"
      >
        {CONTROL_TABS.map((t) => {
          const isActive = activeTab === t.key;
          const chip =
            t.key === "overview" || t.key === "history"
              ? null
              : tabCounts[t.key as Exclude<TabKey, "overview" | "history">];
          return (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={isActive}
              aria-controls={`control-tab-panel-${t.key}`}
              id={`control-tab-${t.key}`}
              data-testid={`control-tab-${t.key}`}
              onClick={() => setTab(t.key)}
              className={cn(
                "-mb-px flex shrink-0 items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-3 text-sm transition-colors",
                isActive
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              <span>{t.label}</span>
              {chip ? (
                <span
                  className="font-mono text-xs text-muted-foreground"
                  data-testid={`control-tab-${t.key}-chip`}
                >
                  {chip}
                </span>
              ) : null}
            </button>
          );
        })}
      </div>

      {/* ============ TAB PANELS ============ */}
      {/* Slice 254 — panels are conditionally rendered (no `hidden`-
          class mounting like slice 044's audit workspace) because the
          control-detail page does NOT have draft state that must
          survive tab flips. Switching tabs unmounts the previous
          panel's subtree; TanStack Query keeps the data in cache so
          flipping back is instant. */}
      <div
        role="tabpanel"
        id={`control-tab-panel-${activeTab}`}
        aria-labelledby={`control-tab-${activeTab}`}
        data-testid={`control-tab-panel-${activeTab}`}
      >
        {activeTab === "overview" ? (
          <OverviewPanel
            id={id}
            coverage={coverage}
            outOfScopeFvIds={outOfScopeFvIds}
            evidenceQ={evidenceQ}
            stateQ={stateQ}
            state={state}
            fvIds={fvIds}
            scopeQueries={scopeQueries}
            requirements={requirements}
            policiesQ={policiesQ}
            risksQ={risksQ}
            historyQ={historyQ}
            anchorSCF={anchor ? anchor.scf_id : null}
          />
        ) : null}

        {activeTab === "evidence" ? (
          <EvidencePanel id={id} evidenceQ={evidenceQ} />
        ) : null}

        {activeTab === "mappings" ? (
          <MappingsPanel
            coverage={coverage}
            outOfScopeFvIds={outOfScopeFvIds}
            anchorSCF={anchor ? anchor.scf_id : null}
          />
        ) : null}

        {activeTab === "scope" ? (
          <ScopePanel
            fvIds={fvIds}
            scopeQueries={scopeQueries}
            requirements={requirements}
          />
        ) : null}

        {activeTab === "policies" ? (
          <PoliciesPanel policiesQ={policiesQ} />
        ) : null}

        {activeTab === "risks" ? <RisksPanel risksQ={risksQ} /> : null}

        {activeTab === "history" ? <HistoryPanel historyQ={historyQ} /> : null}
      </div>
    </div>
  );
}

export default function ControlDetailPage() {
  // Slice 254 — useSearchParams requires Suspense in Next.js 16 App
  // Router strict mode. The fallback is a small skeleton so the page
  // shell still shows something during the brief client boot. Pattern
  // borrowed from `web/app/(authed)/calendar/page.tsx`.
  return (
    <Suspense
      fallback={
        <div className="space-y-4" data-testid="control-detail-loading">
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      }
    >
      <ControlDetailPageInner />
    </Suspense>
  );
}

// distinctFrameworkVersionIds pulls the unique framework_version_id set
// out of the coverage requirements, preserving first-seen order so the
// effective-scope rail is stable across renders.
function distinctFrameworkVersionIds(
  coverage: ControlCoverage | undefined,
): string[] {
  if (!coverage) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const r of coverage.requirements) {
    if (!seen.has(r.framework_version_id)) {
      seen.add(r.framework_version_id);
      out.push(r.framework_version_id);
    }
  }
  return out;
}

// scopeCellSum totals the effective-scope cell counts across all
// resolved framework-version scope queries. Used for the "Effective
// scope" tab's count chip. Errors / loading rows contribute 0 — the
// chip logic upstream renders "—" when ANY query is unresolved, so
// this sum only runs in the fully-resolved branch.
function scopeCellSum(scopeQueries: ReadonlyArray<{ data?: unknown }>): number {
  let total = 0;
  for (const q of scopeQueries) {
    const d = q.data as EffectiveScopeResponse | undefined;
    if (d) total += d.effective_scope_count;
  }
  return total;
}

function evidenceCount(
  data: EvidenceListResponse | undefined,
  isLoading: boolean,
  error: unknown,
): string {
  if (isLoading || error) return "—";
  if (!data) return "—";
  // The Overview-tab evidence card pages at EVIDENCE_STREAM_LIMIT
  // rows; when more exist the chip surfaces "5+" rather than
  // fabricating a higher total. The Evidence tab itself reads the
  // same payload and renders the same hint; the /evidence list page
  // is the authoritative tenant-wide count surface.
  if (data.next_cursor) return `${data.count}+`;
  return formatTabCount(data.count);
}

function policiesCount(
  data: ControlLinkedPoliciesResponse | undefined,
  isLoading: boolean,
  error: unknown,
): string {
  if (isLoading || error || !data) return "—";
  return formatTabCount(data.count);
}

function risksCount(
  data: ControlLinkedRisksResponse | undefined,
  isLoading: boolean,
  error: unknown,
): string {
  if (isLoading || error || !data) return "—";
  return formatTabCount(data.count);
}

function KpiCard({
  label,
  value,
  sub,
}: {
  label: string;
  value: string;
  sub: string;
}) {
  return (
    <Card size="sm" data-testid="kpi-card">
      <CardContent>
        <div className="text-[11px] uppercase tracking-wider text-muted-foreground">
          {label}
        </div>
        <div className="mt-1 text-2xl font-semibold">{value}</div>
        <div className="mt-0.5 text-xs text-muted-foreground">{sub}</div>
      </CardContent>
    </Card>
  );
}

// =================== TAB PANELS ===================

// OverviewPanel — Plans/mockups/control.html shows the Overview tab as
// the "everything-at-a-glance" surface (lines 156-560). Slice 254
// preserves the exact two-column layout that shipped pre-tab-strip
// (anti-criterion P0-254-3): coverage + UCF graph + evidence stream
// on the left two-thirds, freshness + effective scope + policies +
// risks + audit log on the right one-third. Each card's data is
// shared across tabs (the same TanStack Query keys back the
// dedicated-tab views), so flipping tabs is free.
function OverviewPanel({
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
function EvidencePanel({
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
function MappingsPanel({
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
function ScopePanel({
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
function PoliciesPanel({
  policiesQ,
}: {
  policiesQ: ReturnType<typeof useQuery<ControlLinkedPoliciesResponse>>;
}) {
  return (
    <Card data-testid="policies-tab-panel">
      <CardHeader className="border-b">
        <CardTitle>Linked policies</CardTitle>
        <CardDescription>
          Policies linked to this control via the policy_control_links table
          (slice 020).
        </CardDescription>
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
function RisksPanel({
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
function HistoryPanel({
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
function EvidenceStreamBody({
  id,
  evidenceQ,
}: {
  id: string;
  evidenceQ: ReturnType<typeof useQuery<EvidenceListResponse>>;
}) {
  if (evidenceQ.isLoading) return <Skeleton className="h-24 w-full" />;
  if (evidenceQ.error) {
    return (
      <Alert variant="destructive" data-testid="evidence-stream-error">
        <AlertTitle>Could not load evidence stream</AlertTitle>
        <AlertDescription>
          {(evidenceQ.error as Error).message}
        </AlertDescription>
      </Alert>
    );
  }
  if (evidenceQ.data && evidenceQ.data.evidence.length > 0) {
    return (
      <div
        className="divide-y divide-border"
        data-testid="evidence-stream-list"
      >
        {evidenceQ.data.evidence.map((rec) => (
          <EvidenceStreamRow
            key={rec.evidence_id}
            evidenceID={rec.evidence_id}
            observedAt={rec.observed_at}
            kind={rec.evidence_kind}
            source={rec.source}
            result={rec.result}
          />
        ))}
        {evidenceQ.data.next_cursor ? (
          <div className="pt-3 text-center text-xs text-muted-foreground">
            <Link
              href={`/evidence?control_id=${encodeURIComponent(id)}`}
              className="hover:underline"
              data-testid="evidence-stream-view-all"
            >
              View all records in last 30 days →
            </Link>
          </div>
        ) : null}
      </div>
    );
  }
  return (
    <p
      className="text-sm text-muted-foreground"
      data-testid="evidence-stream-empty"
    >
      No evidence records for this control in the last 30 days.
    </p>
  );
}

function EffectiveScopeRows({
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
  if (fvIds.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No mapped frameworks, so no effective scope to compute.
      </p>
    );
  }
  return (
    <>
      {fvIds.map((fvId, i) => {
        const q = scopeQueries[i];
        const data = q?.data as EffectiveScopeResponse | undefined;
        const reqForFv = requirements.find(
          (r) => r.framework_version_id === fvId,
        );
        const label = reqForFv
          ? `${reqForFv.framework_name} ${reqForFv.framework_version}`
          : fvId;
        return (
          <div key={fvId} data-testid="effective-scope-row">
            <div className="flex items-center justify-between text-sm">
              <span className="text-foreground">{label}</span>
              <span className="font-mono text-xs">
                {q?.isLoading
                  ? "…"
                  : data
                    ? `${data.effective_scope_count} cells`
                    : "error"}
              </span>
            </div>
            {data && !data.in_scope ? (
              <div className="mt-0.5 text-[11px] text-muted-foreground">
                out of scope
                {data.out_of_scope_reason
                  ? ` — ${data.out_of_scope_reason}`
                  : ""}
              </div>
            ) : null}
          </div>
        );
      })}
    </>
  );
}

function PoliciesBody({
  policiesQ,
}: {
  policiesQ: ReturnType<typeof useQuery<ControlLinkedPoliciesResponse>>;
}) {
  if (policiesQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (policiesQ.error) {
    return (
      <p className="text-sm text-destructive" data-testid="policies-error">
        Could not load linked policies: {(policiesQ.error as Error).message}
      </p>
    );
  }
  if (policiesQ.data && policiesQ.data.policies.length > 0) {
    return (
      <ul
        className="divide-y divide-border text-sm"
        data-testid="policies-list"
      >
        {policiesQ.data.policies.map((p) => (
          <li
            key={p.policy_id}
            className="flex items-center justify-between py-2"
            data-testid="policy-row"
          >
            <div className="min-w-0">
              <Link
                href={`/policies/${encodeURIComponent(p.policy_id)}`}
                className="text-foreground hover:underline"
              >
                {p.title}
              </Link>
              <div className="font-mono text-[11px] text-muted-foreground">
                {p.version}
              </div>
            </div>
            <Badge variant="secondary" className="capitalize">
              {p.status}
            </Badge>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <p className="text-sm text-muted-foreground" data-testid="policies-empty">
      No policies are linked to this control.
    </p>
  );
}

function RisksBody({
  risksQ,
}: {
  risksQ: ReturnType<typeof useQuery<ControlLinkedRisksResponse>>;
}) {
  if (risksQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (risksQ.error) {
    return (
      <p className="text-sm text-destructive" data-testid="risks-error">
        Could not load linked risks: {(risksQ.error as Error).message}
      </p>
    );
  }
  if (risksQ.data && risksQ.data.risks.length > 0) {
    return (
      <ul className="divide-y divide-border text-sm" data-testid="risks-list">
        {risksQ.data.risks.map((r) => (
          <li key={r.risk_id} className="py-2" data-testid="risk-row">
            <div className="flex items-center justify-between">
              <Link
                href={`/risks/${encodeURIComponent(r.risk_id)}`}
                className="min-w-0 text-foreground hover:underline"
              >
                {r.title}
              </Link>
              <span className="font-mono text-xs text-muted-foreground">
                {formatResidualScore(r.residual_score)} residual
              </span>
            </div>
            {typeof r.link_weight === "number" ? (
              <div className="font-mono text-[11px] text-muted-foreground">
                link weight {r.link_weight.toFixed(2)}
              </div>
            ) : null}
          </li>
        ))}
      </ul>
    );
  }
  return (
    <p className="text-sm text-muted-foreground" data-testid="risks-empty">
      No risks are linked to this control.
    </p>
  );
}

function HistoryBody({
  historyQ,
}: {
  historyQ: ReturnType<typeof useQuery<ControlHistoryResponse>>;
}) {
  if (historyQ.isLoading) return <Skeleton className="h-16 w-full" />;
  if (historyQ.error) {
    return (
      <p className="text-sm text-destructive" data-testid="audit-log-error">
        Could not load audit log: {(historyQ.error as Error).message}
      </p>
    );
  }
  if (historyQ.data && historyQ.data.history.length > 0) {
    return (
      <ul className="space-y-1.5 text-xs" data-testid="audit-log-list">
        {historyQ.data.history.map((h, i) => (
          <li
            key={`${h.evaluated_at}-${i}`}
            className="text-muted-foreground"
            data-testid="audit-log-entry"
          >
            <span className="font-mono text-muted-foreground">
              {formatHistoryDate(h.evaluated_at)}
            </span>{" "}
            ·{" "}
            <span className="text-foreground">
              evaluated <span className="font-mono">{h.computed_state}</span>
            </span>{" "}
            <span className="text-muted-foreground">
              ({h.evidence_count} evidence ·{" "}
              <span className="font-mono">{h.freshness_status}</span>)
            </span>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <p className="text-sm text-muted-foreground" data-testid="audit-log-empty">
      No evaluation history yet for this control.
    </p>
  );
}

// Slice 253 — one row of the evidence-stream card. Mirrors the mockup
// (`Plans/mockups/control.html` lines 389-440): a result dot, the
// observed_at timestamp, a summary line (the evidence_kind + the
// connector/actor tag), and a pass/fail/na/inconclusive badge. The
// `source` JSONB is rendered as a brief tag — `{actor_type}/{actor_id}`
// when present — rather than fabricated narrative copy.
function EvidenceStreamRow({
  evidenceID,
  observedAt,
  kind,
  source,
  result,
}: {
  evidenceID: string;
  observedAt: string;
  kind: string | null;
  source: Record<string, unknown> | null;
  result: "pass" | "fail" | "na" | "inconclusive";
}) {
  const dotClass =
    result === "pass"
      ? "bg-emerald-500"
      : result === "fail"
        ? "bg-rose-500"
        : "bg-muted-foreground";
  const resultClass =
    result === "pass"
      ? "text-emerald-700"
      : result === "fail"
        ? "text-rose-600"
        : "text-muted-foreground";
  return (
    <div
      className="grid grid-cols-12 items-center gap-3 py-2.5"
      data-testid="evidence-stream-row"
    >
      <div className="col-span-1">
        <span className={`inline-block h-2 w-2 rounded-full ${dotClass}`} />
      </div>
      <div className="col-span-3 font-mono text-[11px] text-muted-foreground">
        {observedAt}
      </div>
      <div className="col-span-6 min-w-0">
        <div className="truncate text-sm text-foreground">
          {kind ?? evidenceID}
        </div>
        <div className="truncate font-mono text-[11px] text-muted-foreground">
          {formatEvidenceSource(source)}
        </div>
      </div>
      <div className={`col-span-2 text-right font-mono text-xs ${resultClass}`}>
        {result}
      </div>
    </div>
  );
}

// formatEvidenceSource renders the slice-013 provenance JSONB as a
// short "actor_type/actor_id" tag. Shape varies by connector; when the
// JSONB has no recognisable shape we fall back to "—" rather than
// stringifying arbitrary nested objects into the row.
function formatEvidenceSource(source: Record<string, unknown> | null): string {
  if (!source || typeof source !== "object") return "—";
  const actorType = source.actor_type;
  const actorID = source.actor_id;
  if (typeof actorType === "string" && typeof actorID === "string") {
    return `${actorType}/${actorID}`;
  }
  if (typeof actorID === "string") return actorID;
  if (typeof actorType === "string") return actorType;
  return "—";
}

// formatHistoryDate renders the RFC3339 evaluated_at as the YYYY-MM-DD
// prefix used in the mockup's audit-log entries (lines 552-557). Falls
// back to the raw string when parsing fails so a backend-shape change
// surfaces as a visible date, not a silent error.
function formatHistoryDate(iso: string): string {
  if (!iso) return "—";
  const t = iso.indexOf("T");
  return t > 0 ? iso.slice(0, t) : iso;
}
