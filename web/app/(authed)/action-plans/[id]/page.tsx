"use client";

// Slice 384 — /action-plans/[id] detail view (AC-23). Shows all fields plus
// the linked risks and controls (read shape: `GET /v1/action-plans/{id}`,
// single round-trip). Tenant isolation is RLS-enforced (invariant 6); a
// cross-tenant id resolves to a clean upstream 404 -> in-shell not-found.
//
// #2108 renders the page through the shared DetailHeader. Its Edit links to
// `/action-plans/[id]/edit`; its primary action is the plan's forward step
// (Start, Mark complete, Unblock, Verify) and the overflow holds the other
// transitions the state machine allows from the current status (Block,
// Mark complete from Blocked, Reopen). Every transition is one click,
// confirmed inline, then toasted. Verify shows only on a completed plan
// with no linked controls, and only to an approver who is not the plan's
// owner; the platform refuses everyone else, and a plan with linked
// controls, whatever the page shows.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";

import { DetailHeader } from "@/components/detail/detail-header";
import { Markdown } from "@/components/markdown";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { OwnerAccessFlag } from "@/components/ui/owner-access-flag";
import { Person } from "@/components/ui/person";
import { ConfirmInline } from "@/components/ui/row-actions";
import { useToast } from "@/components/ui/toast";
import {
  ACTION_PLAN_STATUSES,
  fetchActionPlan,
  updateActionPlan,
  verifyActionPlan,
  type ActionPlanDetailResponse,
  type ActionPlanStatus,
} from "@/lib/api/action-plans";
import { getMe } from "@/lib/api/me";

import {
  planTransitions,
  viewerCanVerify,
  type PlanTransition,
} from "../lifecycle";
import {
  dateLabel,
  planStatusLabel,
  statusLabel,
  statusPillClass,
} from "../status";

