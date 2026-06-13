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

import { useQueries, useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

import { ControlHeaderActions } from "@/components/control/header-actions";

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
  type ControlLinkedPoliciesResponse,
  type ControlLinkedRisksResponse,
  type EffectiveScopeResponse,
} from "@/lib/api/control-detail";
import {
  fetchEvidenceList,
  type EvidenceListResponse,
} from "@/lib/api/evidence";
import { type AnchorDetail } from "@/lib/api/anchors";
import { APIError } from "@/lib/api/base";

import { classifyControlDetailError } from "../error-classifier";
import { CONTROL_TABS, formatTabCount, isTabKey, type TabKey } from "./tabs";

import {
  EvidencePanel,
  HistoryPanel,
  MappingsPanel,
  OverviewPanel,
  PoliciesPanel,
  RisksPanel,
  ScopePanel,
} from "./_sections/panels";

// Slice 253 — page size for the evidence stream card. Five rows mirrors
// the mockup (`Plans/_archive/mockups/control.html` lines 389-440). The KPI sub-
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

  // ATLAS-012 — when the coverage call 404s, the id in the URL is an SCF
  // anchor with no instantiated control. The empty-state copy should name
  // the human-readable SCF code (e.g. "AAA-01"), not the raw UUID. The
  // anchor still resolves in the global catalog, so we look up its
  // requirements (same BFF the catalog detail page uses) purely to read
  // `anchor.scf_id`. Enabled only on the notfound branch so the happy
  // path makes no extra call; if the lookup is pending or also fails we
  // fall back to the id so the empty-state always renders.
  const anchorCodeQ = useQuery<AnchorDetail>({
    queryKey: ["control", id, "anchor-code"],
    queryFn: () => fetchAnchorDetail(id),
    enabled: Boolean(id) && errorClass === "notfound",
  });
  const anchorCode = anchorCodeQ.data?.anchor.scf_id ?? null;

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
          SCF anchor <span className="font-mono">{anchorCode ?? id}</span>{" "}
          resolves in the global SCF catalog but no tenant control is bound to
          it. This is the expected state on a fresh install — controls are
          tenant-scoped and authored separately from the catalog.
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
      {/* breadcrumb — ATLAS-011: include the leaf crumb (the SCF anchor
          code, e.g. "Controls › AAA-01") so the trail ends at the page the
          operator is on, not at the section. Falls back to the control's
          bundle id when the row is unanchored. */}
      <nav
        aria-label="Breadcrumb"
        data-testid="control-detail-breadcrumb"
        className="flex items-center gap-1.5 text-sm text-muted-foreground"
      >
        <Link href="/controls" className="hover:underline">
          Controls
        </Link>
        <span aria-hidden>›</span>
        <span
          className="font-medium text-foreground"
          data-testid="control-detail-breadcrumb-leaf"
        >
          {anchor ? anchor.scf_id : control.bundle_id}
        </span>
      </nav>

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
          (Plans/_archive/mockups/control.html lines 139-152). The strip uses
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

// OverviewPanel — Plans/_archive/mockups/control.html shows the Overview tab as
// the "everything-at-a-glance" surface (lines 156-560). Slice 254
// preserves the exact two-column layout that shipped pre-tab-strip
// (anti-criterion P0-254-3): coverage + UCF graph + evidence stream
// on the left two-thirds, freshness + effective scope + policies +
// risks + audit log on the right one-third. Each card's data is
// shared across tabs (the same TanStack Query keys back the
// dedicated-tab views), so flipping tabs is free.

// ATLAS-012 — resolve an SCF anchor's human-readable code via the same
// BFF the catalog detail page uses. Used only on the control-detail
// 404 empty-state to print "AAA-01" instead of the raw anchor UUID.
async function fetchAnchorDetail(id: string): Promise<AnchorDetail> {
  const res = await fetch(
    `/api/anchors/${encodeURIComponent(id)}/requirements`,
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as AnchorDetail;
}
