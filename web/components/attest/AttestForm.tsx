"use client";

// Slice 011 — JSON-Schema-driven attestation form.
//
// The component receives the control's manual_evidence_schema (a flat
// JSON Schema; nested objects fall back to a JSON textarea). It renders
// one input per top-level property, plus the platform-required
// `statement` field and an optional file upload, then POSTs to the
// platform via the Next.js proxy route.
//
// v1 deliberately keeps the renderer small: no @rjsf, no JSON Schema
// AST. Five primitives cover the 50-control kit (slice 010) — string,
// textarea (long string), number, boolean, enum. Anything else degrades
// to a plain JSON textarea so authors can ship complex shapes without
// blocking on a richer renderer.

//
// #2089: on success the form returns to the control with an
// "Attestation recorded" toast, so it is never left filled. Required
// fields are checked here and reported inline beside each field (the
// form is noValidate, so the browser shows no native bubble).
//
// #2111: the form asks for the result, pass or fail, and a fail needs a
// reason. On success it returns to the control with `?attested=<the
// record's ingested_at>`, and the control page shows "Evaluating…" until
// the evaluation that attestation triggered has landed.

import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import {
  AttestForm as AttestFormShape,
  AttestResult,
  AttestSubmitRequest,
  AttestSubmitResponse,
} from "@/lib/api/attest";
import { ATTESTED_PARAM } from "@/lib/attest-evaluation";

type FormProperty = {
  name: string;
  type?: string;
  title?: string;
  description?: string;
  format?: string;
  enum?: unknown[];
  required: boolean;
  minLength?: number;
  maxLength?: number;
};

function extractProperties(
  schema: Record<string, unknown> | null,
): FormProperty[] {
  if (!schema || typeof schema !== "object") return [];
  const props = (schema as { properties?: Record<string, unknown> }).properties;
  if (!props || typeof props !== "object") return [];
  const reqList = ((schema as { required?: unknown[] }).required ??
    []) as unknown[];
  const required = new Set(
    reqList.filter((v): v is string => typeof v === "string"),
  );
  return Object.keys(props)
    .sort()
    .map((name) => {
      const p = props[name] as Record<string, unknown>;
      return {
        name,
        type: typeof p.type === "string" ? (p.type as string) : undefined,
        title: typeof p.title === "string" ? (p.title as string) : undefined,
        description:
          typeof p.description === "string"
            ? (p.description as string)
            : undefined,
        format: typeof p.format === "string" ? (p.format as string) : undefined,
        enum: Array.isArray(p.enum) ? (p.enum as unknown[]) : undefined,
        required: required.has(name),
        minLength:
          typeof p.minLength === "number" ? (p.minLength as number) : undefined,
        maxLength:
          typeof p.maxLength === "number" ? (p.maxLength as number) : undefined,
      } satisfies FormProperty;
    });
}

// Key for the platform-required statement in the inline-error map. Not a
// valid JSON-Schema property name, so it cannot collide with one.
const STATEMENT_FIELD = "$statement";
// Key for the fail reason, on the same terms.
const FAIL_REASON_FIELD = "$fail_reason";

function isLongString(prop: FormProperty): boolean {
  // Heuristic: descriptions or anything with maxLength > 200 → textarea.
  if (prop.maxLength && prop.maxLength > 200) return true;
  if (
    prop.name === "notes" ||
    prop.name === "description" ||
    prop.name === "rationale"
  )
    return true;
  return false;
}

function coerceValue(prop: FormProperty, raw: string): unknown {
  if (raw === "") return undefined;
  switch (prop.type) {
    case "integer":
      return Number.parseInt(raw, 10);
    case "number":
      return Number(raw);
    case "boolean":
      return raw === "true";
    default:
      return raw;
  }
}

// A fresh idempotency key per submission. getRandomValues works outside a
// secure context, unlike randomUUID.
function newAttestIdempotencyKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return (
    "attest-web-" +
    Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("")
  );
}

