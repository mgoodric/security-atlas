"use client";

// #1785 / #2836 — upload a control sheet (CSV or XLSX) and map it before
// staging.
//
// 1. File: the sheet and the framework its "tested under" codes belong to.
//    The platform reads the header row (POST /v1/controls/imports/preview)
//    and stages nothing.
// 2. Columns: each detected header with the control field it maps to,
//    pre-filled from the platform's aliases and changeable, the unmapped
//    columns and a preview of the first rows (the ColumnMapping step the
//    risk import shares, #2848). Staging sends the mapping as the upload's
//    column map (POST /v1/controls/imports).
// Then the batch's review page.
//
// Caps, header detection, alias matching and every refusal are the
// platform's; its error messages are shown inline.

import { useMutation, useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useId, useState } from "react";

import { ColumnMapping } from "@/components/import/column-mapping";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { useToast } from "@/components/ui/toast";
import {
  type ControlImportPreview,
  controlMappingFields,
  previewUpload,
  stageUpload,
  uploadForm,
} from "@/lib/api/control-import-upload";
import {
  type Assignments,
  initialAssignments,
  missingRequired,
  uploadMapping,
} from "@/lib/import/column-mapping";

type Framework = { id: string; name: string; slug: string };

async function fetchFrameworks(): Promise<Framework[]> {
  const res = await fetch("/api/frameworks");
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return ((await res.json()) as { frameworks: Framework[] }).frameworks;
}

export default function ControlImportUploadPage() {
  const router = useRouter();
  const { toast } = useToast();
  const fileId = useId();
  const frameworkId = useId();
  const [file, setFile] = useState<File | null>(null);
  const [framework, setFramework] = useState("");
  const [preview, setPreview] = useState<ControlImportPreview | null>(null);
  const [assignments, setAssignments] = useState<Assignments>({});

  const frameworksQ = useQuery({
    queryKey: ["frameworks"],
    queryFn: fetchFrameworks,
  });

  const fields = preview ? controlMappingFields(preview) : [];
  const frameworkName =
    frameworksQ.data?.find((f) => f.slug === framework)?.name ?? framework;

  const previewM = useMutation({
    mutationFn: () => previewUpload(uploadForm(file!)),
    onSuccess: (p) => {
      setPreview(p);
      setAssignments(initialAssignments(p.columns));
    },
  });

  const stageM = useMutation({
    mutationFn: () =>
      stageUpload(
        uploadForm(
          file!,
          framework,
          uploadMapping(preview!.columns, assignments),
        ),
      ),
    onSuccess: (batch) => {
      toast(
        `Import staged: ${batch.staged_count} staged, ${batch.rejected_count} not staged`,
      );
      router.push(`/controls/imports/${encodeURIComponent(batch.id)}`);
    },
  });

  const step = preview ? "columns" : "file";

  return (
    <PageShell
      title="Import controls"
      subtitle="Upload your existing controls as CSV or XLSX, check how its columns map to control fields, then stage it. Every row is staged for review or reported back with the reason it was not."
      width={step === "file" ? "reading" : undefined}
      actions={
        <Link
          href="/controls"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Back to controls
        </Link>
      }
      data-testid="control-import-upload"
    >
      <ol
        className="mb-6 flex gap-4 text-sm text-muted-foreground"
        data-testid="control-import-steps"
      >
        {[
          ["file", "1. File"],
          ["columns", "2. Columns"],
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
            <label htmlFor={frameworkId} className="text-sm font-medium">
              Framework the &ldquo;tested under&rdquo; codes belong to
            </label>
            <Select
              id={frameworkId}
              value={framework}
              onChange={(e) => setFramework(e.target.value)}
              data-testid="control-import-framework"
              required
            >
              <option value="">
                {frameworksQ.isLoading
                  ? "Loading frameworks…"
                  : "Choose a framework"}
              </option>
              {(frameworksQ.data ?? []).map((f) => (
                <option key={f.id} value={f.slug}>
                  {f.name}
                </option>
              ))}
            </Select>
          </div>
          <div className="space-y-1.5">
            <label htmlFor={fileId} className="text-sm font-medium">
              Control sheet
            </label>
            <input
              id={fileId}
              type="file"
              accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
              className="block w-full text-sm"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
              data-testid="control-import-file"
              required
            />
            <p className="text-xs text-muted-foreground">
              Recognised columns include ID, title, description, tested under,
              implementation type and applicability; any other header can be
              mapped in the next step. Capped at 5 MiB, 5000 rows and 200
              columns.
            </p>
          </div>
          {previewM.isError ? (
            <Alert
              variant="destructive"
              data-testid="control-import-upload-error"
            >
              <AlertDescription>{previewM.error.message}</AlertDescription>
            </Alert>
          ) : null}
          <Button
            type="submit"
            disabled={!file || !framework || previewM.isPending}
            data-testid="control-import-read"
          >
            {previewM.isPending ? "Reading…" : "Read columns"}
          </Button>
        </form>
      ) : null}

      {step === "columns" && preview ? (
        <div className="space-y-4">
          <p className="text-sm" data-testid="control-import-header-note">
            {preview.header_detected
              ? `Headers read from row ${preview.header_row} of ${file?.name}; ${preview.data_rows} data rows, tested under ${frameworkName}.`
              : `No row of ${file?.name} has two recognised column headers, so row ${preview.header_row} is offered as the header. Map its columns below; ${preview.data_rows} data rows follow it.`}
          </p>
          <ColumnMapping
            fields={fields}
            columns={preview.columns}
            rows={preview.rows}
            value={assignments}
            onChange={setAssignments}
            testId="control-import-mapping"
          />
          {stageM.isError ? (
            <Alert
              variant="destructive"
              data-testid="control-import-stage-error"
            >
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
              data-testid="control-import-submit"
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
    </PageShell>
  );
}
