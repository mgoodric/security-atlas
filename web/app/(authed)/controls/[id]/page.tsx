"use client";

// Slice 041 — control detail view (`/controls/[id]`).
//
// #2140 (front door, #2072) — the page reads, in order, as:
//   1. the shared header (DetailHeader): the title; the SCF anchor,
//      lifecycle and family; the bundle, implementation, freshness class,
//      anchor fit and external refs; and the actions (Attest, Evaluate
//      now, Download bundle, Upload new version, Request exception);
//   2. the state strip: current state, last evaluated, freshness, owner,
//      owner role, and markers for an exception covering the control and
//      an open action plan;
//   3. the AI gap explanation, once, and only when one is available;
//   4. the Overview panel's Evidence, Coverage and Linked sections (see
//      ./_sections/panels.tsx).
//
// The framework graph is on the Framework map tab (`?tab=map`); the
// overview never renders it. History keeps the evaluation history. Tab
// state is URL-bound via `?tab=<key>`; a missing, unknown or retired key
// lands on the overview (./tabs.ts).
//
// Write affordances follow the live GET /v1/me and fail closed: none show
// until it answers, and none when it fails. Upload new version and
// Request exception need is_approver (admin, grc_engineer: the set OPA
// admits on controls and exceptions writes). Evaluate now and Attest
// also show for a control_owner, whom OPA admits on those two paths; the
// handler then checks the assignment.
//
// Exceptions are read through GET /v1/exceptions?control_id=, filtered
// on the server. A caller the platform refuses the exceptions read (a
// viewer, a control owner) gets no Exceptions part and no error; the
// strip's exception marker then rests on the evaluated state alone. The
// Exceptions and Action plans parts are also left out when their module
// is off for the caller (#2174, useSeesPage over /v1/me/modules).
//
// The 404 empty state (slice 152, ADR 0004) and the 401 -> /login
// redirect are unchanged. Server values live only in the TanStack Query
// cache; the one useEffect is the redirect.

import { useInfiniteQuery, useQueries, useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect } from "react";

import { DemoBadge } from "@/components/demo-badge";
import { DetailHeader } from "@/components/detail/detail-header";
import { Person } from "@/components/ui/person";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";

import {
  formatExternalRef,
  groupExternalRefs,
} from "@/components/control/external-refs";
import { GapExplanationCard } from "@/components/control/gap-explanation";
import {
  ControlHeaderActions,
  ControlStateStrip,
  StripCell,
} from "@/components/control/header-actions";

import { cn } from "@/lib/utils";
import { fetchActionPlansForControl } from "@/lib/api/action-plans";
import {
  fetchControlCoverage,
  fetchControlEffectiveScope,
  fetchControlAnchorHistory,
  fetchControlHistory,
  fetchControlPolicies,
  fetchControlRisks,
  fetchControlState,
  type ControlCoverage,
  type EffectiveScopeResponse,
} from "@/lib/api/control-detail";
import { fetchEvidenceList } from "@/lib/api/evidence";
import { fetchExceptionsList } from "@/lib/api/exceptions";
import { type AnchorDetail } from "@/lib/api/anchors";
import { APIError } from "@/lib/api/base";
import { getMe } from "@/lib/api/me";
import { enumLabel } from "@/lib/labels/enum-labels";

import { classifyControlDetailError } from "../error-classifier";
import { anchorFitLabel } from "../filters";
import { useSeesPage } from "@/components/modules-context";

import { CONTROL_TABS, isTabKey, type TabKey } from "./tabs";
import {
  activeExceptions,
  exceptionsForControl,
  isExcepted,
  openActionPlans,
  worstFreshness,
} from "./strip";

import { HistoryPanel, MapPanel, OverviewPanel } from "./_sections/panels";

// Page size for the Evidence section. The /evidence list page is the
// destination for full paginated browsing.
const EVIDENCE_STREAM_LIMIT = 5;

// A refused read is terminal: do not retry it.
function retryUnlessRefused(count: number, err: Error): boolean {
  return (
    !(
      err instanceof APIError &&
      (err.status === 401 || err.status === 403 || err.status === 404)
    ) && count < 2
  );
}

