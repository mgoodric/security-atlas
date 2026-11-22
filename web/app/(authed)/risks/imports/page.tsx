"use client";

// #2848 — upload a risk register (CSV or XLSX) and map it before staging
// (spec #2812).
//
// 1. File: the register and the batch's one scoring methodology. The
//    platform reads the header row (POST /v1/risks/imports/preview) and
//    stages nothing.
// 2. Columns: each detected header with the risk field it maps to,
//    pre-filled from the platform's aliases and changeable, the unmapped
//    columns and a preview of the first rows (the shared ColumnMapping
//    step). Staging sends the mapping as the upload's column map.
// 3. Values: the staged batch's distinct category and theme values, each
//    mapped to an existing category or theme or created for it through the
//    audited creation (PUT /v1/risks/imports/{id}/values). Shown only while
//    a value maps to nothing.
// Then the batch review page, where an approver approves.
//
// Caps, header detection, alias matching and every refusal are the
// platform's; its error messages are shown inline.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useId, useState } from "react";

import { ColumnMapping } from "@/components/import/column-mapping";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { useToast } from "@/components/ui/toast";
import { getMe } from "@/lib/api/me";
import {
  IMPORT_METHODOLOGIES,
  type RiskImportPreview,
  type ValueChoice,
  createChoice,
  mapValue,
  methodologyLabel,
  needsValueStep,
  previewUpload,
  riskMappingFields,
  stageUpload,
  uploadForm,
  valuesToMap,
} from "@/lib/api/risk-import-upload";
import type { RiskImportBatch, ValueMapping } from "@/lib/api/risk-imports";
import { fetchRiskCategories } from "@/lib/api/risks";
import type { RiskTheme } from "@/lib/api/risk-hierarchy";
import {
  type Assignments,
  initialAssignments,
  missingRequired,
  uploadMapping,
} from "@/lib/import/column-mapping";

const CREATE = "__create__";

async function fetchThemes(): Promise<RiskTheme[]> {
  const res = await fetch("/api/risks-hierarchy/themes", { cache: "no-store" });
  if (!res.ok) throw new Error(`themes: ${res.status}`);
  return ((await res.json()) as { themes?: RiskTheme[] }).themes ?? [];
}

function reviewPath(id: string): string {
  return `/risks/imports/${encodeURIComponent(id)}`;
}

