"use client";

// #1785 — upload a control sheet (CSV or XLSX) for review.
//
// The operator picks the framework their "tested under" codes belong to
// and the file; the platform stages every row or refuses it with a
// reason (POST /v1/controls/imports). On success the page opens the
// batch's review page. Size, row and column caps are the platform's; its
// error message is shown inline beside the form.

import { useMutation, useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useId, useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { useToast } from "@/components/ui/toast";
import type { ImportBatch } from "@/lib/api/control-imports";
import { upstreamErrorMessage } from "@/lib/api/_shared";

type Framework = { id: string; name: string; slug: string };

async function fetchFrameworks(): Promise<Framework[]> {
  const res = await fetch("/api/frameworks");
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return ((await res.json()) as { frameworks: Framework[] }).frameworks;
}

async function upload(form: FormData): Promise<ImportBatch> {
  const res = await fetch("/api/controls/imports", {
    method: "POST",
    body: form,
  });
  if (!res.ok) {
    throw new Error(
      await upstreamErrorMessage(res, `upload failed (${res.status})`),
    );
  }
  return (await res.json()) as ImportBatch;
}

export default function ControlImportUploadPage() {
  const router = useRouter();
  const { toast } = useToast();
  const fileId = useId();
  const frameworkId = useId();
  const [file, setFile] = useState<File | null>(null);
  const [framework, setFramework] = useState("");

  const frameworksQ = useQuery({
    queryKey: ["frameworks"],
    queryFn: fetchFrameworks,
  });

  const uploadM = useMutation({
    mutationFn: () => {
      const form = new FormData();
      if (file) form.append("file", file);
      form.append("framework", framework);
      return upload(form);
    },
    onSuccess: (batch) => {
      toast(
        `Import staged: ${batch.staged_count} staged, ${batch.rejected_count} not staged`,
      );
      router.push(`/controls/imports/${encodeURIComponent(batch.id)}`);
    },
  });

  return (
    <PageShell
      title="Import controls"
      subtitle="Upload your existing controls as CSV or XLSX. Every row is staged for review or reported back with the reason it was not."
      width="reading"
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
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          uploadM.mutate();
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
            implementation type and applicability.
          </p>
        </div>
        {uploadM.isError ? (
          <Alert
            variant="destructive"
            data-testid="control-import-upload-error"
          >
            <AlertDescription>{uploadM.error.message}</AlertDescription>
          </Alert>
        ) : null}
        <Button
          type="submit"
          disabled={!file || !framework || uploadM.isPending}
          data-testid="control-import-submit"
        >
          {uploadM.isPending ? "Uploading…" : "Upload and stage"}
        </Button>
      </form>
    </PageShell>
  );
}