export function AttestForm({
  form,
  uploadEndpoint = "/api/artifacts/upload",
  submitEndpoint,
}: {
  form: AttestFormShape;
  uploadEndpoint?: string;
  submitEndpoint?: string;
}) {
  const properties = useMemo(
    () => extractProperties(form.manual_evidence_schema),
    [form.manual_evidence_schema],
  );
  const [statement, setStatement] = useState("");
  const [result, setResult] = useState<AttestResult>("pass");
  const [failReason, setFailReason] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const router = useRouter();
  const { toast } = useToast();

  const setField = (name: string, raw: string) => {
    setValues((v) => ({ ...v, [name]: raw }));
    setFieldErrors((errs) => ({ ...errs, [name]: "" }));
  };

  function validate(): Record<string, string> {
    const errs: Record<string, string> = {};
    if (statement.trim() === "")
      errs[STATEMENT_FIELD] = "Statement is required.";
    if (result === "fail" && failReason.trim() === "")
      errs[FAIL_REASON_FIELD] = "A reason is required for a fail.";
    for (const prop of properties) {
      if (prop.required && prop.type !== "boolean" && !values[prop.name]) {
        errs[prop.name] = `${prop.title ?? prop.name} is required.`;
      }
    }
    return errs;
  }

  // Wires an input to its inline error for assistive tech.
  const errorProps = (name: string) =>
    fieldErrors[name]
      ? {
          "aria-invalid": true as const,
          "aria-describedby": `attest-error-${name}`,
        }
      : {};
  const fieldError = (name: string) =>
    fieldErrors[name] ? (
      <p
        id={`attest-error-${name}`}
        className="mt-1 text-sm text-destructive"
        data-testid={`attest-error-${name}`}
      >
        {fieldErrors[name]}
      </p>
    ) : null;

  const targetSubmit =
    submitEndpoint ??
    `/api/controls/${encodeURIComponent(form.control_id)}/attestations`;

  async function uploadIfNeeded(): Promise<string | undefined> {
    if (!file) return undefined;
    const fd = new FormData();
    fd.append("file", file);
    const res = await fetch(uploadEndpoint, { method: "POST", body: fd });
    if (!res.ok) {
      throw new Error(
        `artifact upload failed: ${res.status} ${await res.text()}`,
      );
    }
    const body = (await res.json()) as {
      artifact?: { id?: string };
    };
    if (!body.artifact?.id) {
      throw new Error("artifact upload did not return an id");
    }
    return body.artifact.id;
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!form.caller_can_attest) {
      setError(
        `Your account does not hold owner_role "${form.owner_role}" for this control.`,
      );
      return;
    }
    const errs = validate();
    setFieldErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setBusy(true);
    try {
      const attestationData: Record<string, unknown> = {};
      for (const prop of properties) {
        const v = coerceValue(prop, values[prop.name] ?? "");
        if (v !== undefined) attestationData[prop.name] = v;
      }
      const artifactID = await uploadIfNeeded();
      const body: AttestSubmitRequest = {
        statement,
        attestation_data: attestationData,
        result,
        // #2509: each submission is a new attestation, even with the same
        // text as an earlier one (attesting pass again after a fail). The
        // server's default key would dedupe an identical body sent inside
        // its retry window.
        idempotency_key: newAttestIdempotencyKey(),
      };
      if (result === "fail") body.fail_reason = failReason.trim();
      if (artifactID) body.artifact_id = artifactID;
      const res = await fetch(targetSubmit, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        const txt = await res.text();
        throw new Error(`${res.status}: ${txt}`);
      }
      const recorded = (await res.json()) as AttestSubmitResponse;
      toast("Attestation recorded");
      const back = `/controls/${encodeURIComponent(form.control_id)}`;
      router.push(
        recorded.ingested_at
          ? `${back}?${ATTESTED_PARAM}=${encodeURIComponent(
              recorded.ingested_at,
            )}`
          : back,
      );
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  }

  return (
    <form
      onSubmit={onSubmit}
      noValidate
      className="space-y-4"
      data-testid="attest-form"
    >
      {!form.caller_can_attest ? (
        <div
          role="alert"
          className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-amber-900"
          data-testid="attest-role-banner"
        >
          Your account does not hold owner_role{" "}
          <code className="font-mono">{form.owner_role}</code>; the submit
          button will be rejected.
        </div>
      ) : null}

      <fieldset data-testid="attest-result">
        <legend className="block text-sm font-medium">
          Result <span className="text-red-600">*</span>
        </legend>
        <div className="mt-1 flex gap-4 text-sm">
          {(["pass", "fail"] as const).map((r) => (
            <label key={r} className="flex items-center gap-1.5">
              <input
                type="radio"
                name="attest-result"
                value={r}
                checked={result === r}
                onChange={() => {
                  setResult(r);
                  setFieldErrors((errs) => ({
                    ...errs,
                    [FAIL_REASON_FIELD]: "",
                  }));
                }}
                data-testid={`attest-result-${r}`}
              />
              {r === "pass" ? "Pass" : "Fail"}
            </label>
          ))}
        </div>
        <p className="mt-1 text-xs text-muted-foreground">
          The latest attestation decides the control&apos;s attested outcome.
        </p>
      </fieldset>

      {result === "fail" ? (
        <div>
          <label className="block text-sm font-medium" htmlFor="fail-reason">
            Reason for the fail <span className="text-red-600">*</span>
          </label>
          <textarea
            id="fail-reason"
            rows={2}
            value={failReason}
            onChange={(e) => {
              setFailReason(e.target.value);
              setFieldErrors((errs) => ({ ...errs, [FAIL_REASON_FIELD]: "" }));
            }}
            className="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 font-sans text-sm"
            data-testid="attest-fail-reason"
            {...errorProps(FAIL_REASON_FIELD)}
          />
          {fieldError(FAIL_REASON_FIELD)}
        </div>
      ) : null}

      <div>
        <label className="block text-sm font-medium" htmlFor="statement">
          Statement <span className="text-red-600">*</span>
        </label>
        <textarea
          id="statement"
          required
          minLength={1}
          rows={3}
          value={statement}
          onChange={(e) => {
            setStatement(e.target.value);
            setFieldErrors((errs) => ({ ...errs, [STATEMENT_FIELD]: "" }));
          }}
          className="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 font-sans text-sm"
          data-testid="attest-statement"
          {...errorProps(STATEMENT_FIELD)}
        />
        {fieldError(STATEMENT_FIELD)}
        <p className="mt-1 text-xs text-muted-foreground">
          Required by the platform schema. Describe what you are attesting.
        </p>
      </div>

      {properties.map((prop) => {
        const label = prop.title ?? prop.name;
        const v = values[prop.name] ?? "";
        if (prop.enum && prop.enum.length > 0) {
          return (
            <div key={prop.name}>
              <label
                className="block text-sm font-medium"
                htmlFor={`f-${prop.name}`}
              >
                {label}{" "}
                {prop.required ? <span className="text-red-600">*</span> : null}
              </label>
              <select
                id={`f-${prop.name}`}
                required={prop.required}
                value={v}
                onChange={(e) => setField(prop.name, e.target.value)}
                className="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
                data-testid={`attest-prop-${prop.name}`}
                {...errorProps(prop.name)}
              >
                <option value="">--</option>
                {prop.enum.map((opt, i) => (
                  <option key={i} value={String(opt)}>
                    {String(opt)}
                  </option>
                ))}
              </select>
              {fieldError(prop.name)}
              {prop.description ? (
                <p className="mt-1 text-xs text-muted-foreground">
                  {prop.description}
                </p>
              ) : null}
            </div>
          );
        }
        if (prop.type === "boolean") {
          return (
            <div key={prop.name} className="flex items-center gap-2">
              <input
                id={`f-${prop.name}`}
                type="checkbox"
                checked={v === "true"}
                onChange={(e) =>
                  setField(prop.name, e.target.checked ? "true" : "false")
                }
                data-testid={`attest-prop-${prop.name}`}
              />
              <label htmlFor={`f-${prop.name}`} className="text-sm">
                {label}
              </label>
            </div>
          );
        }
        if (isLongString(prop)) {
          return (
            <div key={prop.name}>
              <label
                className="block text-sm font-medium"
                htmlFor={`f-${prop.name}`}
              >
                {label}{" "}
                {prop.required ? <span className="text-red-600">*</span> : null}
              </label>
              <textarea
                id={`f-${prop.name}`}
                required={prop.required}
                rows={3}
                value={v}
                onChange={(e) => setField(prop.name, e.target.value)}
                className="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
                data-testid={`attest-prop-${prop.name}`}
                {...errorProps(prop.name)}
              />
              {fieldError(prop.name)}
              {prop.description ? (
                <p className="mt-1 text-xs text-muted-foreground">
                  {prop.description}
                </p>
              ) : null}
            </div>
          );
        }
        return (
          <div key={prop.name}>
            <label
              className="block text-sm font-medium"
              htmlFor={`f-${prop.name}`}
            >
              {label}{" "}
              {prop.required ? <span className="text-red-600">*</span> : null}
            </label>
            <Input
              id={`f-${prop.name}`}
              required={prop.required}
              type={
                prop.type === "integer" || prop.type === "number"
                  ? "number"
                  : prop.format === "uri"
                    ? "url"
                    : "text"
              }
              value={v}
              minLength={prop.minLength}
              maxLength={prop.maxLength}
              onChange={(e) => setField(prop.name, e.target.value)}
              data-testid={`attest-prop-${prop.name}`}
              {...errorProps(prop.name)}
            />
            {fieldError(prop.name)}
            {prop.description ? (
              <p className="mt-1 text-xs text-muted-foreground">
                {prop.description}
              </p>
            ) : null}
          </div>
        );
      })}

      <div>
        <label className="block text-sm font-medium" htmlFor="artifact">
          Optional supporting artifact (PDF, screenshot, signed file)
        </label>
        <input
          id="artifact"
          type="file"
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          className="mt-1 text-sm"
          data-testid="attest-artifact"
        />
      </div>

      {error ? (
        <div
          role="alert"
          className="rounded-md border border-red-300 bg-red-50 px-3 py-2 text-red-900"
          data-testid="attest-error"
        >
          {error}
        </div>
      ) : null}

      <Button
        type="submit"
        disabled={busy || !form.caller_can_attest}
        data-testid="attest-submit"
      >
        {busy ? "Submitting..." : "Submit attestation"}
      </Button>
    </form>
  );
}
