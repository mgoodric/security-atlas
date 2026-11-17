"use client";

// #2113 — /exceptions/[id]/{deny,activate,revoke}: the lifecycle actions
// that take input, each on its own route (parent #2071's detail-page
// pattern). Deny and revoke ask for a reason, which the platform writes on
// the exception's audit row; activate asks for an optional effective-from
// day (blank means now). On success the page returns to the exception
// with a toast (#2089); a refusal (403 for a role or the approval rule,
// 409 for a wrong state) shows inline beside the action.
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
import { fetchException, transitionException } from "@/lib/api/exceptions";

import {
  ACTION_COPY,
  ACTION_FROM,
  isFormAction,
  validateAction,
  type FormAction,
} from "../../lifecycle";

export default function ExceptionActionPage({
  params,
}: {
  params: Promise<{ id: string; action: string }>;
}) {
  const { id, action } = use(params);
  if (!isFormAction(action)) notFound();
  return <ActionForm id={id} action={action} />;
}

function ActionForm({ id, action }: { id: string; action: FormAction }) {
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const copy = ACTION_COPY[action];
  const back = `/exceptions/${encodeURIComponent(id)}`;

  const [reason, setReason] = useState("");
  const [effectiveOn, setEffectiveOn] = useState("");
  const [fieldErr, setFieldErr] = useState<string | null>(null);
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const exQ = useQuery({
    queryKey: ["exceptions", id],
    queryFn: () => fetchException(id),
    retry: (count, err) =>
      !(err instanceof APIError && [400, 404].includes(err.status)) &&
      count < 2,
  });
  if (
    exQ.error instanceof APIError &&
    (exQ.error.status === 404 || exQ.error.status === 400)
  ) {
    notFound();
  }
  const ex = exQ.data?.exception;
  const wrongState = ex && ex.status !== ACTION_FROM[action];

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitErr(null);
    const r = validateAction(action, { reason, effectiveOn });
    if (!r.ok) {
      setFieldErr(r.error);
      return;
    }
    setFieldErr(null);
    setSubmitting(true);
    try {
      const resp = await transitionException(id, action, r.body);
      qc.setQueryData(["exceptions", id], resp);
      await qc.invalidateQueries({ queryKey: ["exceptions"] });
      void invalidateApprovals(qc);
      toast(copy.done);
      router.push(back);
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSubmitting(false);
    }
  }

  return (
    <PageShell
      width="reading"
      title={copy.title}
      subtitle={copy.help}
      data-testid={`exception-${action}-page`}
      actions={
        <Link
          href={back}
          className={buttonVariants({ variant: "outline" })}
          data-testid="exception-action-back"
        >
          Back to exception
        </Link>
      }
    >
      {wrongState ? (
        <Alert data-testid="exception-action-wrong-state">
          <AlertTitle>Not available</AlertTitle>
          <AlertDescription>
            This exception is {ex.status}. {copy.onlyFrom}
          </AlertDescription>
        </Alert>
      ) : null}
      <form
        onSubmit={submit}
        noValidate
        className="space-y-4"
        data-testid={`exception-${action}-form`}
      >
        {action === "activate" ? (
          <div className="space-y-1">
            <label htmlFor="ex-effective" className="text-sm font-medium">
              Effective from
            </label>
            <p className="text-xs text-muted-foreground">
              Leave blank to start now.
            </p>
            <Input
              id="ex-effective"
              type="date"
              value={effectiveOn}
              onChange={(e) => setEffectiveOn(e.target.value)}
              data-testid="exception-action-effective"
              aria-invalid={fieldErr ? true : undefined}
              aria-describedby={fieldErr ? "ex-action-error" : undefined}
            />
          </div>
        ) : (
          <div className="space-y-1">
            <label htmlFor="ex-reason" className="text-sm font-medium">
              Reason
            </label>
            <textarea
              id="ex-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              rows={3}
              maxLength={2000}
              className="w-full rounded-md border bg-background px-2 py-1 text-sm"
              data-testid="exception-action-reason"
              aria-invalid={fieldErr ? true : undefined}
              aria-describedby={fieldErr ? "ex-action-error" : undefined}
            />
          </div>
        )}
        {fieldErr ? (
          <p id="ex-action-error" className="text-xs text-rose-600">
            {fieldErr}
          </p>
        ) : null}
        {submitErr ? (
          <Alert variant="destructive" data-testid="exception-action-error">
            <AlertTitle>Could not {action} the exception</AlertTitle>
            <AlertDescription>{submitErr}</AlertDescription>
          </Alert>
        ) : null}
        <Button
          type="submit"
          variant={action === "activate" ? "default" : "destructive"}
          disabled={submitting || Boolean(wrongState)}
          data-testid="exception-action-submit"
        >
          {submitting ? copy.busy : copy.submit}
        </Button>
      </form>
    </PageShell>
  );
}
