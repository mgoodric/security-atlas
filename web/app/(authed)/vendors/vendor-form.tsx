"use client";

import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Vendor, VendorWrite } from "@/lib/api/vendors";

// Slice-024 vendor create/edit form. Intentionally simple — the AC says
// "simple form", not "full TPRM workflow". Phase 2 adds richer affordances
// (cell picker, questionnaire issuance trigger, etc.).

type Props = {
  initial?: Vendor;
  onSubmit: (body: VendorWrite) => Promise<void>;
  submitLabel: string;
};

const CRITICALITIES = ["low", "medium", "high"] as const;
const CADENCES = ["monthly", "quarterly", "biannual", "annual"] as const;

function fromVendor(v: Vendor | undefined): VendorWrite {
  return {
    name: v?.name ?? "",
    domain: v?.domain ?? "",
    criticality: v?.criticality ?? "medium",
    contract_start: v?.contract_start ?? "",
    contract_end: v?.contract_end ?? "",
    dpa_signed: v?.dpa_signed ?? false,
    dpa_signed_at: v?.dpa_signed_at ?? "",
    review_cadence: v?.review_cadence ?? "annual",
    last_review_date: v?.last_review_date ?? "",
    owner_user: v?.owner_user ?? "",
    linked_sow_uri: v?.linked_sow_uri ?? "",
    notes: v?.notes ?? "",
    scope_cell_ids: v?.scope_cell_ids ?? [],
  };
}

function normalizeForSubmit(b: VendorWrite): VendorWrite {
  // Empty strings -> null so the backend sees "absent" instead of "blank".
  // dpa_signed without dpa_signed_at is filtered to null/false at the
  // server, but we clear the date here for a tighter wire payload.
  const clean = (s: string | null | undefined): string | null => {
    if (!s) return null;
    const t = s.trim();
    return t === "" ? null : t;
  };
  return {
    ...b,
    name: b.name.trim(),
    domain: clean(b.domain ?? null),
    contract_start: clean(b.contract_start ?? null),
    contract_end: clean(b.contract_end ?? null),
    dpa_signed_at: b.dpa_signed ? clean(b.dpa_signed_at ?? null) : null,
    last_review_date: clean(b.last_review_date ?? null),
    linked_sow_uri: clean(b.linked_sow_uri ?? null),
    owner_user: b.owner_user.trim(),
    notes: b.notes.trim(),
  };
}

export function VendorForm({ initial, onSubmit, submitLabel }: Props) {
  const [body, setBody] = useState<VendorWrite>(fromVendor(initial));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function update<K extends keyof VendorWrite>(key: K, value: VendorWrite[K]) {
    setBody((b) => ({ ...b, [key]: value }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit(normalizeForSubmit(body));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      {error ? (
        <Alert variant="destructive">
          <AlertTitle>Could not save vendor</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Identity</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-2">
          <Field label="Name" required>
            <Input
              value={body.name}
              onChange={(e) => update("name", e.target.value)}
              required
            />
          </Field>
          <Field label="Domain">
            <Input
              value={body.domain ?? ""}
              onChange={(e) => update("domain", e.target.value)}
              placeholder="datadoghq.com"
            />
          </Field>
          <Field label="Owner (email)">
            <Input
              value={body.owner_user}
              onChange={(e) => update("owner_user", e.target.value)}
              placeholder="alice@example.com"
            />
          </Field>
          <Field label="Criticality" required>
            <Select
              value={body.criticality}
              options={CRITICALITIES}
              onChange={(v) =>
                update("criticality", v as VendorWrite["criticality"])
              }
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Contract</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-2">
          <Field label="Contract start">
            <Input
              type="date"
              value={body.contract_start ?? ""}
              onChange={(e) => update("contract_start", e.target.value)}
            />
          </Field>
          <Field label="Contract end">
            <Input
              type="date"
              value={body.contract_end ?? ""}
              onChange={(e) => update("contract_end", e.target.value)}
            />
          </Field>
          <Field label="Linked SOW URI">
            <Input
              value={body.linked_sow_uri ?? ""}
              onChange={(e) => update("linked_sow_uri", e.target.value)}
              placeholder="s3://contracts/vendor-2025.pdf"
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>DPA &amp; review</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-2">
          <Field label="DPA signed">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={body.dpa_signed}
                onChange={(e) => update("dpa_signed", e.target.checked)}
              />
              Signed
            </label>
          </Field>
          <Field label="DPA signed on">
            <Input
              type="date"
              value={body.dpa_signed_at ?? ""}
              onChange={(e) => update("dpa_signed_at", e.target.value)}
              disabled={!body.dpa_signed}
            />
          </Field>
          <Field label="Review cadence" required>
            <Select
              value={body.review_cadence}
              options={CADENCES}
              onChange={(v) =>
                update("review_cadence", v as VendorWrite["review_cadence"])
              }
            />
          </Field>
          <Field label="Last review">
            <Input
              type="date"
              value={body.last_review_date ?? ""}
              onChange={(e) => update("last_review_date", e.target.value)}
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Notes</CardTitle>
        </CardHeader>
        <CardContent>
          <textarea
            className="min-h-24 w-full rounded-md border bg-background p-2 text-sm"
            value={body.notes}
            onChange={(e) => update("notes", e.target.value)}
            placeholder="Anything the audit team will care about next quarter."
          />
        </CardContent>
      </Card>

      <div className="flex justify-end">
        <Button type="submit" disabled={submitting}>
          {submitting ? "Saving..." : submitLabel}
        </Button>
      </div>
    </form>
  );
}

function Field({
  label,
  children,
  required,
}: {
  label: string;
  children: React.ReactNode;
  required?: boolean;
}) {
  return (
    <label className="space-y-1 text-sm">
      <span className="font-medium">
        {label}
        {required ? <span className="text-destructive"> *</span> : null}
      </span>
      <div>{children}</div>
    </label>
  );
}

function Select<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: readonly T[];
  onChange: (v: T) => void;
}) {
  return (
    <select
      className="h-9 w-full rounded-md border bg-background px-2 text-sm"
      value={value}
      onChange={(e) => onChange(e.target.value as T)}
    >
      {options.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
    </select>
  );
}