export default function ActionPlanDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const qc = useQueryClient();
  const { toast } = useToast();
  const [confirming, setConfirming] = useState<PlanTransition | null>(null);

  const planQ = useQuery<ActionPlanDetailResponse>({
    queryKey: ["action-plans", "detail", id],
    queryFn: () => fetchActionPlan(id),
    enabled: Boolean(id),
  });
  // Who is viewing decides only whether Verify shows; the platform checks
  // the role and the owner again.
  const meQ = useQuery({
    queryKey: ["settings-me-profile"],
    queryFn: getMe,
    staleTime: 60_000,
  });

  const transition = useMutation({
    mutationFn: (t: PlanTransition) =>
      t.id === "verify"
        ? verifyActionPlan(id)
        : updateActionPlan(id, {
            status: t.to as Exclude<ActionPlanStatus, "verified">,
          }),
    onSuccess: async (_data, t) => {
      setConfirming(null);
      await qc.invalidateQueries({ queryKey: ["action-plans", "detail", id] });
      void qc.invalidateQueries({ queryKey: ["action-plans", "list"] });
      toast(t.toast);
    },
    onError: (err, t) => {
      setConfirming(null);
      toast(`Could not ${t.label.toLowerCase()}: ${(err as Error).message}`);
    },
  });

  if (planQ.isLoading) {
    return (
      <PageShell width="reading" title="Action plan">
        <p className="text-sm text-muted-foreground">Loading…</p>
      </PageShell>
    );
  }
  if (planQ.isError) {
    return (
      <PageShell width="reading" title="Action plan">
        <Alert variant="destructive" data-testid="action-plan-detail-error">
          <AlertTitle>Could not load action plan</AlertTitle>
          <AlertDescription>{(planQ.error as Error).message}</AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  const plan = planQ.data!.action_plan;
  const linkage = planQ.data!.linkage;
  const base = `/action-plans/${encodeURIComponent(id)}`;
  const transitions = planTransitions(plan.status, {
    hasLinkedControls: linkage.controls.length > 0,
    canVerify: viewerCanVerify(meQ.data, plan.owner_id),
  });
  const primary = transitions.find((t) => t.primary);
  const busy = confirming !== null || transition.isPending;

  return (
    <DetailHeader
      data-testid="action-plan-detail"
      titleTestId="action-plan-detail-title"
      headerTestId="action-plan-detail-header"
      back={
        <Link
          href="/action-plans"
          className="self-center text-sm text-muted-foreground hover:underline"
        >
          ← Action plans
        </Link>
      }
      title={plan.title}
      edit={{ href: `${base}/edit`, "data-testid": "action-plan-detail-edit" }}
      primaryAction={
        primary
          ? {
              label: primary.label,
              onClick: () => setConfirming(primary),
              disabled: busy,
              "data-testid": `action-plan-transition-${primary.id}`,
            }
          : undefined
      }
      overflow={transitions
        .filter((t) => !t.primary)
        .map((t) => ({
          id: t.id,
          label: t.label,
          disabledReason: busy
            ? "Finish the open confirmation first."
            : undefined,
          onSelect: () => setConfirming(t),
          "data-testid": `action-plan-transition-${t.id}`,
        }))}
      status={
        <>
          <span
            className={
              "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11px] font-medium " +
              statusPillClass(plan.status)
            }
            data-testid="action-plan-detail-status"
          >
            {planStatusLabel(plan.status, plan.controls_failing)}
          </span>
          <span className="text-xs text-muted-foreground">
            Due {dateLabel(plan.due_date)}
          </span>
        </>
      }
      subtitle={<Stepper status={plan.status} />}
    >
      {confirming ? (
        <ConfirmInline
          prompt={confirming.prompt}
          confirmLabel={confirming.label}
          ariaLabel={`Confirm: ${confirming.label.toLowerCase()}`}
          busy={transition.isPending}
          onCancel={() => setConfirming(null)}
          onConfirm={() => transition.mutate(confirming)}
          data-testid="action-plan-transition-confirm"
        />
      ) : null}

      {plan.status === "completed" && linkage.controls.length > 0 ? (
        <p
          className="text-xs text-muted-foreground"
          data-testid="action-plan-detail-verify-by-controls"
        >
          This plan has linked controls, so it is verified by their evaluations,
          not by hand.
        </p>
      ) : null}

      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <dt className="text-xs text-muted-foreground">Owner</dt>
          <dd className="text-sm">
            <span className="inline-flex flex-wrap items-center gap-2">
              <Person
                id={plan.owner_id}
                avatar
                testId="action-plan-detail-owner"
              />
              <OwnerAccessFlag
                lacksAccess={plan.owner_lacks_access}
                testId="action-plan-detail-owner-no-access"
              />
            </span>
          </dd>
        </div>
        <Field label="Created" value={dateLabel(plan.created_at)} />
        <Field label="Updated" value={dateLabel(plan.updated_at)} />
        <Field
          label="Audit period"
          value={plan.audit_period_id ?? "—"}
          mono={Boolean(plan.audit_period_id)}
        />
      </dl>

      <Section title="Triggering event">
        <p
          className="text-sm text-foreground/80"
          data-testid="action-plan-detail-trigger"
        >
          {plan.triggering_event || "—"}
        </p>
      </Section>

      <Section title="Description">
        <Markdown
          source={plan.description || "—"}
          className="text-foreground/80"
          data-testid="action-plan-detail-description"
        />
      </Section>

      <Section title={`Linked risks (${linkage.risks.length})`}>
        {linkage.risks.length === 0 ? (
          <p className="text-sm text-muted-foreground">No linked risks.</p>
        ) : (
          <ul className="space-y-1" data-testid="action-plan-detail-risks">
            {linkage.risks.map((l) => (
              <li key={l.target_id}>
                <Link
                  href={`/risks/${encodeURIComponent(l.target_id)}`}
                  className="font-mono text-xs text-primary hover:underline"
                >
                  {l.target_id}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section title={`Linked controls (${linkage.controls.length})`}>
        {linkage.controls.length === 0 ? (
          <p className="text-sm text-muted-foreground">No linked controls.</p>
        ) : (
          <ul className="space-y-1" data-testid="action-plan-detail-controls">
            {linkage.controls.map((l) => (
              <li key={l.target_id}>
                <Link
                  href={`/controls/${encodeURIComponent(l.target_id)}`}
                  className="font-mono text-xs text-primary hover:underline"
                >
                  {l.target_id}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Section>
    </DetailHeader>
  );
}

/** The plan's place in its lifecycle: draft → … → verified, with Blocked
 *  as the side state it is. */
function Stepper({ status }: { status: ActionPlanStatus }) {
  const line = ACTION_PLAN_STATUSES.filter((s) => s !== "blocked");
  const at = line.indexOf(status === "blocked" ? "in_progress" : status);
  return (
    <ol
      className="flex flex-wrap items-center gap-1 text-xs"
      aria-label="Plan lifecycle"
      data-testid="action-plan-stepper"
    >
      {line.map((s, i) => (
        <li key={s} className="flex items-center gap-1">
          {i > 0 ? (
            <span aria-hidden="true" className="text-muted-foreground">
              →
            </span>
          ) : null}
          <span
            aria-current={s === status ? "step" : undefined}
            className={
              i === at
                ? "font-medium text-foreground"
                : i < at
                  ? "text-foreground/70"
                  : "text-muted-foreground"
            }
          >
            {statusLabel(s)}
            {s === "in_progress" && status === "blocked" ? " (blocked)" : ""}
          </span>
        </li>
      ))}
    </ol>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={"text-sm " + (mono ? "font-mono" : "")}>{value}</dd>
    </div>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="space-y-1">
      <h2 className="text-sm font-medium">{title}</h2>
      {children}
    </section>
  );
}
