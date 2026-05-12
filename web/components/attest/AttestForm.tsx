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

import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  AttestForm as AttestFormShape,
  AttestSubmitRequest,
  AttestSubmitResponse,
} from "@/lib/api";

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
  const [values, setValues] = useState<Record<string, string>>({});
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<AttestSubmitResponse | null>(null);

  const setField = (name: string, raw: string) =>
    setValues((v) => ({ ...v, [name]: raw }));

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
    setSuccess(null);
    if (!form.caller_can_attest) {
      setError(
        `Your account does not hold owner_role "${form.owner_role}" for this control.`,
      );
      return;
    }
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
      };
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
      const json = (await res.json()) as AttestSubmitResponse;
      setSuccess(json);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={onSubmit} className="space-y-4">
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
          onChange={(e) => setStatement(e.target.value)}
          className="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 font-sans text-sm"
          data-testid="attest-statement"
        />
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
              >
                <option value="">--</option>
                {prop.enum.map((opt, i) => (
                  <option key={i} value={String(opt)}>
                    {String(opt)}
                  </option>
                ))}
              </select>
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
              />
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
            />
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
      {success ? (
        <div
          role="status"
          className="rounded-md border border-green-300 bg-green-50 px-3 py-2 text-green-900"
          data-testid="attest-success"
        >
          Attestation recorded. record_id={" "}
          <code className="font-mono">{success.record_id}</code>
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
