"use client";

// #2113 — /exceptions/new: request an exception (parent #2071).
//
// Opened from a control's "Request exception" with `?control_id=<id>`,
// which pre-fills the control. The form asks for the scope it covers
// (the #2103 scope picker, starting at Everything, the whole control),
// the justification, compensating controls and an expiry no more than
// 365 days out. On success it lands on the new exception with a toast
// (#2089).
//
// The picker emits null while "choose by dimension" has no value picked.
// That is an incomplete form: `validateRequest` builds no body for it and
// the submit stays refused, because the platform reads a stored null
// predicate as every cell. The whole control is sent as {"op":"true"}.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useMemo, useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { ScopePicker } from "@/components/scope/scope-picker";
import { EVERYTHING } from "@/components/scope/scope-picker-model";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { fetchTenantControlsList } from "@/lib/api/controls-list";
import { createException } from "@/lib/api/exceptions";

import {
  DEFAULT_DAYS,
  MAX_DAYS,
  dayAfter,
  validateRequest,
  type RequestErrors,
} from "../lifecycle";

export default function NewExceptionPage() {
  return (
    <PageShell
      width="reading"
      title="Request exception"
      subtitle="Waive a control for a bounded scope and time, with a justification an approver reviews. An exception lasts at most 365 days and is never renewed; file a new one to continue."
    >
      <Suspense fallback={null}>
        <RequestForm />
      </Suspense>
    </PageShell>
  );
}

function RequestForm() {
  const router = useRouter();
  const params = useSearchParams();
  const qc = useQueryClient();
  const { toast } = useToast();
  // The day the form opened; the expiry bounds are counted from it.
  const [now] = useState(() => new Date());

  const [controlId, setControlId] = useState(
    () => params.get("control_id") ?? "",
  );
  const [predicate, setPredicate] = useState<object | null>(EVERYTHING);
  const [justification, setJustification] = useState("");
  const [compensating, setCompensating] = useState("");
  const [expiresOn, setExpiresOn] = useState(() =>
    dayAfter(new Date(), DEFAULT_DAYS),
  );
  const [errors, setErrors] = useState<RequestErrors>({});
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const controlsQ = useQuery({
    queryKey: ["controls-list", "tenant"],
    queryFn: fetchTenantControlsList,
    staleTime: 30_000,
  });
  const controls = useMemo(() => controlsQ.data ?? [], [controlsQ.data]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitErr(null);
    const r = validateRequest(
      { controlId, predicate, justification, compensating, expiresOn },
      now,
    );
    if (!r.ok) {
      setErrors(r.errors);
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      const { exception } = await createException(r.body);
      await qc.invalidateQueries({ queryKey: ["exceptions"] });
      toast("Exception requested");
      router.push(`/exceptions/${encodeURIComponent(exception.id)}`);
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSubmitting(false);
    }
  }

  return (
    <form
      onSubmit={submit}
      noValidate
      className="space-y-6"
      data-testid="exception-request-form"
    >
      <div className="space-y-1">
        <label htmlFor="ex-control" className="text-sm font-medium">
          Control
        </label>
        <select
          id="ex-control"
          value={controlId}
          onChange={(e) => setControlId(e.target.value)}
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm"
          data-testid="exception-request-control"
          aria-invalid={errors.control ? true : undefined}
          aria-describedby={errors.control ? "ex-control-error" : undefined}
        >
          <option value="">
            {controlsQ.isLoading ? "Loading controls…" : "Choose a control"}
          </option>
          {controlId && !controls.some((c) => c.id === controlId) ? (
            <option value={controlId}>{controlId}</option>
          ) : null}
          {controls.map((c) => (
            <option key={c.id} value={c.id}>
              {c.scf_id} {c.title}
            </option>
          ))}
        </select>
        {errors.control ? (
          <p id="ex-control-error" className="text-xs text-rose-600">
            {errors.control}
          </p>
        ) : null}
      </div>

      <div className="space-y-1" data-testid="exception-request-scope">
        <ScopePicker value={EVERYTHING} onChange={setPredicate} />
        {errors.scope ? (
          <p
            className="text-xs text-rose-600"
            role="alert"
            data-testid="exception-request-scope-error"
          >
            {errors.scope}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ex-justification" className="text-sm font-medium">
          Justification
        </label>
        <textarea
          id="ex-justification"
          value={justification}
          onChange={(e) => setJustification(e.target.value)}
          rows={4}
          maxLength={4000}
          className="w-full rounded-md border bg-background px-2 py-1 text-sm"
          data-testid="exception-request-justification"
          aria-invalid={errors.justification ? true : undefined}
          aria-describedby={
            errors.justification ? "ex-justification-error" : undefined
          }
        />
        {errors.justification ? (
          <p id="ex-justification-error" className="text-xs text-rose-600">
            {errors.justification}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ex-compensating" className="text-sm font-medium">
          Compensating controls
        </label>
        <p className="text-xs text-muted-foreground">
          What you are doing instead, one per line. Optional.
        </p>
        <textarea
          id="ex-compensating"
          value={compensating}
          onChange={(e) => setCompensating(e.target.value)}
          rows={3}
          maxLength={4000}
          className="w-full rounded-md border bg-background px-2 py-1 text-sm"
          data-testid="exception-request-compensating"
        />
      </div>

      <div className="space-y-1">
        <label htmlFor="ex-expires" className="text-sm font-medium">
          Expires on
        </label>
        <Input
          id="ex-expires"
          type="date"
          value={expiresOn}
          min={dayAfter(now, 1)}
          max={dayAfter(now, MAX_DAYS)}
          onChange={(e) => setExpiresOn(e.target.value)}
          data-testid="exception-request-expires"
          aria-invalid={errors.expires ? true : undefined}
          aria-describedby={errors.expires ? "ex-expires-error" : undefined}
        />
        {errors.expires ? (
          <p id="ex-expires-error" className="text-xs text-rose-600">
            {errors.expires}
          </p>
        ) : null}
      </div>

      {submitErr ? (
        <Alert variant="destructive" data-testid="exception-request-error">
          <AlertTitle>Could not request the exception</AlertTitle>
          <AlertDescription>{submitErr}</AlertDescription>
        </Alert>
      ) : null}

      <Button
        type="submit"
        disabled={submitting || predicate === null}
        data-testid="exception-request-submit"
      >
        {submitting ? "Requesting…" : "Request exception"}
      </Button>
    </form>
  );
}
