// #2848 — the risk import upload's mapping step: the preview wire type
// (internal/risk/riskimport.Preview), the risk fields a column can map to
// as the shared mapping helper takes them, and the calls the upload page
// makes. Pure helpers are node-testable so the page stays a renderer.

import type { MappingColumn, MappingField } from "@/lib/import/column-mapping";
import { appendMapping, type UploadMapping } from "@/lib/import/column-mapping";
import { enumLabel } from "@/lib/labels/enum-labels";

import { upstreamErrorMessage } from "./_shared";
import { APIError } from "./base";
import type { RiskImportBatch, ValueMapping } from "./risk-imports";

export type RiskImportPreview = {
  methodology: string;
  header_row: number;
  header_detected: boolean;
  columns: MappingColumn[];
  fields: { name: string; required: boolean }[];
  rows: string[][];
  data_rows: number;
};

// The methodologies a batch can declare (riskimport.ResolveMethodology);
// the first is the platform default.
export const IMPORT_METHODOLOGIES = [
  "nist_800_30",
  "qualitative_5x5",
  "fair",
] as const;

export function methodologyLabel(m: string): string {
  return enumLabel("RiskMethodology", m);
}

// Display labels and order for the risk fields (riskimport.Field*). A
// field the platform adds before this list knows it shows by name, last.
export const RISK_FIELD_LABELS: Record<string, string> = {
  external_ref: "Risk ID",
  title: "Title",
  description: "Description",
  category: "Category",
  themes: "Themes",
  owner: "Owner",
  treatment: "Treatment",
  linked_controls: "Linked controls",
  instrument_reference: "Transfer instrument",
  justification: "Acceptance justification",
  accepted_until: "Accepted until",
  accepter: "Accepted by (historical)",
  review_due: "Review due",
  inherent_likelihood: "Inherent likelihood",
  inherent_impact: "Inherent impact",
  residual_likelihood: "Residual likelihood",
  residual_impact: "Residual impact",
  inherent_loss_event_frequency: "Inherent loss event frequency",
  inherent_loss_magnitude: "Inherent loss magnitude",
  residual_loss_event_frequency: "Residual loss event frequency",
  residual_loss_magnitude: "Residual loss magnitude",
  level: "Level",
  org_unit: "Org unit",
  parent_ref: "Parent risk ID",
};

/** riskMappingFields is the preview's field list as the mapping helper
 *  takes it: labelled, in RISK_FIELD_LABELS order. */
export function riskMappingFields(p: RiskImportPreview): MappingField[] {
  const order = Object.keys(RISK_FIELD_LABELS);
  const rank = (n: string) => {
    const i = order.indexOf(n);
    return i < 0 ? order.length : i;
  };
  return [...p.fields]
    .sort((a, b) => rank(a.name) - rank(b.name))
    .map((f) => ({
      name: f.name,
      label: RISK_FIELD_LABELS[f.name] ?? f.name,
      required: f.required,
    }));
}

/** uploadForm is the multipart body of a preview or an upload. */
export function uploadForm(
  file: File,
  methodology: string,
  mapping?: UploadMapping,
): FormData {
  const form = new FormData();
  form.append("file", file);
  form.append("methodology", methodology);
  if (mapping) appendMapping(form, mapping);
  return form;
}

async function postForm<T>(path: string, form: FormData): Promise<T> {
  const res = await fetch(path, { method: "POST", body: form });
  if (!res.ok) {
    throw new APIError(
      res.status,
      await upstreamErrorMessage(res, `upload failed (${res.status})`),
    );
  }
  return (await res.json()) as T;
}

export function previewUpload(form: FormData): Promise<RiskImportPreview> {
  return postForm("/api/risks/imports/preview", form);
}

export function stageUpload(form: FormData): Promise<RiskImportBatch> {
  return postForm("/api/risks/imports", form);
}

/** A value's mapping: to an existing category slug or theme name, or a
 *  new one created for it. */
export type ValueChoice =
  | { target: string }
  | { create: { slug?: string; name: string; description: string } };

export async function mapValue(
  batchID: string,
  v: Pick<ValueMapping, "kind" | "value">,
  choice: ValueChoice,
): Promise<ValueMapping> {
  const res = await fetch(
    `/api/risks/imports/${encodeURIComponent(batchID)}/values`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ kind: v.kind, value: v.value, ...choice }),
    },
  );
  if (!res.ok) {
    throw new APIError(
      res.status,
      await upstreamErrorMessage(res, `mapping failed (${res.status})`),
    );
  }
  return (await res.json()) as ValueMapping;
}

/** slugFor suggests a category slug or theme name for a value: lower case,
 *  runs of anything else turned into "-", within the platform's
 *  ^[a-z0-9][a-z0-9_-]{0,62}$. */
export function slugFor(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9_]+/g, "-")
    .replace(/^[-_]+|-+$/g, "")
    .slice(0, 63)
    .replace(/-+$/, "");
}

/** createChoice is the create body for a value: a category takes a slug
 *  and a name, a theme only a name, which is slug-shaped. */
export function createChoice(
  kind: ValueMapping["kind"],
  value: string,
): ValueChoice {
  const slug = slugFor(value);
  return kind === "category"
    ? { create: { slug, name: value.trim(), description: "" } }
    : { create: { name: slug, description: "" } };
}

/** valuesToMap is every category and theme value of a staged batch,
 *  categories first; needsValueStep is true while any maps to nothing. */
export function valuesToMap(batch: RiskImportBatch): ValueMapping[] {
  return [...(batch.category_values ?? []), ...(batch.theme_values ?? [])];
}

export function needsValueStep(batch: RiskImportBatch): boolean {
  return valuesToMap(batch).some((v) => !v.target);
}
