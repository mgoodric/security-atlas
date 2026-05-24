"use client";

// Slice 041 — control detail view (`/controls/[id]`).
// Slice 253 — re-pointed the four endpoint-pending placeholders.
//
// Built per `Plans/mockups/control.html`. Renders, in mockup order:
//   - control header: CTRL id, SCF anchor pill, lifecycle badge, family,
//     title, owner/implementation/freshness-class meta
//   - KPI strip: effectiveness 30d, frameworks satisfied, evidence
//     records 30d (slice 253 — bound to GET /v1/evidence?control_id=…),
//     effective-scope cells
//   - coverage-by-framework table (slice 008)
//   - UCF mini-viz SVG (slice 008)
//   - evidence stream (slice 253 — bound to GET /v1/evidence; renders
//     the latest five records or a truly-empty honest state)
//   - right rail: freshness clock (slice 012), effective-scope panel
//     (slice 018, one call per framework_version_id), linked policies
//     (slice 253 — GET /v1/controls/{id}/policies), linked risks
//     (slice 253 — GET /v1/controls/{id}/risks), audit log (slice 253
//     — GET /v1/controls/{id}/history)
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
// Data sync: server values live ONLY in TanStack Query cache and are read
// during render — there is NO useEffect that seeds state from a server
// value (React 19 set-state-in-effect lint, slice 063 learned this). The
// single useEffect is the 401 -> /login redirect, matching the
// `catalog/scf/[id]` precedent exactly.

import { useQueries, useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect } from "react";

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
import { UcfMiniViz } from "@/components/control/ucf-mini-viz";
import {
  fetchControlCoverage,
  fetchControlEffectiveScope,
  fetchControlEffectiveness,
  fetchControlHistory,
  fetchControlPolicies,
  fetchControlRisks,
  fetchControlState,
  fetchEvidenceList,
  type ControlCoverage,
  type EffectiveScopeResponse,
} from "@/lib/api";
import { formatResidualScore } from "@/app/(authed)/risks/filters";

import { classifyControlDetailError } from "../error-classifier";

// Slice 253 — page size for the evidence stream card. Five rows mirrors
// the mockup (`Plans/mockups/control.html` lines 389-440). The KPI sub-
// text reads "in last 30 days" (the upstream default window) — when the
// stream's count maxes at the limit AND a next_cursor is present we
// surface a "<limit>+ in last 30 days" hint rather than fabricating a
// total the backend never returned. The dedicated /evidence list page
// is the destination for full paginated browsing.
const EVIDENCE_STREAM_LIMIT = 5;

