"use client";

// Slice 384 — action-plan create form (AC-24).
//
// Fields: title, description, triggering_event (free-text per P0-384-11),
// owner_id (user UUID), due_date, + searchable multi-select for linked risks
// (max 50) and linked controls (max 50). Bound to the BFF create flow
// (`createActionPlan` in lib/api/action-plans.ts), which POSTs the plan then
// links the selected risks + controls. No invented fields; no AI on this
// surface (P0-384-12). Native <textarea>/<label> per the vendor-form
// precedent (no new shadcn primitives).

import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { type CreateActionPlanInput } from "@/lib/api/action-plans";
import { fetchTenantControlsList } from "@/lib/api/controls-list";
import { fetchRisksList } from "@/lib/api/risks";

import { EntityMultiSelect, type SelectOption } from "./entity-multi-select";
import {
  hasErrors,
  MAX_LINKED,
  validateActionPlanForm,
  type FieldErrors,
} from "./validate";

type Props = {
  onSubmit: (body: CreateActionPlanInput) => Promise<void>;
};

export function ActionPlanForm({ onSubmit }: Props) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [triggeringEvent, setTriggeringEvent] = useState("");
  const [ownerId, setOwnerId] = useState("");
  const [dueDate, setDueDate] = useState("");
  const [riskIds, setRiskIds] = useState<string[]>([]);
  const [controlIds, setControlIds] = useState<string[]>([]);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const risksQ = useQuery({
    queryKey: ["action-plans", "risk-picker"],
    queryFn: fetchRisksList,
    staleTime: 30_000,
  });
  const controlsQ = useQuery({
    queryKey: ["action-plans", "control-picker"],
    queryFn: fetchTenantControlsList,
    staleTime: 30_000,
  });

  const riskOptions: SelectOption[] = useMemo(
    () =>
      (risksQ.data?.risks ?? []).map((r) => ({
        id: r.id,
        label: r.title,
        hint: r.category,
      })),
    [risksQ.data],
  );
  const controlOptions: SelectOption[] = useMemo(
    () =>
      (controlsQ.data ?? []).map((c) => ({
        id: c.id,
        label: c.title,
        hint: c.control_family,
      })),
    [controlsQ.data],
  );

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
        />
        {errors.title ? (
          <p
            className="text-xs text-rose-600"
            data-testid="action-plan-title-error"
          >
            {errors.title}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-owner" className="text-sm font-medium">
          Owner (user UUID)
        </label>
        <Input
          id="ap-owner"
          value={ownerId}
          onChange={(e) => setOwnerId(e.target.value)}
          placeholder="00000000-0000-0000-0000-000000000000"
          data-testid="action-plan-owner"
          aria-invalid={errors.ownerId ? true : undefined}
        />
        {errors.ownerId ? (
          <p
            className="text-xs text-rose-600"
            data-testid="action-plan-owner-error"
          >
            {errors.ownerId}
          </p>
        ) : null}
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
        />
        {errors.dueDate ? (
          <p
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

      <EntityMultiSelect
        legend="Linked risks"
        searchPlaceholder="Search risks…"
        testIdPrefix="action-plan-risks"
        max={MAX_LINKED}
        options={riskOptions}
        selectedIds={riskIds}
        onChange={setRiskIds}
        isLoading={risksQ.isLoading}
        isError={risksQ.isError}
        errorMessage={(risksQ.error as Error | undefined)?.message}
        emptyHint="No risks in this tenant yet — create one from the risk register first."
      />

      <EntityMultiSelect
        legend="Linked controls"
        searchPlaceholder="Search controls…"
        testIdPrefix="action-plan-controls"
        max={MAX_LINKED}
        options={controlOptions}
        selectedIds={controlIds}
        onChange={setControlIds}
        isLoading={controlsQ.isLoading}
        isError={controlsQ.isError}
        errorMessage={(controlsQ.error as Error | undefined)?.message}
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
