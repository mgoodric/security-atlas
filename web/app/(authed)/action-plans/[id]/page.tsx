"use client";

// Slice 384 — /action-plans/[id] detail view (AC-23). Shows all fields plus
// the linked risks and controls (read shape: `GET /v1/action-plans/{id}`,
// single round-trip). Mirrors the slice-681 /risks/[id] read-only detail
// shape. Tenant isolation is RLS-enforced (invariant 6); a cross-tenant id
// resolves to a clean upstream 404 -> in-shell not-found.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams } from "next/navigation";

import { Markdown } from "@/components/markdown";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { Person } from "@/components/ui/person";
import {
  fetchActionPlan,
  type ActionPlanDetailResponse,
} from "@/lib/api/action-plans";

import { dateLabel, statusLabel, statusPillClass } from "../status";

export default function ActionPlanDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;

  const planQ = useQuery<ActionPlanDetailResponse>({
    queryKey: ["action-plans", "detail", id],
    queryFn: () => fetchActionPlan(id),
    enabled: Boolean(id),
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

  return (
    <PageShell
      width="reading"
      data-testid="action-plan-detail"
      title={<span data-testid="action-plan-detail-title">{plan.title}</span>}
      subtitle={
        <div className="flex items-center gap-2">
          <span
            className={
              "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11px] font-medium " +
              statusPillClass(plan.status)
            }
            data-testid="action-plan-detail-status"
          >
            {statusLabel(plan.status)}
          </span>
          <span className="text-xs text-muted-foreground">
            Due {dateLabel(plan.due_date)}
          </span>
        </div>
      }
      actions={
        <Link
          href="/action-plans"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Back to list
        </Link>
      }
    >
      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <dt className="text-xs text-muted-foreground">Owner</dt>
          <dd className="text-sm">
            <Person
              id={plan.owner_id}
              avatar
              testId="action-plan-detail-owner"
            />
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
    </PageShell>
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