export default function ControlDetailPage() {
  const router = useRouter();
  const { id } = useParams<{ id: string }>();

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
      <header className="space-y-2" data-testid="control-header">
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

      {/* ============ MAIN GRID ============ */}
      <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
        {/* LEFT 2/3 */}
        <div className="space-y-5 lg:col-span-2">
          {/* COVERAGE BY FRAMEWORK */}
          <Card data-testid="coverage-section">
            <CardHeader className="border-b">
              <CardTitle>Coverage by framework</CardTitle>
              <CardDescription>
                Routed through{" "}
                <span className="font-mono">
                  {anchor ? anchor.scf_id : "no anchor"}
                </span>{" "}
                · STRM relationship type and mapping strength per requirement
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
                This control through the SCF anchor to the framework
                requirements it satisfies
              </CardDescription>
            </CardHeader>
            <CardContent>
              <UcfMiniViz
                coverage={coverage}
                outOfScopeFvIds={outOfScopeFvIds}
              />
            </CardContent>
          </Card>

          {/* EVIDENCE STREAM — slice 253: bound to GET /v1/evidence?control_id=… */}
          <Card data-testid="evidence-stream-section">
            <CardHeader className="border-b">
              <CardTitle>Evidence stream · recent</CardTitle>
              <CardDescription>
                Append-only ledger · last 30 days
              </CardDescription>
            </CardHeader>
            <CardContent>
              {evidenceQ.isLoading ? (
                <Skeleton className="h-24 w-full" />
              ) : evidenceQ.error ? (
                <Alert
                  variant="destructive"
                  data-testid="evidence-stream-error"
                >
                  <AlertTitle>Could not load evidence stream</AlertTitle>
                  <AlertDescription>
                    {(evidenceQ.error as Error).message}
                  </AlertDescription>
                </Alert>
              ) : evidenceQ.data && evidenceQ.data.evidence.length > 0 ? (
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
              ) : (
                <p
                  className="text-sm text-muted-foreground"
                  data-testid="evidence-stream-empty"
                >
                  No evidence records for this control in the last 30 days.
                </p>
              )}
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
              {fvIds.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No mapped frameworks, so no effective scope to compute.
                </p>
              ) : (
                fvIds.map((fvId, i) => {
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
                })
              )}
            </CardContent>
          </Card>

          {/* LINKED POLICIES — slice 253: GET /v1/controls/{id}/policies */}
          <Card data-testid="policies-section">
            <CardHeader>
              <CardTitle>Policies</CardTitle>
            </CardHeader>
            <CardContent>
              {policiesQ.isLoading ? (
                <Skeleton className="h-16 w-full" />
              ) : policiesQ.error ? (
                <p
                  className="text-sm text-destructive"
                  data-testid="policies-error"
                >
                  Could not load linked policies:{" "}
                  {(policiesQ.error as Error).message}
                </p>
              ) : policiesQ.data && policiesQ.data.policies.length > 0 ? (
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
              ) : (
                <p
                  className="text-sm text-muted-foreground"
                  data-testid="policies-empty"
                >
                  No policies are linked to this control.
                </p>
              )}
            </CardContent>
          </Card>

          {/* LINKED RISKS — slice 253: GET /v1/controls/{id}/risks */}
          <Card data-testid="risks-section">
            <CardHeader>
              <CardTitle>Risks treated</CardTitle>
            </CardHeader>
            <CardContent>
              {risksQ.isLoading ? (
                <Skeleton className="h-16 w-full" />
              ) : risksQ.error ? (
                <p
                  className="text-sm text-destructive"
                  data-testid="risks-error"
                >
                  Could not load linked risks: {(risksQ.error as Error).message}
                </p>
              ) : risksQ.data && risksQ.data.risks.length > 0 ? (
                <ul
                  className="divide-y divide-border text-sm"
                  data-testid="risks-list"
                >
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
              ) : (
                <p
                  className="text-sm text-muted-foreground"
                  data-testid="risks-empty"
                >
                  No risks are linked to this control.
                </p>
              )}
            </CardContent>
          </Card>

          {/* AUDIT LOG — slice 253: GET /v1/controls/{id}/history */}
          <Card data-testid="audit-log-section">
            <CardHeader>
              <CardTitle>Audit log</CardTitle>
            </CardHeader>
            <CardContent>
              {historyQ.isLoading ? (
                <Skeleton className="h-16 w-full" />
              ) : historyQ.error ? (
                <p
                  className="text-sm text-destructive"
                  data-testid="audit-log-error"
                >
                  Could not load audit log: {(historyQ.error as Error).message}
                </p>
              ) : historyQ.data && historyQ.data.history.length > 0 ? (
                <ul
                  className="space-y-1.5 text-xs"
                  data-testid="audit-log-list"
                >
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
                        evaluated{" "}
                        <span className="font-mono">{h.computed_state}</span>
                      </span>{" "}
                      <span className="text-muted-foreground">
                        ({h.evidence_count} evidence ·{" "}
                        <span className="font-mono">{h.freshness_status}</span>)
                      </span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p
                  className="text-sm text-muted-foreground"
                  data-testid="audit-log-empty"
                >
                  No evaluation history yet for this control.
                </p>
              )}
            </CardContent>
          </Card>
        </aside>
      </div>
    </div>
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
