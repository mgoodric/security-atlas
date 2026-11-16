"use client";

// #2105 — /risks/[id]/treatment: change a risk's treatment to untreated,
// mitigate, transfer or avoid. Accepting a risk is proposed instead
// (/risks/[id]/acceptance/propose), and a risk with an acceptance proposed
// or approved changes treatment only after it is rejected or revoked.
// The platform validates the treatment as create does: mitigate needs a
// linked control or action plan, transfer an instrument reference. On
// success the page returns to the risk with a toast (#2089); a refusal
// shows inline.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { APIError } from "@/lib/api/base";
import { fetchRiskDetail, transitionRisk } from "@/lib/api/risks";
import { enumLabel } from "@/lib/labels/enum-labels";

import {
  CHANGE_TREATMENTS,
  acceptanceState,
  validateTreatmentChange,
} from "../../lifecycle";

const HELP: Record<string, string> = {
  untreated: "No treatment decision yet.",
  mitigate: "Needs at least one linked control or action plan.",
  transfer: "Needs the insurance policy, contract or SOW it relies on.",
  avoid: "The activity behind the risk stops.",
};

export default function RiskTreatmentPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const back = `/risks/${encodeURIComponent(id)}`;

  const [treatment, setTreatment] = useState<string | null>(null);
  const [instrument, setInstrument] = useState<string | null>(null);
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
  const inFlight = risk ? acceptanceState(risk) !== "none" : false;
  // The form starts from the risk's own values until the user changes them.
  const chosen = treatment ?? risk?.treatment ?? "";
  const instrumentValue = instrument ?? risk?.instrument_reference ?? "";

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitErr(null);
    const r = validateTreatmentChange({
      treatment: chosen,
      instrumentReference: instrumentValue,
    });
    if (!r.ok) {
      setFieldErr(r.error);
      return;
    }
    setFieldErr(null);
    setSubmitting(true);
    try {
      await transitionRisk(id, "treatment", r.body);
      await qc.invalidateQueries({ queryKey: ["risk", id] });
      await qc.invalidateQueries({ queryKey: ["risks"] });
      toast("Treatment changed");
      router.push(back);
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSubmitting(false);
    }
  }

  return (
    <PageShell
      width="reading"
      title="Change treatment"
      subtitle={risk?.title}
      data-testid="risk-treatment-page"
      actions={
        <Link
          href={back}
          className={buttonVariants({ variant: "outline" })}
          data-testid="risk-treatment-back"
        >
          Back to risk
        </Link>
      }
    >
      {inFlight ? (
        <Alert data-testid="risk-treatment-acceptance-in-flight">
          <AlertTitle>Acceptance in progress</AlertTitle>
          <AlertDescription>
            This risk has an acceptance {risk?.acceptance_status}. Reject or
            revoke it before changing the treatment.
          </AlertDescription>
        </Alert>
      ) : null}
      <form
        onSubmit={submit}
        noValidate
        className="space-y-4"
        data-testid="risk-treatment-form"
      >
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Treatment</legend>
          {CHANGE_TREATMENTS.map((t) => (
            <label key={t} className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="treatment"
                value={t}
                checked={chosen === t}
                onChange={() => setTreatment(t)}
                className="mt-1"
                data-testid={`risk-treatment-option-${t}`}
              />
              <span>
                <span className="font-medium">
                  {enumLabel("RiskTreatment", t)}
                </span>
                <span className="block text-xs text-muted-foreground">
                  {HELP[t]}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
        {chosen === "transfer" ? (
          <div className="space-y-1">
            <label
              htmlFor="risk-treatment-instrument"
              className="text-sm font-medium"
            >
              Instrument reference
            </label>
            <Input
              id="risk-treatment-instrument"
              value={instrumentValue}
              onChange={(e) => setInstrument(e.target.value)}
              data-testid="risk-treatment-instrument"
            />
          </div>
        ) : null}
        {fieldErr ? (
          <p
            className="text-xs text-rose-600"
            data-testid="risk-treatment-field-error"
          >
            {fieldErr}
          </p>
        ) : null}
        {submitErr ? (
          <Alert variant="destructive" data-testid="risk-treatment-error">
            <AlertTitle>Could not change the treatment</AlertTitle>
            <AlertDescription>{submitErr}</AlertDescription>
          </Alert>
        ) : null}
        <Button
          type="submit"
          disabled={submitting || inFlight}
          data-testid="risk-treatment-submit"
        >
          {submitting ? "Saving…" : "Change treatment"}
        </Button>
      </form>
    </PageShell>
  );
}
