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
// Slice 681 made this page read-only. #1748 adds one write surface: the
// Parents / Children sections attach and detach child risks
// (RiskHierarchyLinks). #2104 renders the page through the shared
// DetailHeader, whose Edit links to `/risks/[id]/edit`, and adds the
// History section (the risk audit trail). #2105 adds the treatment and
// acceptance transitions: the header's primary action is the acceptance
// lifecycle's next step (propose, approve, revoke), the overflow holds
// Change treatment and Reject, and an Acceptance card shows the proposal
// and its approval, marked when self-approved. #2107 adds the open/closed
// lifecycle: "Close risk…" in the overflow opens /risks/[id]/close; a
// closed risk shows its closure and leads with Reopen, a one-click
// transition confirmed inline, in place of the acceptance actions.
// The roll-up from the children sits beside the assessed severity and never
// replaces it. #2106 adds the Linked controls section (link with the shared
// picker, unlink confirmed inline) and "Add action plan", which opens the
// create form pre-linked to this risk.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmInline } from "@/components/ui/row-actions";
import { Skeleton } from "@/components/ui/skeleton";
import { Timestamp } from "@/components/ui/timestamp";
import { useToast } from "@/components/ui/toast";
import { LinkedActionPlans } from "@/components/action-plans/linked-action-plans";
import { Markdown } from "@/components/markdown";
import { RiskHierarchyLinks } from "@/components/risk-hierarchy/risk-hierarchy-links";
import { RiskControls } from "@/components/risks/risk-controls";
import { RiskHistory } from "@/components/risks/risk-history";
import { DetailHeader } from "@/components/detail/detail-header";
import { SelfApprovedBadge } from "@/components/self-approved-badge";
import { PageShell } from "@/components/shell/page-shell";
import { APIError } from "@/lib/api/base";
import {
  fetchRiskCategories,
  fetchRiskDetail,
  transitionRisk,
  treatmentLabel,
  type Risk,
} from "@/lib/api/risks";

import {
  categoryDisplay,
  formatResidualScore,
  fairScore,
  formatDollars,
  patternNoLongerMet,
  residualClass,
  residualSeverity,
  residualState,
  reviewDuePending,
  severityBand,
  severityClasses,
} from "../filters";
import { enumLabel } from "@/lib/labels/enum-labels";
import { SeverityPill } from "../severity-pill";
import {
  ACTION_COPY,
  acceptanceState,
  closureReasonLabel,
  primaryAcceptanceAction,
} from "../lifecycle";
import { RiskOwner } from "./owner";

