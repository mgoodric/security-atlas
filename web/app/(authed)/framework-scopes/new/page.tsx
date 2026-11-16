"use client";

// #2115 — /framework-scopes/new: the framework-scope wizard (parent #2071).
//
//   1. Pick the framework version (the entity picker). Opened from a
//      version's scope page with `?framework_version_id=<id>`, which
//      pre-fills it.
//   2. Scope it: Everything, or values chosen per dimension (the scope
//      picker). Advanced swaps the picker for the JSON predicate editor;
//      switching back re-reads the JSON into the picker.
//   3. Name it and Submit for approval: the draft is created, then
//      submitted, through the existing create and submit routes. The page
//      lands on the version's scope page, where an approver approves and
//      activates it.
//
// A null predicate (choose by dimension with nothing picked, or `null`
// typed under Advanced) is an incomplete form: `validateScopeDraft` builds
// no body for it and the submit stays refused, because the platform reads
// a stored null predicate as every cell. The JSON is parsed, never
// evaluated; the platform validates its grammar.

import { useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";

import { ScopePicker } from "@/components/scope/scope-picker";
import { EVERYTHING } from "@/components/scope/scope-picker-model";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { EntityPicker } from "@/components/ui/entity-picker";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import type { EntityKind } from "@/lib/entity-picker";
import type { FrameworkScope } from "@/lib/api/framework-scopes";

import {
  advancedText,
  errorMessage,
  parseAdvanced,
  validateScopeDraft,
  type ScopeDraftErrors,
} from "../wizard";

const FRAMEWORK_KINDS: readonly EntityKind[] = ["framework"];

export default function NewFrameworkScopePage() {
  return (
    <PageShell
      width="reading"
      title="New framework scope"
      subtitle="Which systems a framework applies to. A new scope is a draft until an approver approves it and activates it."
    >
      <Suspense fallback={null}>
        <ScopeWizard />
      </Suspense>
    </PageShell>
  );
}

function ScopeWizard() {
  const router = useRouter();
  const params = useSearchParams();
  const qc = useQueryClient();
  const { toast } = useToast();

  const [frameworkVersionId, setFrameworkVersionId] = useState(
    () => params.get("framework_version_id") ?? "",
  );
  const [predicate, setPredicate] = useState<unknown>(EVERYTHING);
  const [advanced, setAdvanced] = useState(false);
  const [jsonText, setJsonText] = useState("");
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [errors, setErrors] = useState<ScopeDraftErrors>({});
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  function openAdvanced() {
    setJsonText(advancedText(predicate));
    setJsonError(null);
    setAdvanced(true);
  }

  function closeAdvanced() {
    const r = parseAdvanced(jsonText);
    if (!r.ok) {
      setJsonError(r.error);
      return;
    }
    setPredicate(r.predicate);
    setAdvanced(false);
  }

  // Under Advanced the predicate is the editor's text, parsed; anything
  // that does not parse to an object is incomplete.
  const current: unknown = advanced
    ? (() => {
        const r = parseAdvanced(jsonText);
        return r.ok ? r.predicate : null;
      })()
    : predicate;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitErr(null);
    const r = validateScopeDraft({
      frameworkVersionId,
      name,
      predicate: current,
    });
    if (!r.ok) {
      setErrors(r.errors);
      if (advanced && r.errors.scope) {
        const p = parseAdvanced(jsonText);
        if (!p.ok) setJsonError(p.error);
      }
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      const created = await fetch("/api/framework-scopes", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(r.body),
      });
      if (!created.ok) throw new Error(await errorMessage(created));
      const { framework_scope } = (await created.json()) as {
        framework_scope: FrameworkScope;
      };
      const dest = `/framework-scopes/${encodeURIComponent(
        r.body.framework_version_id,
      )}`;
      const submitted = await fetch(
        `/api/framework-scopes/${encodeURIComponent(
          framework_scope.id,
        )}/submit`,
        { method: "PATCH" },
      );
      await qc.invalidateQueries({ queryKey: ["framework-scopes"] });
      if (!submitted.ok) {
        // The draft exists; say so and land on it rather than lose it.
        toast(
          `Saved as a draft; it could not be submitted: ${await errorMessage(
            submitted,
          )}`,
        );
      } else {
        toast("Scope submitted for approval");
      }
      router.push(dest);
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
      data-testid="framework-scope-wizard"
    >
      <div className="space-y-1">
        <label htmlFor="fs-framework" className="text-sm font-medium">
          Framework version
        </label>
        <EntityPicker
          kinds={FRAMEWORK_KINDS}
          id="fs-framework"
          value={frameworkVersionId}
          onChange={setFrameworkVersionId}
          placeholder="Search frameworks, or paste a version ID"
          testId="framework-scope-wizard-version"
          error={errors.frameworkVersion}
        />
      </div>

      <div className="space-y-2" data-testid="framework-scope-wizard-scope">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-medium">Scope</span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-pressed={advanced}
            onClick={advanced ? closeAdvanced : openAdvanced}
            data-testid="framework-scope-wizard-advanced"
          >
            {advanced ? "Back to the picker" : "Advanced"}
          </Button>
        </div>
        {advanced ? (
          <div className="space-y-1">
            <label htmlFor="fs-predicate" className="text-sm">
              Predicate (JSON)
            </label>
            <textarea
              id="fs-predicate"
              value={jsonText}
              onChange={(e) => {
                setJsonText(e.target.value);
                setJsonError(null);
              }}
              rows={10}
              spellCheck={false}
              className="w-full rounded-md border bg-background p-2 font-mono text-xs"
              data-testid="framework-scope-wizard-json"
              aria-invalid={jsonError ? true : undefined}
              aria-describedby={jsonError ? "fs-predicate-error" : undefined}
            />
            {jsonError ? (
              <p
                id="fs-predicate-error"
                className="text-xs text-rose-600"
                role="alert"
                data-testid="framework-scope-wizard-json-error"
              >
                {jsonError}
              </p>
            ) : null}
          </div>
        ) : (
          <ScopePicker value={predicate} onChange={setPredicate} />
        )}
        {errors.scope && !advanced ? (
          <p className="text-xs text-rose-600" role="alert">
            {errors.scope}
          </p>
        ) : null}
      </div>

      <div className="space-y-1">
        <label htmlFor="fs-name" className="text-sm font-medium">
          Name
        </label>
        <Input
          id="fs-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={200}
          data-testid="framework-scope-wizard-name"
          aria-invalid={errors.name ? true : undefined}
          aria-describedby={errors.name ? "fs-name-error" : undefined}
        />
        {errors.name ? (
          <p id="fs-name-error" className="text-xs text-rose-600">
            {errors.name}
          </p>
        ) : null}
      </div>

      {submitErr ? (
        <Alert variant="destructive" data-testid="framework-scope-wizard-error">
          <AlertTitle>Could not create the scope</AlertTitle>
          <AlertDescription>{submitErr}</AlertDescription>
        </Alert>
      ) : null}

      <Button
        type="submit"
        disabled={submitting || current === null}
        data-testid="framework-scope-wizard-submit"
      >
        {submitting ? "Submitting…" : "Submit for approval"}
      </Button>
    </form>
  );
}
