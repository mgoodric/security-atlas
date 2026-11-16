"use client";

// Slice 384 — action-plan create form (AC-24).
//
// Fields: title, description, triggering_event (free-text per P0-384-11),
// owner_id (picked by name, #2086), due_date, + searchable multi-select for linked risks
// (max 50) and linked controls (max 50). Bound to the BFF create flow
// (`createActionPlan` in lib/api/action-plans.ts), which POSTs the plan then
// links the selected risks + controls. No invented fields; no AI on this
// surface (P0-384-12). Native <textarea>/<label> per the vendor-form
// precedent (no new shadcn primitives).

import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { EntityMultiPicker, EntityPicker } from "@/components/ui/entity-picker";
import { Input } from "@/components/ui/input";
import { type CreateActionPlanInput } from "@/lib/api/action-plans";
import type { EntityKind } from "@/lib/entity-picker";

import {
  hasErrors,
  MAX_LINKED,
  validateActionPlanForm,
  type FieldErrors,
} from "./validate";

type Props = {
  onSubmit: (body: CreateActionPlanInput) => Promise<void>;
  // #2106: risks the form starts with linked ("Add action plan" on a risk).
  initialRiskIds?: readonly string[];
};

const OWNER_KINDS: readonly EntityKind[] = ["user"];
const RISK_KINDS: readonly EntityKind[] = ["risk"];
const CONTROL_KINDS: readonly EntityKind[] = ["control"];

export function ActionPlanForm({ onSubmit, initialRiskIds = [] }: Props) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [triggeringEvent, setTriggeringEvent] = useState("");
  const [ownerId, setOwnerId] = useState("");
  const [dueDate, setDueDate] = useState("");
  const [riskIds, setRiskIds] = useState<string[]>(() => [...initialRiskIds]);
  const [controlIds, setControlIds] = useState<string[]>([]);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitErr(null);
    const values = {
      title,
      description,
      triggeringEvent,
      ownerId,
      dueDate,
      riskIds,
      controlIds,
    };
    const errs = validateActionPlanForm(values);
    setErrors(errs);
    if (hasErrors(errs)) return;

    setSubmitting(true);
    try {
      await onSubmit({
        title: title.trim(),
        description: description.trim() || undefined,
        triggering_event: triggeringEvent.trim() || undefined,
        owner_id: ownerId.trim(),
        due_date: dueDate || null,
        risk_ids: riskIds,
        control_ids: controlIds,
      });
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSubmitting(false);
    }
  };

  return (
    <form
      onSubmit={submit}
      noValidate
      className="space-y-4"
      data-testid="action-plan-form"
    >
      <div className="space-y-1">
        <label htmlFor="ap-title" className="text-sm font-medium">
          Title
        </label>
        <Input
          id="ap-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          maxLength={200}
          data-testid="action-plan-title"
          aria-invalid={errors.title ? true : undefined}
          aria-describedby={errors.title ? "ap-title-error" : undefined}
        />
        {errors.title ? (
          <p
            id="ap-title-error"
            className="text-xs text-rose-600"
            data-testid="action-plan-title-error"
          >
            {errors.title}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-owner" className="text-sm font-medium">
          Owner
        </label>
        <EntityPicker
          kinds={OWNER_KINDS}
          id="ap-owner"
          value={ownerId}
          onChange={setOwnerId}
          placeholder="Search people, or paste a user ID"
          testId="action-plan-owner"
          errorId="action-plan-owner-error"
          error={errors.ownerId}
        />
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-due" className="text-sm font-medium">
          Due date
        </label>
        <Input
          id="ap-due"
          type="date"
          value={dueDate}
          onChange={(e) => setDueDate(e.target.value)}
          data-testid="action-plan-due-date"
          aria-invalid={errors.dueDate ? true : undefined}
          aria-describedby={errors.dueDate ? "ap-due-error" : undefined}
        />
        {errors.dueDate ? (
          <p
            id="ap-due-error"
            className="text-xs text-rose-600"
            data-testid="action-plan-due-error"
          >
            {errors.dueDate}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-trigger" className="text-sm font-medium">
          Triggering event
        </label>
        <Input
          id="ap-trigger"
          value={triggeringEvent}
          onChange={(e) => setTriggeringEvent(e.target.value)}
          maxLength={500}
          placeholder="e.g. Customer X Q2 2026 TPRM finding #4"
          data-testid="action-plan-triggering-event"
        />
        {errors.triggeringEvent ? (
          <p className="text-xs text-rose-600">{errors.triggeringEvent}</p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-desc" className="text-sm font-medium">
          Description
        </label>
        <textarea
          id="ap-desc"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          maxLength={4000}
          rows={4}
          className="w-full rounded-md border bg-background px-2 py-1 text-sm"
          data-testid="action-plan-description"
        />
        {errors.description ? (
          <p className="text-xs text-rose-600">{errors.description}</p>
        ) : null}
      </div>

      <EntityMultiPicker
        kinds={RISK_KINDS}
        legend="Linked risks"
        searchPlaceholder="Search risks…"
        testId="action-plan-risks"
        max={MAX_LINKED}
        value={riskIds}
        onChange={setRiskIds}
        emptyHint="No risks in this tenant yet — create one from the risk register first."
      />

      <EntityMultiPicker
        kinds={CONTROL_KINDS}
        legend="Linked controls"
        searchPlaceholder="Search controls…"
        testId="action-plan-controls"
        max={MAX_LINKED}
        value={controlIds}
        onChange={setControlIds}
        emptyHint="No controls in this tenant yet — upload a control bundle first."
      />

      {errors.links ? (
        <p className="text-xs text-rose-600">{errors.links}</p>
      ) : null}

      {submitErr ? (
        <Alert variant="destructive" data-testid="action-plan-submit-error">
          <AlertTitle>Could not create action plan</AlertTitle>
          <AlertDescription>{submitErr}</AlertDescription>
        </Alert>
      ) : null}

      <div className="flex items-center gap-2">
        <Button
          type="submit"
          disabled={submitting}
          data-testid="action-plan-submit"
        >
          {submitting ? "Creating…" : "Create action plan"}
        </Button>
      </div>
    </form>
  );
}
