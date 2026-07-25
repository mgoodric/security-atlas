"use client";

// Slice 681 / ATLAS-039 (AC-2) — read-only per-risk detail view
// (`/risks/[id]`).
//
// Closes the ATLAS-039 audit finding: the /risks register said per-risk
// detail was a "future slice" and offered only "View in hierarchy" — no
// honest drill-in to a single risk. JUDGMENT D2 (decisions log): BUILD
// the route rather than keep deferring it. The backend read API
// (`GET /v1/risks/{id}` -> `GetRisk`) is RLS-tenant-scoped and the data
// already exists, so a read-only detail page is the honest fix — exactly
// the precedent slice 672 set for the identical ATLAS-024 policy finding.
//
// Mirrors the slice 672 policies/[id] + controls/[id] precedent: a
// client page that fetches its own BFF (`/api/risks/{id}`) via TanStack
// Query, with a loading skeleton, a 401 -> /login redirect, a 404 ->
// in-shell `notFound()`, and a destructive Alert for any other error.
// The BFF is the only tenant context (cookie session -> upstream RLS,
// invariant #6); the page never passes a tenant_id.
//
// Read-only (slice 681 anti-criterion): NO edit / delete / link
// affordances. The one outbound action is "View in hierarchy", the same
// org-tree scoping link the list row carries.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { LinkedActionPlans } from "@/components/action-plans/linked-action-plans";
import { APIError } from "@/lib/api/base";
import { fetchRiskDetail } from "@/lib/api/risks";

import {
  formatResidualScore,
  residualState,
  reviewDuePending,
  severityBand,
  severityClasses,
} from "../filters";

function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—";
  return iso.slice(0, 10);
}

export default function RiskDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();

  const { data, isLoading, error } = useQuery({
    queryKey: ["risk", id],
    queryFn: () => fetchRiskDetail(id),
    // A 404 (genuinely-missing / cross-tenant id) and a 401 are terminal
    // states — do not retry them.
    retry: (count, err) =>
      !(
        err instanceof APIError &&
        (err.status === 404 || err.status === 401)
      ) && count < 2,
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/risks/${id}`);
    }
  }, [error, id, router]);

  // 404 -> in-shell not-found boundary (the nearest (authed)/not-found.tsx
  // catches it inside the authed layout shell, so nav stays present).
  if (error instanceof APIError && error.status === 404) {
    notFound();
  }

  if (isLoading) {
    return (
      <div className="space-y-6" data-testid="risk-detail-loading">
        <Skeleton className="h-10 w-2/3" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <div className="space-y-6">
        <BackLink />
        <Alert variant="destructive" data-testid="risk-detail-error">
          <AlertTitle>Could not load risk</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </div>
    );
  }

  if (!data) {
    // 401 redirect in flight, or no data yet — render nothing.
    return null;
  }

  const { risk } = data;
  const band = severityBand(risk.severity);

  // Residual: prefer the live-derived magnitude when the BFF carried a
  // deriver breakdown; otherwise fall back to the stored residual_score.
  // A not-yet-evaluated residual renders as the same honest "Pending
  // evaluation" affordance the list cell uses (slice 680).
  const residualPending = residualState(risk.residual_score) !== "scored";
  const residualText = residualPending
    ? "Pending evaluation"
    : formatResidualScore(risk.residual_score);

  return (
    <div className="space-y-6" data-testid="risk-detail">
      <BackLink />

      {/* ============ HEADER ============ */}
      <header className="space-y-3" data-testid="risk-detail-header">
        <div className="flex flex-wrap items-center gap-2">
          <Badge
            variant="secondary"
            data-testid="risk-detail-treatment"
            className="capitalize"
          >
            {risk.treatment}
          </Badge>
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="risk-detail-category"
          >
            {risk.category}
          </span>
        </div>
        <h1
          className="text-2xl font-semibold tracking-tight"
          data-testid="risk-detail-title"
        >
          {risk.title}
        </h1>
        <dl className="flex flex-wrap items-center gap-x-6 gap-y-1 text-sm">
          <div>
            <dt className="inline text-muted-foreground">Owner </dt>
            <dd
              className="inline text-foreground"
              data-testid="risk-detail-owner"
            >
              {risk.treatment_owner.trim() || "unassigned"}
            </dd>
          </div>
          <div>
            <dt className="inline text-muted-foreground">Methodology </dt>
            <dd className="inline text-foreground">{risk.methodology}</dd>
          </div>
          <div>
            <dt className="inline text-muted-foreground">Review due </dt>
            <dd
              className="inline font-mono text-foreground"
              data-testid="risk-detail-review-due"
            >
              {reviewDuePending(risk.review_due_at)
                ? "Pending evaluation"
                : formatDate(risk.review_due_at)}
            </dd>
          </div>
        </dl>
      </header>

      {/* ============ SCORING (independent axes — canvas §6.2) ============ */}
      <div className="grid gap-4 sm:grid-cols-2">
        <Card size="sm" data-testid="risk-detail-severity-card">
          <CardContent>
            <div
              className="text-[11px] uppercase tracking-wider text-muted-foreground"
              title="Inherent severity: likelihood × impact on the 5×5 grid, before any control mitigation."
            >
              Inherent severity
            </div>
            <div className="mt-1 flex items-center gap-2">
              <span
                className={`inline-flex items-center justify-center w-7 h-7 text-sm font-semibold rounded ${severityClasses(
                  band,
                )}`}
                data-testid="risk-detail-severity"
              >
                {risk.severity}
              </span>
              <span className="text-xs text-muted-foreground capitalize">
                {band}
              </span>
            </div>
          </CardContent>
        </Card>

        <Card size="sm" data-testid="risk-detail-residual-card">
          <CardContent>
            <div
              className="text-[11px] uppercase tracking-wider text-muted-foreground"
              title="Residual: inherent severity reduced by the linked controls' measured effectiveness (0..1)."
            >
              Residual (after controls)
            </div>
            <div
              className="mt-1 text-2xl font-semibold"
              data-testid="risk-detail-residual"
            >
              {residualText}
            </div>
          </CardContent>
        </Card>
      </div>

      {/* ============ DESCRIPTION ============ */}
      <Card data-testid="risk-detail-description-card">
        <CardHeader className="border-b">
          <CardTitle>Description</CardTitle>
        </CardHeader>
        <CardContent>
          {risk.description.trim() ? (
            <p
              className="text-sm text-foreground whitespace-pre-wrap break-words"
              data-testid="risk-detail-description"
            >
              {risk.description}
            </p>
          ) : (
            <p
              className="text-sm text-muted-foreground"
              data-testid="risk-detail-description-empty"
            >
              This risk has no description.
            </p>
          )}
        </CardContent>
      </Card>

      {/* ============ LINKED ACTION PLANS (slice 384, AC-25) ============ */}
      <LinkedActionPlans target="risk" targetId={id} />

      {/* ============ HIERARCHY LINK ============ */}
      <div data-testid="risk-detail-hierarchy-link">
        <Link
          href={`/risks/hierarchy?focus=${encodeURIComponent(id)}`}
          className="text-sm text-primary hover:underline"
        >
          View in hierarchy →
        </Link>
      </div>
    </div>
  );
}

function BackLink() {
  return (
    <div className="text-sm">
      <Link
        href="/risks"
        className="text-muted-foreground hover:underline"
        data-testid="risk-detail-back"
      >
        ← Risk register
      </Link>
    </div>
  );
}
