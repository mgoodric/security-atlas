"use client";

// Slice 149 — audit-period-create form.
//
// Bound DIRECTLY to the slice-028 `createReq` wire shape
// (`internal/api/auditperiods/handlers.go`). The four required fields
// are: name, framework_version_id (UUID), period_start (RFC3339),
// period_end (RFC3339).
//
// FrameworkVersion picker (D-149-1): there is NO public list endpoint
// for FrameworkVersion as of slice 149. The form accepts a pasted UUID
// — the operator copies it from the audits-list page (the
// `framework-version` column renders the first 8 chars of the UUID with
// the full value in the title attribute) or from the catalog admin
// area. A future spillover slice files a dedicated framework-version
// picker endpoint + dropdown; this UI swaps in without page rework.
//
// Date inputs (D-149-2): native `<input type="date">` returns a
// YYYY-MM-DD value. The backend `createReq` expects `time.Time` which
// accepts RFC3339. We append `T00:00:00Z` before POSTing so the wire
// shape is unambiguous; the platform treats both periods as UTC.
//
// On 4xx the upstream error message renders in an Alert above the
// submit row and the form state is preserved (the user can fix the
// flagged field and resubmit without re-typing everything).

import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

import type { AuditPeriodCreateInput } from "./actions";

type FormState = {
  name: string;
  framework_version_id: string;
  period_start: string;
  period_end: string;
};

function initialState(): FormState {
  return {
    name: "",
    framework_version_id: "",
    period_start: "",
    period_end: "",
  };
}

function toCreateInput(s: FormState): AuditPeriodCreateInput {
  return {
    name: s.name.trim(),
    framework_version_id: s.framework_version_id.trim(),
    // YYYY-MM-DD from the native date picker → RFC3339 UTC midnight so
    // the backend `time.Time` decoder accepts it. handlers.go validates
    // period_start <= period_end after parsing.
    period_start: `${s.period_start}T00:00:00Z`,
    period_end: `${s.period_end}T00:00:00Z`,
  };
}

type Props = {
  onSubmit: (body: AuditPeriodCreateInput) => Promise<void>;
};

const LABEL_CLASS = "block text-sm font-medium text-foreground mb-1.5";
const HELP_CLASS = "mt-1 text-xs text-muted-foreground";

export function AuditPeriodForm({ onSubmit }: Props) {
  const [state, setState] = useState<FormState>(initialState());
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function update<K extends keyof FormState>(key: K, value: FormState[K]) {
    setState((s) => ({ ...s, [key]: value }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit(toCreateInput(state));
    } catch (err) {
      const msg = (err as Error).message ?? "failed to create audit period";
      setError(msg);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form
      onSubmit={handleSubmit}
      className="space-y-6"
      data-testid="audits-create-form"
    >
      {error && (
        <Alert variant="destructive" data-testid="audits-create-error">
          <AlertTitle>Could not create audit period</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div>
        <label className={LABEL_CLASS} htmlFor="audit-period-name">
          Name <span className="text-destructive">*</span>
        </label>
        <Input
          id="audit-period-name"
          name="name"
          required
          value={state.name}
          onChange={(e) => update("name", e.target.value)}
          placeholder="e.g. Q3 2026 SOC 2 Type II"
          data-testid="audits-create-name"
        />
        <p className={HELP_CLASS}>
          Short, scannable label for the period. Appears in the audits list
          header and on every artifact exported from this period.
        </p>
      </div>

      <div>
        <label className={LABEL_CLASS} htmlFor="audit-period-framework">
          Framework version <span className="text-destructive">*</span>
        </label>
        <Input
          id="audit-period-framework"
          name="framework_version_id"
          required
          value={state.framework_version_id}
          onChange={(e) => update("framework_version_id", e.target.value)}
          placeholder="00000000-0000-0000-0000-000000000000"
          className="font-mono"
          data-testid="audits-create-framework-version-id"
        />
        <p className={HELP_CLASS}>
          Paste the FrameworkVersion UUID. Copy it from the audits list (the
          framework-version column shows the first 8 chars; hover for the full
          UUID), or from the catalog admin. A dedicated picker is a future
          enhancement.
        </p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className={LABEL_CLASS} htmlFor="audit-period-start">
            Period start <span className="text-destructive">*</span>
          </label>
          <Input
            id="audit-period-start"
            name="period_start"
            type="date"
            required
            value={state.period_start}
            onChange={(e) => update("period_start", e.target.value)}
            data-testid="audits-create-period-start"
          />
        </div>

        <div>
          <label className={LABEL_CLASS} htmlFor="audit-period-end">
            Period end <span className="text-destructive">*</span>
          </label>
          <Input
            id="audit-period-end"
            name="period_end"
            type="date"
            required
            value={state.period_end}
            onChange={(e) => update("period_end", e.target.value)}
            data-testid="audits-create-period-end"
          />
          <p className={HELP_CLASS}>
            Must be on or after period_start. Treated as UTC; the platform uses
            these bounds to scope evidence sampling once the period is frozen.
          </p>
        </div>
      </div>

      <div className="flex items-center gap-3">
        <Button
          type="submit"
          disabled={submitting}
          data-testid="audits-create-submit"
        >
          {submitting ? "Creating…" : "Create audit period"}
        </Button>
        <span className="text-xs text-muted-foreground">
          On success you return to the audits list.
        </span>
      </div>
    </form>
  );
}