export default function RiskDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [confirmingReopen, setConfirmingReopen] = useState(false);

  // #2107: reopen is one click, confirmed inline.
  const reopen = useMutation({
    mutationFn: () => transitionRisk(id, "reopen", {}),
    onSuccess: async () => {
      setConfirmingReopen(false);
      await qc.invalidateQueries({ queryKey: ["risk", id] });
      await qc.invalidateQueries({ queryKey: ["risks"] });
      toast("Risk reopened");
    },
    onError: (err) => {
      setConfirmingReopen(false);
      toast(`Could not reopen: ${(err as Error).message}`);
    },
  });

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

  // #1817 — archived included, so a risk on an archived category shows
  // that category by name, marked archived. Same query as the register.
  const categoriesQ = useQuery({
    queryKey: ["risks", "categories", "with-archived"],
    queryFn: () => fetchRiskCategories(true),
    staleTime: 60_000,
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
      <PageShell title="Risk" data-testid="risk-detail-loading">
        <Skeleton className="h-10 w-2/3" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </PageShell>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <PageShell title="Risk" actions={<BackLink />}>
        <Alert variant="destructive" data-testid="risk-detail-error">
          <AlertTitle>Could not load risk</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  if (!data) {
    // 401 redirect in flight, or no data yet — render nothing.
    return null;
  }

  const { risk } = data;
  const category = categoryDisplay(risk, categoriesQ.data ?? []);
  const band = severityBand(risk.severity);
  const fairInherent =
    risk.methodology === "fair" ? fairScore(risk.inherent_score) : null;
  const isFair = risk.methodology === "fair";

  // Residual: the stored residual_score in whichever shape the platform
  // wrote it (#2058 — the deriver's `{score, ...}` included). A
  // not-yet-evaluated residual renders as the same honest "Pending
  // evaluation" affordance the list cell uses (slice 680). A 5x5
  // residual shows on the inherent card's 1..25 scale and bands.
  const residualPending = residualState(risk.residual_score) !== "scored";
  const residualText = residualPending
    ? "Pending evaluation"
    : formatResidualScore(risk.residual_score, risk.methodology);
  const residualN = isFair ? null : residualSeverity(risk.residual_score);

  const base = `/risks/${encodeURIComponent(id)}`;
  const accState = acceptanceState(risk);
  const nextAcceptance = primaryAcceptanceAction(accState);
  const closed = risk.status === "closed";

  return (
    <DetailHeader
      data-testid="risk-detail"
      titleTestId="risk-detail-title"
      headerTestId="risk-detail-header"
      back={<BackLink />}
      title={risk.title}
      edit={{
        href: `${base}/edit`,
        "data-testid": "risk-detail-edit",
      }}
      primaryAction={
        closed
          ? {
              label: reopen.isPending ? "Reopening…" : "Reopen",
              onClick: () => setConfirmingReopen(true),
              disabled: confirmingReopen || reopen.isPending,
              "data-testid": "risk-detail-reopen",
            }
          : {
              label: ACTION_COPY[nextAcceptance].title,
              href: `${base}/acceptance/${nextAcceptance}`,
              "data-testid": `risk-detail-${nextAcceptance}-acceptance`,
            }
      }
      overflow={[
        {
          id: "close",
          label: "Close risk…",
          hidden: closed,
          onSelect: () => router.push(`${base}/close`),
          "data-testid": "risk-detail-close",
        },
        {
          id: "treatment",
          label: "Change treatment…",
          hidden: closed,
          onSelect: () => router.push(`${base}/treatment`),
          disabledReason:
            accState === "none"
              ? undefined
              : "Reject or revoke the acceptance first.",
          "data-testid": "risk-detail-change-treatment",
        },
        {
          id: "reject",
          label: "Reject acceptance…",
          hidden: closed || accState !== "proposed",
          onSelect: () => router.push(`${base}/acceptance/reject`),
          "data-testid": "risk-detail-reject-acceptance",
        },
      ]}
      status={
        <>
          {closed ? (
            <Badge variant="outline" data-testid="risk-detail-closed">
              Closed
            </Badge>
          ) : null}
          <Badge variant="secondary" data-testid="risk-detail-treatment">
            {treatmentLabel(risk)}
          </Badge>
          <SelfApprovedBadge
            selfApproved={Boolean(risk.acceptance?.self_approved)}
            testId="risk-detail-self-approved"
          />
          <span
            className="text-xs text-muted-foreground"
            data-testid="risk-detail-category"
          >
            {category.name}
          </span>
          {category.archived ? (
            <Badge
              variant="outline"
              data-testid="risk-detail-category-archived"
              title="This category is archived. It stays on the risks that have it but takes no new risks."
            >
              archived category
            </Badge>
          ) : null}
        </>
      }
      subtitle={
        <dl className="flex flex-wrap items-center gap-x-6 gap-y-1 text-sm">
          <div>
            <dt className="inline text-muted-foreground">Owner </dt>
            <dd className="inline text-foreground">
              <RiskOwner risk={risk} />
            </dd>
          </div>
          <div>
            <dt className="inline text-muted-foreground">Methodology </dt>
            <dd
              className="inline text-foreground"
              data-testid="risk-detail-methodology"
            >
              {enumLabel("RiskMethodology", risk.methodology)}
            </dd>
          </div>
          <div>
            <dt className="inline text-muted-foreground">Review due </dt>
            <dd
              className="inline font-mono text-foreground"
              data-testid="risk-detail-review-due"
            >
              {reviewDuePending(risk.review_due_at) ? (
                "Not set"
              ) : (
                // The UTC calendar day, as the edit form and History read
                // it (#2104); the "date" style formats in UTC.
                <Timestamp value={risk.review_due_at} style="date" />
              )}
            </dd>
          </div>
        </dl>
      }
    >
      {closed && confirmingReopen ? (
        <ConfirmInline
          prompt="Reopen this risk?"
          confirmLabel="Reopen"
          ariaLabel="Confirm: reopen risk"
          busy={reopen.isPending}
          onCancel={() => setConfirmingReopen(false)}
          onConfirm={() => reopen.mutate()}
          data-testid="risk-detail-reopen-confirm"
        >
          <span className="text-muted-foreground">
            It returns to the register and its counts.
          </span>
        </ConfirmInline>
      ) : null}

      {closed && risk.closure ? (
        <Alert data-testid="risk-detail-closure">
          <AlertTitle>
            Closed: {closureReasonLabel(risk.closure.reason)}
          </AlertTitle>
          <AlertDescription>
            {risk.closure.note ? (
              <p data-testid="risk-detail-closure-note">{risk.closure.note}</p>
            ) : null}
            <p className="text-xs">
              Closed
              {risk.closure.closed_at ? (
                <>
                  {" "}
                  <Timestamp value={risk.closure.closed_at} />
                </>
              ) : null}
              . A closed risk is left out of the register, its counts and the
              top risks until it is reopened.
            </p>
          </AlertDescription>
        </Alert>
      ) : null}

      {patternNoLongerMet(risk.inherent_score) ? (
        <Alert data-testid="risk-detail-pattern-lapsed">
          <AlertTitle>Pattern no longer met</AlertTitle>
          <AlertDescription>
            This rule-generated meta-risk has 0 child risks in its rule&apos;s
            current window, so its score is empty. The aggregation engine never
            closes a meta-risk: review it and close or delete it if the pattern
            is over.
          </AlertDescription>
        </Alert>
      ) : null}

      {/* ============ SCORING (independent axes — canvas §6.2) ============ */}
      <div className="grid gap-4 sm:grid-cols-2">
        <Card size="sm" data-testid="risk-detail-severity-card">
          <CardContent>
            {fairInherent ? (
              <>
                <div className="text-[11px] uppercase tracking-wider text-muted-foreground">
                  Inherent annualized exposure
                </div>
                <div
                  className="mt-1 text-2xl font-semibold"
                  data-testid="risk-detail-fair-inherent-exposure"
                >
                  {formatDollars(fairInherent.annualized_loss_exposure)}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  {fairInherent.loss_event_frequency.toFixed(2)} events/year ×{" "}
                  {formatDollars(fairInherent.loss_magnitude)}
                </div>
              </>
            ) : (
              <>
                <div
                  className="text-[11px] uppercase tracking-wider text-muted-foreground"
                  title="Inherent severity (1–25): likelihood × impact on the 5×5 grid, before any control mitigation."
                >
                  Inherent severity
                </div>
                <div className="mt-1">
                  <SeverityPill
                    value={String(risk.severity)}
                    band={band}
                    className={severityClasses(band)}
                    testId="risk-detail-severity"
                    size="md"
                  />
                </div>
              </>
            )}
            {risk.rollup ? (
              <div
                className="mt-3 border-t pt-2 text-sm"
                data-testid="risk-detail-rollup"
              >
                <span
                  className="text-[11px] uppercase tracking-wider text-muted-foreground"
                  title="Roll-up severity: the child risks' severities combined by the severity function. It does not change the assessed score."
                >
                  Rolled up from children{" "}
                </span>
                <span
                  className="font-semibold"
                  data-testid="risk-detail-rollup-severity"
                >
                  {risk.rollup.severity}
                </span>
                <span className="text-xs text-muted-foreground">
                  {" "}
                  ({risk.rollup.severity_function} of {risk.rollup.child_count}{" "}
                  {risk.rollup.child_count === 1 ? "child" : "children"})
                </span>
                {risk.rollup.exceeds_assessed ? (
                  <div
                    className="mt-1 text-xs text-amber-700 dark:text-amber-300"
                    data-testid="risk-detail-rollup-exceeds"
                  >
                    Higher than the assessed severity (
                    {risk.rollup.assessed_severity}). Review the assessment.
                  </div>
                ) : null}
              </div>
            ) : null}
          </CardContent>
        </Card>

        <Card size="sm" data-testid="risk-detail-residual-card">
          <CardContent>
            <div
              className="text-[11px] uppercase tracking-wider text-muted-foreground"
              title="Residual (1–25): inherent severity reduced by the linked controls' measured effectiveness, on the same scale and bands as inherent."
            >
              {isFair
                ? "Residual annualized exposure"
                : "Residual (after controls)"}
            </div>
            {residualN != null ? (
              <div className="mt-1">
                <SeverityPill
                  value={residualText}
                  band={severityBand(residualN)}
                  className={residualClass(residualText)}
                  testId="risk-detail-residual"
                  size="md"
                />
              </div>
            ) : (
              <div
                className="mt-1 text-2xl font-semibold"
                data-testid="risk-detail-residual"
              >
                {residualText}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {/* ============ ACCEPTANCE (#2105) ============ */}
      {risk.acceptance ? <AcceptanceCard risk={risk} /> : null}

      {/* ============ DESCRIPTION ============ */}
      <Card data-testid="risk-detail-description-card">
        <CardHeader className="border-b">
          <CardTitle>Description</CardTitle>
        </CardHeader>
        <CardContent>
          {risk.description.trim() ? (
            <Markdown
              source={risk.description}
              data-testid="risk-detail-description"
            />
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

      {/* ============ PARENTS / CHILDREN (#1748) ============ */}
      <RiskHierarchyLinks risk={risk} />

      {/* ============ LINKED CONTROLS (#2106) ============ */}
      <RiskControls risk={risk} />

      {/* ============ LINKED ACTION PLANS (slice 384, AC-25) ============ */}
      {/* #2106: "Add action plan" opens the create form pre-linked. */}
      <LinkedActionPlans
        target="risk"
        targetId={id}
        addHref={
          closed
            ? undefined
            : `/action-plans/new?risk_id=${encodeURIComponent(id)}`
        }
      />

      {/* ============ HIERARCHY LINK ============ */}
      <div data-testid="risk-detail-hierarchy-link">
        <Link
          href={`/risks/hierarchy?focus=${encodeURIComponent(id)}`}
          className="text-sm text-primary hover:underline"
        >
          View in hierarchy →
        </Link>
      </div>

      {/* ============ HISTORY (#2104) ============ */}
      <RiskHistory riskId={id} />
    </DetailHeader>
  );
}

// The proposed or approved acceptance: why, until when, and who. People
// show as their ids, as on the exception page.
function AcceptanceCard({ risk }: { risk: Risk }) {
  const a = risk.acceptance;
  if (!a) return null;
  const by = (who: string | undefined) => ` by ${who || "unknown"}`;
  const rows: [string, React.ReactNode, string][] = [
    [
      "Proposed",
      <>
        <Timestamp value={a.proposed_at} />
        {by(a.proposed_by)}
      </>,
      "proposed",
    ],
    [
      "Proposed expiry",
      <Timestamp key="v" value={a.proposed_until} style="date" />,
      "proposed-until",
    ],
  ];
  if (risk.acceptance_status === "approved") {
    rows.push(
      [
        "Approved",
        <>
          <Timestamp value={a.approved_at} />
          {by(risk.accepter)}
        </>,
        "approved",
      ],
      [
        "Accepted until",
        <Timestamp key="v" value={risk.accepted_until} style="date" />,
        "accepted-until",
      ],
    );
  }
  return (
    <Card data-testid="risk-detail-acceptance-card">
      <CardHeader className="border-b">
        <CardTitle>
          Acceptance{" "}
          <span
            className="text-sm font-normal text-muted-foreground"
            data-testid="risk-detail-acceptance-status"
          >
            {risk.acceptance_status === "approved"
              ? "approved"
              : "awaiting approval"}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {a.justification.trim() ? (
          <Markdown
            source={a.justification}
            data-testid="risk-detail-acceptance-justification"
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            No justification was recorded.
          </p>
        )}
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          {rows.map(([label, value, key]) => (
            <div key={key} className="contents">
              <dt className="text-muted-foreground">{label}</dt>
              <dd data-testid={`risk-detail-acceptance-${key}`}>{value}</dd>
            </div>
          ))}
        </dl>
      </CardContent>
    </Card>
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
