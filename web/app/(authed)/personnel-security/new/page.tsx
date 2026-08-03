"use client";

// OE-664 — manual checklist create form (source=manual; invariant 9:
// manual create/complete is the primary UX). Fields mirror the OE-663
// create wire (`createReq` in internal/api/personnelsecurity/handlers.go):
// kind, person_external_id (required), person_work_email,
// person_display_name, due date (optional — the store defaults it by
// kind when omitted). Native <label>/<select> per the action-plan-form
// precedent; no new shadcn primitives.

import { useRouter } from "next/navigation";
import { useState } from "react";

import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import Link from "next/link";

import {
  createPersonnelChecklist,
  type ChecklistKind,
} from "@/lib/api/personnel-security";

export default function NewPersonnelChecklistPage() {
  const router = useRouter();
  const [kind, setKind] = useState<ChecklistKind>("onboarding");
  const [personId, setPersonId] = useState("");
  const [workEmail, setWorkEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [dueDate, setDueDate] = useState("");
  const [fieldErr, setFieldErr] = useState<string | null>(null);
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitErr(null);
    if (!personId.trim()) {
      setFieldErr("Person ID is required.");
      return;
    }
    setFieldErr(null);
    setSubmitting(true);
    try {
      const created = await createPersonnelChecklist({
        kind,
        person_external_id: personId.trim(),
        person_work_email: workEmail.trim(),
        person_display_name: displayName.trim(),
        // <input type="date"> yields YYYY-MM-DD; upstream wants RFC 3339.
        ...(dueDate ? { due_at: `${dueDate}T00:00:00Z` } : {}),
      });
      router.push(
        `/personnel-security/${encodeURIComponent(created.checklist.id)}`,
      );
    } catch (err) {
      setSubmitErr((err as Error).message);
      setSubmitting(false);
    }
  };

  return (
    <div className="space-y-6 max-w-xl">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">
            New personnel checklist
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Creates a manual onboarding or offboarding checklist for one person.
            Completing items records evidence — nothing here provisions or
            deprovisions access.
          </p>
        </div>
        <Link
          href="/personnel-security"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Back to list
        </Link>
      </div>

      <form
        onSubmit={submit}
        className="space-y-4"
        data-testid="personnel-create-form"
      >
        <div className="space-y-1">
          <label htmlFor="ps-kind" className="text-sm font-medium">
            Kind
          </label>
          <select
            id="ps-kind"
            value={kind}
            onChange={(e) => setKind(e.target.value as ChecklistKind)}
            className="w-full rounded-md border bg-transparent px-3 py-2 text-sm"
            data-testid="personnel-create-kind"
          >
            <option value="onboarding">Onboarding</option>
            <option value="offboarding">Offboarding</option>
          </select>
        </div>

        <div className="space-y-1">
          <label htmlFor="ps-person-id" className="text-sm font-medium">
            Person ID
          </label>
          <Input
            id="ps-person-id"
            value={personId}
            onChange={(e) => setPersonId(e.target.value)}
            placeholder="HRIS / directory identifier"
            data-testid="personnel-create-person-id"
            aria-invalid={fieldErr ? true : undefined}
          />
          {fieldErr ? (
            <p
              className="text-xs text-rose-600"
              data-testid="personnel-create-person-id-error"
            >
              {fieldErr}
            </p>
          ) : null}
        </div>

        <div className="space-y-1">
          <label htmlFor="ps-email" className="text-sm font-medium">
            Work email
          </label>
          <Input
            id="ps-email"
            type="email"
            value={workEmail}
            onChange={(e) => setWorkEmail(e.target.value)}
            data-testid="personnel-create-email"
          />
        </div>

        <div className="space-y-1">
          <label htmlFor="ps-name" className="text-sm font-medium">
            Display name
          </label>
          <Input
            id="ps-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            data-testid="personnel-create-name"
          />
        </div>

        <div className="space-y-1">
          <label htmlFor="ps-due" className="text-sm font-medium">
            Due date
          </label>
          <Input
            id="ps-due"
            type="date"
            value={dueDate}
            onChange={(e) => setDueDate(e.target.value)}
            data-testid="personnel-create-due"
          />
          <p className="text-xs text-muted-foreground">
            Optional — defaults by kind when left empty.
          </p>
        </div>

        {submitErr ? (
          <p
            className="text-sm text-rose-600"
            data-testid="personnel-create-error"
          >
            {submitErr}
          </p>
        ) : null}

        <Button
          type="submit"
          disabled={submitting}
          data-testid="personnel-create-submit"
        >
          {submitting ? "Creating…" : "Create checklist"}
        </Button>
      </form>
    </div>
  );
}
