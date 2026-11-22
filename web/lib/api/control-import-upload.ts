// #2836 — the control import upload's mapping step: the preview wire type
// (internal/controlimport.Preview), the control fields a column can map to
// as the shared mapping helper takes them, and the calls the upload page
// makes. The mapping rules themselves are the shared helper in
// lib/import/column-mapping.ts, which the risk import uses too (#2848).

import type { MappingColumn, MappingField } from "@/lib/import/column-mapping";
import { appendMapping, type UploadMapping } from "@/lib/import/column-mapping";

import { upstreamErrorMessage } from "./_shared";
import { APIError } from "./base";
import type { ImportBatch } from "./control-imports";

export type ControlImportPreview = {
  header_row: number;
  header_detected: boolean;
  columns: MappingColumn[];
  fields: { name: string; required: boolean }[];
  rows: string[][];
  data_rows: number;
};

// Display labels and order for the control fields (controlimport.Field*).
// A field the platform adds before this list knows it shows by name, last.
export const CONTROL_FIELD_LABELS: Record<string, string> = {
  id: "Control ID",
  title: "Title",
  description: "Description",
  tested_under: "Tested under",
  implementation_type: "Implementation type",
  applicability: "Applicability",
};

/** controlMappingFields is the preview's field list as the mapping helper
 *  takes it: labelled, in CONTROL_FIELD_LABELS order. */
export function controlMappingFields(p: ControlImportPreview): MappingField[] {
  const order = Object.keys(CONTROL_FIELD_LABELS);
  const rank = (n: string) => {
    const i = order.indexOf(n);
    return i < 0 ? order.length : i;
  };
  return [...p.fields]
    .sort((a, b) => rank(a.name) - rank(b.name))
    .map((f) => ({
      name: f.name,
      label: CONTROL_FIELD_LABELS[f.name] ?? f.name,
      required: f.required,
    }));
}

/** uploadForm is the multipart body of a preview or an upload. The
 *  preview reads no framework; the upload needs one. */
export function uploadForm(
  file: File,
  framework?: string,
  mapping?: UploadMapping,
): FormData {
  const form = new FormData();
  form.append("file", file);
  if (framework) form.append("framework", framework);
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

export function previewUpload(form: FormData): Promise<ControlImportPreview> {
  return postForm("/api/controls/imports/preview", form);
}

export function stageUpload(form: FormData): Promise<ImportBatch> {
  return postForm("/api/controls/imports", form);
}
