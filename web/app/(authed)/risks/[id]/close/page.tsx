"use client";

// #2107 — /risks/[id]/close: close an open risk with a reason (no longer
// applicable, merged into another risk, realised and handled) and an
// optional note. A closed risk keeps its history and leaves the register,
// its counts and the top risks until it is reopened from its page. On
// success the page returns to the risk with a toast (#2089); a refusal
// shows inline.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { useToast } from "@/components/ui/toast";
import { APIError } from "@/lib/api/base";
import { fetchRiskDetail, transitionRisk } from "@/lib/api/risks";

import {
  CLOSURE_REASONS,
  closureReasonLabel,
  validateClose,
} from "../../lifecycle";

const HELP: Record<(typeof CLOSURE_REASONS)[number], string> = {
  not_applicable:
    "The risk no longer applies: the asset, activity or vendor is gone.",
  merged: "Another risk in the register now covers it. Name it in the note.",
  realised_and_handled:
    "The risk happened and was dealt with. Link the incident in the note.",
};

export default function RiskClosePage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const back = `/risks/${encodeURIComponent(id)}`;

  const [reason, setReason] = useState("");
  const [note, setNote] = useState("");
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
  const alreadyClosed = risk?.status === "closed";

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitErr(null);
    const r = validateClose({ reason, note });
    if (!r.ok) {
      setFieldErr(r.error);
      return;
    }
    setFieldErr(null);
    setSubmitting(true);
    try {
      await transitionRisk(id, "close", r.body);
      await qc.invalidateQueries({ queryKey: ["risk", id] });
      await qc.invalidateQueries({ queryKey: ["risks"] });
      toast("Risk closed");
      router.push(back);
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSubmitting(false);
    }
  }

  return (
    <PageShell
      width="reading"
      title="Close risk"
      subtitle={risk?.title}
      data-testid="risk-close-page"
      actions={
        <Link
          href={back}
          className={buttonVariants({ variant: "outline" })}
          data-testid="risk-close-back"
        >
          Back to risk
        </Link>
      }
    >
      {alreadyClosed ? (
        <Alert data-testid="risk-close-already-closed">
          <AlertTitle>Already closed</AlertTitle>
          <AlertDescription>
            This risk is closed. Reopen it from its page.
          </AlertDescription>
        </Alert>
      ) : null}
      <p className="text-sm text-muted-foreground">
        A closed risk keeps its history. It leaves the register, its counts and
        the top risks until it is reopened. Only an admin can delete a risk.
      </p>
      <form
        onSubmit={submit}
        noValidate
        className="space-y-4"
        data-testid="risk-close-form"
      >
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Reason</legend>
          {CLOSURE_REASONS.map((r) => (
            <label key={r} className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="reason"
                value={r}
                checked={reason === r}
                onChange={() => setReason(r)}
                className="mt-1"
                data-testid={`risk-close-reason-${r}`}
              />
              <span>
                <span className="font-medium">{closureReasonLabel(r)}</span>
                <span className="block text-xs text-muted-foreground">
                  {HELP[r]}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
        <div className="space-y-1">
          <label htmlFor="risk-close-note" className="text-sm font-medium">
            Note <span className="text-muted-foreground">(optional)</span>
          </label>
          <textarea
            id="risk-close-note"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            rows={3}
            maxLength={4000}
            className="w-full rounded-md border bg-background px-2 py-1 text-sm"
            data-testid="risk-close-note"
          />
        </div>
        {fieldErr ? (
          <p
            className="text-xs text-rose-600"
            data-testid="risk-close-field-error"
          >
            {fieldErr}
          </p>
        ) : null}
        {submitErr ? (
          <Alert variant="destructive" data-testid="risk-close-error">
            <AlertTitle>Could not close the risk</AlertTitle>
            <AlertDescription>{submitErr}</AlertDescription>
          </Alert>
        ) : null}
        <Button
          type="submit"
          disabled={submitting || alreadyClosed}
          data-testid="risk-close-submit"
        >
          {submitting ? "Closing…" : "Close risk"}
        </Button>
      </form>
    </PageShell>
  );
}
