"use client";

// #2108 — `/action-plans/[id]/edit`: edit a plan's title, description,
// owner and due date.
//
// The plan page's shared header (DetailHeader) links here. The owner is
// picked by name with the shared EntityPicker (#2086); the platform checks
// that the owner is an active user of the tenant. Only changed fields are
// sent to `PATCH /api/action-plans/{id}`; on success the form returns to
// the plan with a toast. A refusal renders in the form with the input
// kept. Status is not on this form: it moves through the plan page's
// transitions.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { EntityPicker } from "@/components/ui/entity-picker";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import {
  fetchActionPlan,
  updateActionPlan,
  type ActionPlan,
} from "@/lib/api/action-plans";
import type { EntityKind } from "@/lib/entity-picker";

import {
  hasErrors,
  validateActionPlanForm,
  type FieldErrors,
} from "../../new/validate";
import { changedFields, dayOf } from "./fields";

const OWNER_KINDS: readonly EntityKind[] = ["user"];

export default function ActionPlanEditPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const detailHref = `/action-plans/${encodeURIComponent(id)}`;

  // Shares the detail page's key, so the form opens on the plan the
  // operator was just reading.
  const planQ = useQuery({
    queryKey: ["action-plans", "detail", id],
    queryFn: () => fetchActionPlan(id),
    enabled: Boolean(id),
  });

  return (
    <PageShell
      title="Edit action plan"
      subtitle="Change the plan's title, description, owner and due date. Its status moves through the actions on the plan's page."
      width="reading"
      data-testid="action-plan-edit"
      actions={
        <Link
          href={detailHref}
          className="text-sm text-muted-foreground hover:underline"
          data-testid="action-plan-edit-back"
        >
          ← Action plan
        </Link>
      }
    >
      {planQ.isLoading ? <Skeleton className="h-96 w-full" /> : null}
      {planQ.isError ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load action plan</AlertTitle>
          <AlertDescription>{(planQ.error as Error).message}</AlertDescription>
        </Alert>
      ) : null}
      {planQ.data ? (
        <EditForm plan={planQ.data.action_plan} detailHref={detailHref} />
      ) : null}
    </PageShell>
  );
}

function EditForm({
  plan,
  detailHref,
}: {
  plan: ActionPlan;
  detailHref: string;
}) {
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [title, setTitle] = useState(plan.title);
  const [description, setDescription] = useState(plan.description ?? "");
  const [ownerId, setOwnerId] = useState(plan.owner_id);
  const [dueDate, setDueDate] = useState(dayOf(plan.due_date));
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitErr(null);
    const errs = validateActionPlanForm({
      title,
      description,
      triggeringEvent: "",
      ownerId,
      dueDate,
      riskIds: [],
      controlIds: [],
    });
    setErrors(errs);
    if (hasErrors(errs)) return;

    const body = changedFields(plan, { title, description, ownerId, dueDate });
    if (Object.keys(body).length === 0) {
      toast("No changes to save");
      router.push(detailHref);
      return;
    }
    setSaving(true);
    try {
      await updateActionPlan(plan.id, body);
      await qc.invalidateQueries({
        queryKey: ["action-plans", "detail", plan.id],
      });
      void qc.invalidateQueries({ queryKey: ["action-plans", "list"] });
      toast("Action plan updated");
      router.push(detailHref);
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSaving(false);
    }
  };

  return (
    <form
      onSubmit={submit}
      noValidate
      className="space-y-4"
      data-testid="action-plan-edit-form"
    >
      <div className="space-y-1">
        <label htmlFor="ap-edit-title" className="text-sm font-medium">
          Title
        </label>
        <Input
          id="ap-edit-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          maxLength={200}
          data-testid="action-plan-edit-title"
          aria-invalid={errors.title ? true : undefined}
          aria-describedby={errors.title ? "ap-edit-title-error" : undefined}
        />
        {errors.title ? (
          <p id="ap-edit-title-error" className="text-xs text-rose-600">
            {errors.title}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-edit-owner" className="text-sm font-medium">
          Owner
        </label>
        <EntityPicker
          kinds={OWNER_KINDS}
          module="risk"
          id="ap-edit-owner"
          value={ownerId}
          onChange={setOwnerId}
          placeholder="Search people, or paste a user ID"
          testId="action-plan-edit-owner"
          errorId="action-plan-edit-owner-error"
          error={errors.ownerId}
        />
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-edit-due" className="text-sm font-medium">
          Due date
        </label>
        <Input
          id="ap-edit-due"
          type="date"
          value={dueDate}
          onChange={(e) => setDueDate(e.target.value)}
          data-testid="action-plan-edit-due-date"
          aria-invalid={errors.dueDate ? true : undefined}
          aria-describedby={errors.dueDate ? "ap-edit-due-error" : undefined}
        />
        {errors.dueDate ? (
          <p id="ap-edit-due-error" className="text-xs text-rose-600">
            {errors.dueDate}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="ap-edit-desc" className="text-sm font-medium">
          Description
        </label>
        <textarea
          id="ap-edit-desc"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          maxLength={4000}
          rows={6}
          className="w-full rounded-md border bg-background px-2 py-1 text-sm"
          data-testid="action-plan-edit-description"
        />
        {errors.description ? (
          <p className="text-xs text-rose-600">{errors.description}</p>
        ) : null}
      </div>

      {submitErr ? (
        <Alert variant="destructive" data-testid="action-plan-edit-error">
          <AlertTitle>Could not save the action plan</AlertTitle>
          <AlertDescription>{submitErr}</AlertDescription>
        </Alert>
      ) : null}

      <div className="flex items-center gap-2">
        <Button
          type="submit"
          disabled={saving}
          data-testid="action-plan-edit-submit"
        >
          {saving ? "Saving…" : "Save changes"}
        </Button>
        <Link
          href={detailHref}
          className="text-sm text-muted-foreground hover:underline"
        >
          Cancel
        </Link>
      </div>
    </form>
  );
}