// ValueStep maps the staged batch's category and theme values.
function ValueStep({
  batch,
  onBatch,
  onContinue,
}: {
  batch: RiskImportBatch;
  onBatch: (b: RiskImportBatch) => void;
  onContinue: () => void;
}) {
  const meQ = useQuery({ queryKey: ["risk-import", "me"], queryFn: getMe });
  const catsQ = useQuery({
    queryKey: ["risk-categories", "active"],
    queryFn: () => fetchRiskCategories(false),
  });
  const themesQ = useQuery({ queryKey: ["risk-themes"], queryFn: fetchThemes });
  const canCreate = meQ.data?.is_approver === true;
  const values = valuesToMap(batch);
  const unmapped = values.filter((v) => !v.target).length;

  const mapM = useMutation({
    mutationFn: (a: { v: ValueMapping; choice: ValueChoice }) =>
      mapValue(batch.id, a.v, a.choice),
    onSuccess: (m) => {
      const key = m.kind === "category" ? "category_values" : "theme_values";
      onBatch({
        ...batch,
        [key]: (batch[key] ?? []).map((v) =>
          v.value === m.value ? { ...v, ...m } : v,
        ),
      });
      void catsQ.refetch();
      void themesQ.refetch();
    },
  });

  return (
    <div className="space-y-4" data-testid="risk-import-value-step">
      <p className="text-sm">
        {batch.staged_count} staged, {batch.rejected_count} not staged. Map each
        category and theme value once; a row whose value maps to nothing is
        rejected at approval.{" "}
        <Badge
          variant={unmapped > 0 ? "warning" : "pass"}
          data-testid="risk-import-value-unmapped"
        >
          {unmapped} unmapped
        </Badge>
      </p>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
            <tr>
              <th className="px-3 py-2 font-medium">Value in the file</th>
              <th className="px-3 py-2 font-medium">Kind</th>
              <th className="px-3 py-2 font-medium">Rows</th>
              <th className="px-3 py-2 font-medium">Maps to</th>
            </tr>
          </thead>
          <tbody>
            {values.map((v) => {
              const options =
                v.kind === "category"
                  ? (catsQ.data ?? []).map((c) => ({
                      value: c.slug,
                      label: c.name,
                    }))
                  : (themesQ.data ?? []).map((t) => ({
                      value: t.name,
                      label: t.name,
                    }));
              return (
                <tr
                  key={`${v.kind}:${v.value}`}
                  className="border-t"
                  data-testid="risk-import-value-row"
                  data-value={v.value}
                >
                  <td className="px-3 py-2">
                    <label htmlFor={`value-${v.kind}-${v.value}`}>
                      {v.value}
                    </label>
                  </td>
                  <td className="px-3 py-2 capitalize">{v.kind}</td>
                  <td className="px-3 py-2">{v.row_count}</td>
                  <td className="px-3 py-2">
                    <Select
                      id={`value-${v.kind}-${v.value}`}
                      value={v.target ?? ""}
                      disabled={mapM.isPending}
                      onChange={(e) => {
                        const target = e.target.value;
                        if (!target) return;
                        mapM.mutate({
                          v,
                          choice:
                            target === CREATE
                              ? createChoice(v.kind, v.value)
                              : { target },
                        });
                      }}
                      data-testid="risk-import-value-select"
                    >
                      <option value="">Choose a {v.kind}</option>
                      {v.target &&
                      !options.some((o) => o.value === v.target) ? (
                        <option value={v.target}>{v.target}</option>
                      ) : null}
                      {options.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                      {canCreate && !v.target ? (
                        <option value={CREATE}>
                          Create {v.kind} &ldquo;{v.value}&rdquo;
                        </option>
                      ) : null}
                    </Select>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {!canCreate ? (
        <p className="text-xs text-muted-foreground">
          Only an approver can create a category or theme; map to an existing
          one, or ask an approver.
        </p>
      ) : null}
      {mapM.isError ? (
        <Alert variant="destructive" data-testid="risk-import-value-error">
          <AlertDescription>{mapM.error.message}</AlertDescription>
        </Alert>
      ) : null}
      <Button onClick={onContinue} data-testid="risk-import-to-review">
        Continue to review
      </Button>
    </div>
  );
}

export default function RiskImportUploadPage() {
  const router = useRouter();
  const { toast } = useToast();
  const qc = useQueryClient();
  const fileId = useId();
  const methodologyId = useId();
  const [file, setFile] = useState<File | null>(null);
  const [methodology, setMethodology] = useState<string>(
    IMPORT_METHODOLOGIES[0],
  );
  const [preview, setPreview] = useState<RiskImportPreview | null>(null);
  const [assignments, setAssignments] = useState<Assignments>({});
  const [batch, setBatch] = useState<RiskImportBatch | null>(null);

  const fields = preview ? riskMappingFields(preview) : [];

  const previewM = useMutation({
    mutationFn: () => previewUpload(uploadForm(file!, methodology)),
    onSuccess: (p) => {
      setPreview(p);
      setAssignments(initialAssignments(p.columns));
    },
  });

  const toReview = (b: RiskImportBatch) => {
    void qc.invalidateQueries({ queryKey: ["risk-import", b.id] });
    router.push(reviewPath(b.id));
  };

  const stageM = useMutation({
    mutationFn: () =>
      stageUpload(
        uploadForm(
          file!,
          methodology,
          uploadMapping(preview!.columns, assignments),
        ),
      ),
    onSuccess: (b) => {
      toast(
        `Import staged: ${b.staged_count} staged, ${b.rejected_count} not staged`,
      );
      if (needsValueStep(b)) setBatch(b);
      else toReview(b);
    },
  });

  const step = batch ? "values" : preview ? "columns" : "file";

  return (
    <PageShell
      title="Import risks"
      subtitle="Upload your risk register as CSV or XLSX, check how its columns map to risk fields, then stage it for an approver to review. Nothing becomes a risk until it is approved."
      width={step === "file" ? "reading" : undefined}
      actions={
        <Link
          href="/risks"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Back to risks
        </Link>
      }
      data-testid="risk-import-upload"
    >
      <ol
        className="mb-6 flex gap-4 text-sm text-muted-foreground"
        data-testid="risk-import-steps"
      >
        {[
          ["file", "1. File"],
          ["columns", "2. Columns"],
          ["values", "3. Values"],
        ].map(([s, label]) => (
          <li
            key={s}
            aria-current={s === step ? "step" : undefined}
            className={s === step ? "font-medium text-foreground" : undefined}
          >
            {label}
          </li>
        ))}
      </ol>

      {step === "file" ? (
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            previewM.mutate();
          }}
        >
          <div className="space-y-1.5">
            <label htmlFor={fileId} className="text-sm font-medium">
              Risk register
            </label>
            <input
              id={fileId}
              type="file"
              accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
              className="block w-full text-sm"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
              data-testid="risk-import-file"
              required
            />
            <p className="text-xs text-muted-foreground">
              A file exported from the Risks page re-imports with no mapping.
              Capped at 5 MiB, 5000 rows and 200 columns.
            </p>
          </div>
          <div className="space-y-1.5">
            <label htmlFor={methodologyId} className="text-sm font-medium">
              Scoring methodology for the whole file
            </label>
            <Select
              id={methodologyId}
              value={methodology}
              onChange={(e) => setMethodology(e.target.value)}
              data-testid="risk-import-methodology"
            >
              {IMPORT_METHODOLOGIES.map((m) => (
                <option key={m} value={m}>
                  {methodologyLabel(m)}
                </option>
              ))}
            </Select>
          </div>
          {previewM.isError ? (
            <Alert variant="destructive" data-testid="risk-import-upload-error">
              <AlertDescription>{previewM.error.message}</AlertDescription>
            </Alert>
          ) : null}
          <Button
            type="submit"
            disabled={!file || previewM.isPending}
            data-testid="risk-import-read"
          >
            {previewM.isPending ? "Reading…" : "Read columns"}
          </Button>
        </form>
      ) : null}

      {step === "columns" && preview ? (
        <div className="space-y-4">
          <p className="text-sm" data-testid="risk-import-header-note">
            {preview.header_detected
              ? `Headers read from row ${
                  preview.header_row
                } of ${file?.name}; ${
                  preview.data_rows
                } data rows, scored as ${methodologyLabel(
                  preview.methodology,
                )}.`
              : `No row of ${file?.name} names a title and a category column, so row ${preview.header_row} is offered as the header. Map its columns below; ${preview.data_rows} data rows follow it.`}
          </p>
          <ColumnMapping
            fields={fields}
            columns={preview.columns}
            rows={preview.rows}
            value={assignments}
            onChange={setAssignments}
            testId="risk-import-mapping"
          />
          {stageM.isError ? (
            <Alert variant="destructive" data-testid="risk-import-stage-error">
              <AlertDescription>{stageM.error.message}</AlertDescription>
            </Alert>
          ) : null}
          <div className="flex gap-2">
            <Button
              onClick={() => stageM.mutate()}
              disabled={
                missingRequired(fields, assignments).length > 0 ||
                stageM.isPending
              }
              data-testid="risk-import-stage"
            >
              {stageM.isPending ? "Staging…" : "Stage for review"}
            </Button>
            <Button
              variant="outline"
              onClick={() => {
                setPreview(null);
                stageM.reset();
              }}
            >
              Choose another file
            </Button>
          </div>
        </div>
      ) : null}

      {step === "values" && batch ? (
        <ValueStep
          batch={batch}
          onBatch={setBatch}
          onContinue={() => toReview(batch)}
        />
      ) : null}
    </PageShell>
  );
}
