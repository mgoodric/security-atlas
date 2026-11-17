"use client";

// #2105 — /risks/[id]/acceptance/{propose,approve,reject,revoke}: the
// acceptance transitions, each on its own route (parent #2071's
// detail-page pattern). Propose asks for a justification and a proposed
// expiry; approve for the date the acceptance lapses, starting from the
// proposed one; reject for an optional reason; revoke for a required one.
// On success the page returns to the risk with a toast (#2089); a refusal
// (403 for the approver role or the approval rule, 409 for a wrong state)
// shows inline beside the action.
//
// Any other action segment is not found.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { invalidateApprovals } from "@/lib/api/approvals";
import { APIError } from "@/lib/api/base";
import { fetchRiskDetail, transitionRisk } from "@/lib/api/risks";

import {
  ACTION_COPY,
  ACTION_FROM,
  acceptanceState,
  dayAfter,
  isAcceptanceAction,
  validateAcceptance,
  type AcceptanceAction,
} from "../../../lifecycle";

export default function RiskAcceptancePage({
  params,
}: {
  params: Promise<{ id: string; action: string }>;
}) {
  const { id, action } = use(params);
  if (!isAcceptanceAction(action)) notFound();
  return <AcceptanceForm id={id} action={action} />;
}

function AcceptanceForm({
  id,
  action,
}: {
  id: string;
  action: AcceptanceAction;
}) {
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const copy = ACTION_COPY[action];
  const back = `/risks/${encodeURIComponent(id)}`;

  const [justification, setJustification] = useState("");
  const [until, setUntil] = useState(() =>
    action === "propose" ? dayAfter(new Date(), 90) : "",
  );
  const [reason, setReason] = useState("");
  const [fieldErr, setFieldErr] = useState<string | null>(null);
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const riskQ = useQuery({
    queryKey: ["risk", id],
    queryFn: () => fetchRiskDetail(id),
    retry: (count, err) =>
      !(err instanceof APIError && [400, 404].includes(err.status)) &&
      count < 2,
  });
  if (
    riskQ.error instanceof APIError &&
    (riskQ.error.status === 404 || riskQ.error.status === 400)
  ) {
    notFound();
  }
  const risk = riskQ.data?.risk;
  const state = risk ? acceptanceState(risk) : null;
  const wrongState = state !== null && state !== ACTION_FROM[action];

  // Approve starts from the proposed expiry; the approver may change it.
  const proposedUntil = risk?.acceptance?.proposed_until ?? "";
  const untilValue = until || (action === "approve" ? proposedUntil : "");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitErr(null);
    const r = validateAcceptance(
      action,
      { justification, until: untilValue, reason },
      new Date(),
    );
    if (!r.ok) {
      setFieldErr(r.error);
      return;
    }
    setFieldErr(null);
    setSubmitting(true);
    try {
      await transitionRisk(id, `acceptance/${action}`, r.body);
      await qc.invalidateQueries({ queryKey: ["risk", id] });
      await qc.invalidateQueries({ queryKey: ["risks"] });
      void invalidateApprovals(qc);
      toast(copy.done);
      router.push(back);
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSubmitting(false);
    }
  }

  const describedBy = fieldErr ? "risk-acceptance-error" : undefined;
  return (
    <PageShell
      width="reading"
      title={copy.title}
      subtitle={risk ? `${risk.title}. ${copy.help}` : copy.help}
      data-testid={`risk-acceptance-${action}-page`}
      actions={
        <Link
          href={back}
          className={buttonVariants({ variant: "outline" })}
          data-testid="risk-acceptance-back"
        >
          Back to risk
        </Link>
      }
    >
      {wrongState ? (
        <Alert data-testid="risk-acceptance-wrong-state">
          <AlertTitle>Not available</AlertTitle>
          <AlertDescription>{copy.onlyFrom}</AlertDescription>
        </Alert>
      ) : null}
      <form
        onSubmit={submit}
        noValidate
        className="space-y-4"
        data-testid={`risk-acceptance-${action}-form`}
      >
        {action === "propose" ? (
          <div className="space-y-1">
            <label
              htmlFor="risk-acc-justification"
              className="text-sm font-medium"
            >
              Justification
            </label>
            <textarea
              id="risk-acc-justification"
              value={justification}
              onChange={(e) => setJustification(e.target.value)}
              rows={4}
              maxLength={4000}
              className="w-full rounded-md border bg-background px-2 py-1 text-sm"
              data-testid="risk-acceptance-justification"
              aria-invalid={fieldErr ? true : undefined}
              aria-describedby={describedBy}
            />
          </div>
        ) : null}
        {action === "propose" || action === "approve" ? (
          <div className="space-y-1">
            <label htmlFor="risk-acc-until" className="text-sm font-medium">
              {action === "propose" ? "Proposed expiry" : "Accepted until"}
            </label>
            <p className="text-xs text-muted-foreground">
              {action === "propose"
                ? "The approver confirms or changes it."
                : `Proposed: ${proposedUntil || "none"}.`}
            </p>
            <Input
              id="risk-acc-until"
              type="date"
              value={untilValue}
              onChange={(e) => setUntil(e.target.value)}
              data-testid="risk-acceptance-until"
              aria-invalid={fieldErr ? true : undefined}
              aria-describedby={describedBy}
            />
          </div>
        ) : null}
        {action === "reject" || action === "revoke" ? (
          <div className="space-y-1">
            <label htmlFor="risk-acc-reason" className="text-sm font-medium">
              Reason{action === "reject" ? " (optional)" : ""}
            </label>
            <textarea
              id="risk-acc-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              rows={3}
              maxLength={2000}
              className="w-full rounded-md border bg-background px-2 py-1 text-sm"
              data-testid="risk-acceptance-reason"
              aria-invalid={fieldErr ? true : undefined}
              aria-describedby={describedBy}
            />
          </div>
        ) : null}
        {fieldErr ? (
          <p id="risk-acceptance-error" className="text-xs text-rose-600">
            {fieldErr}
          </p>
        ) : null}
        {submitErr ? (
          <Alert
            variant="destructive"
            data-testid="risk-acceptance-submit-error"
          >
            <AlertTitle>Could not {action} the acceptance</AlertTitle>
            <AlertDescription>{submitErr}</AlertDescription>
          </Alert>
        ) : null}
        <Button
          type="submit"
          variant={
            action === "reject" || action === "revoke"
              ? "destructive"
              : "default"
          }
          disabled={submitting || wrongState}
          data-testid="risk-acceptance-submit"
        >
          {submitting ? copy.busy : copy.submit}
        </Button>
      </form>
    </PageShell>
  );
}
