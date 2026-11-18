"use client";

// #2116 — the policy form, for a new policy, a new version of a published
// one, and an edit of a draft.
//
// Fields: title, version (create only), owner role, approver role, linked
// controls (the shared entity picker), acknowledgment roles, and the
// Markdown body with a preview through the shared Markdown component, so
// raw HTML in the body shows as text, never as markup. While no control is
// linked the form says, before submit, that publishing will need one; the
// platform refuses that publish whatever the form shows.

import { useState } from "react";

import { Markdown } from "@/components/markdown";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { EntityMultiPicker } from "@/components/ui/entity-picker";
import { Input } from "@/components/ui/input";
import type { EntityKind } from "@/lib/entity-picker";

import {
  AUDIENCE_OPTIONS,
  audienceLabel,
  NO_CONTROLS_WARNING,
  unrecognisedAudiences,
  validatePolicyForm,
  type PolicyFormErrors,
  type PolicyFormValues,
} from "./lifecycle";

const CONTROL_KINDS: readonly EntityKind[] = ["control"];

type Props = {
  mode: "create" | "edit";
  initial: PolicyFormValues;
  submitLabel: string;
  submittingLabel: string;
  errorTitle: string;
  onSubmit: (values: PolicyFormValues) => Promise<void>;
};

export function PolicyForm({
  mode,
  initial,
  submitLabel,
  submittingLabel,
  errorTitle,
  onSubmit,
}: Props) {
  const [v, setV] = useState<PolicyFormValues>(initial);
  const [errors, setErrors] = useState<PolicyFormErrors>({});
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [preview, setPreview] = useState(false);

  const set =
    <K extends keyof PolicyFormValues>(key: K) =>
    (value: PolicyFormValues[K]) =>
      setV((prev) => ({ ...prev, [key]: value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitErr(null);
    const errs = validatePolicyForm(v, mode);
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setSubmitting(true);
    try {
      await onSubmit(v);
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
      data-testid="policy-form"
    >
      <TextField
        id="policy-title"
        label="Title"
        value={v.title}
        onChange={set("title")}
        error={errors.title}
        maxLength={200}
      />
      {mode === "create" ? (
        <TextField
          id="policy-version"
          label="Version"
          value={v.version}
          onChange={set("version")}
          error={errors.version}
          maxLength={50}
        />
      ) : null}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <TextField
          id="policy-owner-role"
          label="Owner role"
          placeholder="e.g. security_lead"
          value={v.owner_role}
          onChange={set("owner_role")}
          error={errors.owner_role}
          maxLength={100}
        />
        <TextField
          id="policy-approver-role"
          label="Approver role"
          placeholder="e.g. cto"
          value={v.approver_role}
          onChange={set("approver_role")}
          error={errors.approver_role}
          maxLength={100}
        />
      </div>

      <EntityMultiPicker
        kinds={CONTROL_KINDS}
        legend="Linked controls"
        searchPlaceholder="Search controls…"
        testId="policy-controls"
        value={v.linked_control_ids}
        onChange={set("linked_control_ids")}
        emptyHint="No controls in this tenant yet — upload a control bundle first."
      />
      {v.linked_control_ids.length === 0 ? (
        <Alert data-testid="policy-form-no-controls-warning">
          <AlertTitle>No linked controls</AlertTitle>
          <AlertDescription>{NO_CONTROLS_WARNING}</AlertDescription>
        </Alert>
      ) : null}

      <fieldset
        className="space-y-2 rounded-md border border-input p-3"
        data-testid="policy-ack-roles"
      >
        <legend className="px-1 text-sm font-medium">
          Acknowledgment roles
        </legend>
        <p className="text-xs text-muted-foreground">
          People with these roles, or every member for Everyone, are asked to
          acknowledge the published policy.
        </p>
        <div className="flex flex-wrap gap-x-4 gap-y-2">
          {/* #2524: the roles plus Everyone, then any free-text value the
              policy carries from before, flagged. Unticking one drops it;
              the form never adds one. */}
          {[
            ...AUDIENCE_OPTIONS,
            ...unrecognisedAudiences(v.acknowledgment_required_roles),
          ].map((role) => (
            <label key={role} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={v.acknowledgment_required_roles.includes(role)}
                onChange={(e) =>
                  set("acknowledgment_required_roles")(
                    e.target.checked
                      ? [...v.acknowledgment_required_roles, role]
                      : v.acknowledgment_required_roles.filter(
                          (r) => r !== role,
                        ),
                  )
                }
                data-testid={`policy-ack-role-${role}`}
              />
              {audienceLabel(role)}
              {AUDIENCE_OPTIONS.includes(role) ? null : (
                <span
                  className="text-xs text-destructive"
                  data-testid={`policy-ack-role-unrecognised-${role}`}
                >
                  unrecognised audience
                </span>
              )}
            </label>
          ))}
        </div>
        {errors.acknowledgment_required_roles ? (
          <p
            className="text-xs text-destructive"
            data-testid="policy-ack-roles-error"
          >
            {errors.acknowledgment_required_roles}
          </p>
        ) : null}
      </fieldset>

      <div className="space-y-1">
        <div className="flex items-center justify-between">
          <label htmlFor="policy-body" className="text-sm font-medium">
            Policy text (Markdown)
          </label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setPreview((p) => !p)}
            aria-pressed={preview}
            data-testid="policy-body-preview-toggle"
          >
            {preview ? "Edit text" : "Preview"}
          </Button>
        </div>
        {preview ? (
          <div className="min-h-40 rounded-md border p-3">
            <Markdown
              source={v.body_md || "Nothing to preview yet."}
              data-testid="policy-body-preview"
            />
          </div>
        ) : (
          <textarea
            id="policy-body"
            value={v.body_md}
            onChange={(e) => set("body_md")(e.target.value)}
            rows={14}
            className="w-full rounded-md border bg-background px-2 py-1 font-mono text-sm"
            aria-invalid={errors.body_md ? true : undefined}
            aria-describedby={errors.body_md ? "policy-body-error" : undefined}
            data-testid="policy-body"
          />
        )}
        {errors.body_md ? (
          <p
            id="policy-body-error"
            className="text-xs text-rose-600"
            data-testid="policy-body-error"
          >
            {errors.body_md}
          </p>
        ) : null}
      </div>

      {submitErr ? (
        <Alert variant="destructive" data-testid="policy-form-submit-error">
          <AlertTitle>{errorTitle}</AlertTitle>
          <AlertDescription>{submitErr}</AlertDescription>
        </Alert>
      ) : null}

      <Button type="submit" disabled={submitting} data-testid="policy-submit">
        {submitting ? submittingLabel : submitLabel}
      </Button>
    </form>
  );
}

function TextField({
  id,
  label,
  value,
  onChange,
  error,
  placeholder,
  maxLength,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  error?: string;
  placeholder?: string;
  maxLength?: number;
}) {
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      <Input
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        maxLength={maxLength}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
        data-testid={id}
      />
      {error ? (
        <p
          id={`${id}-error`}
          className="text-xs text-rose-600"
          data-testid={`${id}-error`}
        >
          {error}
        </p>
      ) : null}
    </div>
  );
}