// ControlDetailPageInner holds the client-side body. The default export
// wraps it in Suspense so `useSearchParams` is satisfied per Next 16's
// App Router contract.
function ControlDetailPageInner() {
  const router = useRouter();
  const { id } = useParams<{ id: string }>();
  const searchParams = useSearchParams();

  // #2174: link parts into a module the caller cannot see are left out
  // and never fetched.
  const seesPolicies = useSeesPage("/policies");
  const seesRisks = useSeesPage("/risks");
  const seesActionPlans = useSeesPage("/action-plans");
  const seesExceptions = useSeesPage("/exceptions");

  const rawTab = searchParams.get("tab");
  const activeTab: TabKey = isTabKey(rawTab) ? rawTab : "overview";

  const setTab = useCallback(
    (next: TabKey) => {
      const params = new URLSearchParams(searchParams.toString());
      if (next === "overview") {
        // Default tab: keep the canonical URL clean.
        params.delete("tab");
      } else {
        params.set("tab", next);
      }
      const qs = params.toString();
      const href = qs
        ? `/controls/${encodeURIComponent(id)}?${qs}`
        : `/controls/${encodeURIComponent(id)}`;
      // replace, not push: a tab click is not a separate page.
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

  const evidenceQ = useQuery({
    queryKey: ["control", id, "evidence", EVIDENCE_STREAM_LIMIT],
    queryFn: () =>
      fetchEvidenceList({ controlID: id, limit: EVIDENCE_STREAM_LIMIT }),
    enabled: Boolean(id),
  });

  const policiesQ = useQuery({
    queryKey: ["control", id, "policies"],
    queryFn: () => fetchControlPolicies(id),
    enabled: Boolean(id) && seesPolicies,
  });

  const risksQ = useQuery({
    queryKey: ["control", id, "risks"],
    queryFn: () => fetchControlRisks(id),
    enabled: Boolean(id) && seesRisks,
  });

  // #2140: the exceptions filed against this control, filtered on the
  // server. A 403 means the caller may not read exceptions.
  const exceptionsQ = useQuery({
    queryKey: ["exceptions", "for-control", id],
    queryFn: () => fetchExceptionsList({ controlId: id }),
    enabled: Boolean(id) && seesExceptions,
    retry: retryUnlessRefused,
  });
  const exceptionsRefused =
    exceptionsQ.error instanceof APIError && exceptionsQ.error.status === 403;

  // The same query LinkedActionPlans reads, so the strip's marker and the
  // Linked part share one request.
  const actionPlansQ = useQuery({
    queryKey: ["action-plans", "for", "control", id],
    queryFn: () => fetchActionPlansForControl(id),
    enabled: Boolean(id) && seesActionPlans,
  });

  const historyQ = useQuery({
    queryKey: ["control", id, "history"],
    queryFn: () => fetchControlHistory(id),
    enabled: Boolean(id) && activeTab === "history",
  });

  // #2392 — the SCF anchor history, on the History tab, a page at a time.
  const anchorHistoryQ = useInfiniteQuery({
    queryKey: ["control", id, "anchor-history"],
    queryFn: ({ pageParam }) => fetchControlAnchorHistory(id, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    enabled: Boolean(id) && activeTab === "history",
  });

  // Write affordances follow the live role. Fail closed: none show until
  // /v1/me answers, and none when it fails.
  const meQ = useQuery({
    queryKey: ["settings-me-profile"],
    queryFn: getMe,
    staleTime: 60_000,
  });
  const canWrite = meQ.data?.is_approver === true;
  const canEvaluate =
    canWrite || (meQ.data?.roles ?? []).includes("control_owner");

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
  // `unauthorized` (/login redirect), `other` (destructive Alert). Only
  // the coverage read drives the page-level branch; the state read's
  // 401 still redirects.
  const firstError = coverageQ.error ?? stateQ.error ?? null;
  const errorClass = classifyControlDetailError(firstError);

  // ATLAS-012 — on the 404 empty state, name the SCF code (e.g. "MON-04")
  // rather than the raw UUID. Enabled only on the notfound branch.
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
      <PageShell title="Control">
        <div className="space-y-4" data-testid="control-detail-loading">
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      </PageShell>
    );
  }

  // Slice 152 D1-c: 404 on the coverage call means the id in the URL
  // does not resolve to a tenant control — on a fresh install, usually an
  // SCF anchor with no instantiated control (slice 150 D3).
  if (coverageQ.error && errorClass === "notfound") {
    return (
      <PageShell title="Control">
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
      </PageShell>
    );
  }

  if (coverageQ.error && errorClass === "other") {
    return (
      <PageShell title="Control">
        <Alert variant="destructive" data-testid="control-detail-error">
          <AlertTitle>Could not load control</AlertTitle>
          <AlertDescription>
            {(coverageQ.error as Error).message}
          </AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  if (!coverageQ.data) {
    return null;
  }

  // A framework_version is out of scope when its effective-scope call
  // resolved with in_scope === false.
  const outOfScopeFvIds = new Set<string>();
  scopeQueries.forEach((q) => {
    const data = q.data as EffectiveScopeResponse | undefined;
    if (data && data.in_scope === false) {
      outOfScopeFvIds.add(data.framework_version_id);
    }
  });

  const coverage = coverageQ.data;
  const { control, anchor } = coverage;
  const states = stateQ.data?.states;
  const freshness = worstFreshness(states);
  const excepted = isExcepted(states);
  const active = exceptionsRefused
    ? []
    : activeExceptions(exceptionsForControl(exceptionsQ.data?.exceptions, id));
  const openPlans = seesActionPlans
    ? openActionPlans(actionPlansQ.data?.action_plans)
    : [];
  const showExceptionMarker = excepted || active.length > 0;

  return (
    <DetailHeader
      data-testid="control-detail"
      titleTestId="control-title"
      headerTestId="control-header"
      title={control.title}
      back={<BackLink leaf={anchor ? anchor.scf_id : control.bundle_id} />}
      status={
        <>
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
              className="text-[11px] text-muted-foreground"
              data-testid="scf-anchor-pill"
            >
              Not anchored
            </span>
          )}
          <Badge variant="secondary" data-testid="lifecycle-badge">
            {enumLabel("ControlLifecycleState", control.lifecycle_state)}
          </Badge>
          {control.demo ? (
            <DemoBadge className="ml-0" testId="control-demo-badge" />
          ) : null}
          <span className="text-xs text-muted-foreground">
            {control.control_family}
          </span>
        </>
      }
      subtitle={
        <>
          <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-sm">
            <span className="font-mono text-xs text-muted-foreground">
              {control.bundle_id}
              {control.version ? ` · v${control.version}` : ""}
            </span>
            <span className="text-muted-foreground">
              Implementation{" "}
              <span
                className="text-foreground"
                data-testid="control-implementation-type"
              >
                {enumLabel(
                  "ControlImplementationType",
                  control.implementation_type,
                )}
              </span>
            </span>
            <span className="text-muted-foreground">
              Freshness class{" "}
              <span className="text-foreground">
                {enumLabel("EvidenceFreshnessClass", control.freshness_class)}
              </span>
            </span>
            <span
              className="text-muted-foreground"
              data-testid="control-anchor-fit"
              data-anchor-fit={control.anchor_fit ?? "exact"}
            >
              Anchor fit{" "}
              <span className="text-foreground">
                {anchorFitLabel(control.anchor_fit)}
              </span>
            </span>
          </div>
          {control.anchor_fit_note ? (
            <p
              className="text-sm text-muted-foreground"
              data-testid="control-anchor-fit-note"
            >
              {control.anchor_fit_note}
            </p>
          ) : null}

          {/* #1737 — control.yaml external_refs: auditor control numbers
              and the framework requirements this control was tested
              under. Rendered only when the control carries any. */}
          {groupExternalRefs(control.external_refs).map((group) => (
            <div
              key={group.kind}
              className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm"
              data-testid={`control-external-refs-${group.kind}`}
            >
              <span className="text-muted-foreground">{group.label}</span>
              {group.refs.map((ref) => (
                <Badge
                  key={`${ref.system}:${ref.id}`}
                  variant="outline"
                  className="font-normal"
                >
                  <span className="font-mono">{formatExternalRef(ref)}</span>
                  {ref.note ? (
                    <span className="text-muted-foreground">
                      {" · "}
                      {ref.note}
                    </span>
                  ) : null}
                </Badge>
              ))}
            </div>
          ))}
        </>
      }
      actions={
        <ControlHeaderActions
          controlID={id}
          implementationType={control.implementation_type}
          canWrite={canWrite}
          canEvaluate={canEvaluate}
        />
      }
    >
      {/* ============ STATE STRIP (#2140) ============ */}
      <ControlStateStrip
        controlID={id}
        state={stateQ.data}
        stateError={Boolean(stateQ.error)}
      >
        <StripCell label="Freshness">
          <span data-testid="control-strip-freshness">
            {freshness ? enumLabel("FreshnessState", freshness) : "—"}
          </span>
        </StripCell>
        <StripCell label="Owner">
          {control.owner_user_id ? (
            <Person
              id={control.owner_user_id}
              className="text-foreground"
              testId="control-owner"
            />
          ) : (
            <span className="text-muted-foreground" data-testid="control-owner">
              Unassigned
            </span>
          )}
        </StripCell>
        <StripCell label="Owner role">
          <span data-testid="control-owner-role">
            {control.owner_role || "—"}
          </span>
        </StripCell>
        <StripCell label="Markers">
          <div
            className="flex flex-wrap gap-1.5"
            data-testid="control-strip-markers"
          >
            {showExceptionMarker ? (
              <Badge
                variant="info"
                data-testid="control-strip-exception"
                title="An active exception covers this control."
              >
                Exception
              </Badge>
            ) : null}
            {openPlans.length > 0 ? (
              <Badge
                variant="warning"
                data-testid="control-strip-action-plan"
                title={openPlans.map((p) => p.title).join(", ")}
              >
                {openPlans.length === 1
                  ? "Open action plan"
                  : `${openPlans.length} open action plans`}
              </Badge>
            ) : null}
            {!showExceptionMarker && openPlans.length === 0 ? (
              <span className="text-muted-foreground">None</span>
            ) : null}
          </div>
        </StripCell>
      </ControlStateStrip>

      {/* ============ TAB STRIP ============ */}
      {/* #2096: <main> is the scroll container; the negative top equal to
          main's padding pins the strip flush to main's top edge, and
          `data-sticky-tabs` turns on main's scroll padding. */}
      <div
        role="tablist"
        aria-label="Control detail sections"
        data-testid="control-tabs"
        data-sticky-tabs
        className="sticky -top-4 z-20 -mx-4 flex items-center gap-1 overflow-x-auto border-b bg-background px-4 sm:-top-6 sm:-mx-6 sm:px-6"
      >
        {CONTROL_TABS.map((t) => {
          const isActive = activeTab === t.key;
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
              {t.label}
            </button>
          );
        })}
      </div>

      {/* ============ TAB PANELS ============ */}
      <div
        role="tabpanel"
        id={`control-tab-panel-${activeTab}`}
        aria-labelledby={`control-tab-${activeTab}`}
        data-testid={`control-tab-panel-${activeTab}`}
        className="space-y-5"
      >
        {activeTab === "overview" ? (
          <>
            {/* The AI explanation, once, only when available (#2140). */}
            <GapExplanationCard id={id} />
            <OverviewPanel
              id={id}
              coverage={coverage}
              outOfScopeFvIds={outOfScopeFvIds}
              evidenceQ={evidenceQ}
              excepted={excepted}
              fvIds={fvIds}
              scopeQueries={scopeQueries}
              policiesQ={seesPolicies ? policiesQ : null}
              risksQ={seesRisks ? risksQ : null}
              exceptionsQ={
                seesExceptions && !exceptionsRefused ? exceptionsQ : null
              }
              showActionPlans={seesActionPlans}
            />
          </>
        ) : null}

        {activeTab === "map" ? (
          <MapPanel coverage={coverage} outOfScopeFvIds={outOfScopeFvIds} />
        ) : null}

        {activeTab === "history" ? (
          <HistoryPanel
            historyQ={historyQ}
            anchorHistoryQ={anchorHistoryQ}
            stateQ={stateQ}
          />
        ) : null}
      </div>
    </DetailHeader>
  );
}

export default function ControlDetailPage() {
  // useSearchParams requires Suspense in Next.js 16 App Router.
  return (
    <Suspense
      fallback={
        <PageShell title="Control">
          <div className="space-y-4" data-testid="control-detail-loading">
            <Skeleton className="h-32 w-full" />
            <Skeleton className="h-64 w-full" />
          </div>
        </PageShell>
      }
    >
      <ControlDetailPageInner />
    </Suspense>
  );
}

// BackLink is the header's way back, ending at this control (ATLAS-011:
// the trail names the page the operator is on).
function BackLink({ leaf }: { leaf: string }) {
  return (
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
        {leaf}
      </span>
    </nav>
  );
}

// distinctFrameworkVersionIds pulls the unique framework_version_id set
// out of the coverage requirements, preserving first-seen order so the
// effective-scope rows are stable across renders.
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

// ATLAS-012 — resolve an SCF anchor's human-readable code via the same
// BFF the catalog detail page uses. Used only on the 404 empty state.
async function fetchAnchorDetail(id: string): Promise<AnchorDetail> {
  const res = await fetch(
    `/api/anchors/${encodeURIComponent(id)}/requirements`,
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as AnchorDetail;
}
