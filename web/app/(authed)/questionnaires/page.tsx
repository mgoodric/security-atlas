// Slice 263 — Stage A: questionnaire list + Excel upload.
//
// Closes AC-1..AC-7 (slice 263):
//   - Lists the tenant's questionnaires from /api/questionnaires (BFF)
//   - Empty state: hero CTA only (drag-drop zone + headline)
//   - Non-empty state: table of rows + "+ Upload Excel" header button
//   - Excel-upload validates client-side (5MB + .xlsx) then POSTs to
//     /api/questionnaires (create) + /api/questionnaires/{id}/import-
//     excel; on 200 navigates to the new questionnaire's authoring view.
//
// AI-assist boundary (P0-263-1): NO model badges, NO "AI drafted"
// cards, NO retrieval-context panels. The list view is pure operator
// CRUD on the questionnaire roster.

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useState } from "react";

import { UploadZone } from "@/components/questionnaire/upload-zone";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

interface Questionnaire {
  id: string;
  name: string;
  source_label?: string;
  source_filename?: string;
  status: string;
  updated_at?: string;
  created_at?: string;
}

interface ListResp {
  questionnaires: Questionnaire[];
}

async function fetchList(): Promise<Questionnaire[]> {
  const res = await fetch("/api/questionnaires", { cache: "no-store" });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON
    }
    throw new Error(msg);
  }
  const body = (await res.json()) as ListResp;
  return body.questionnaires ?? [];
}

async function createQuestionnaire(name: string): Promise<Questionnaire> {
  const res = await fetch("/api/questionnaires", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, source_filename: name }),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON
    }
    throw new Error(msg);
  }
  return (await res.json()) as Questionnaire;
}

async function importExcel(id: string, file: File): Promise<void> {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch(`/api/questionnaires/${id}/import-excel`, {
    method: "POST",
    body: form,
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON
    }
    throw new Error(msg);
  }
}

export default function QuestionnairesPage() {
  const router = useRouter();
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);

  const listQ = useQuery({
    queryKey: ["questionnaires"],
    queryFn: fetchList,
  });

  const uploadMu = useMutation({
    mutationFn: async (file: File) => {
      // Derive a friendly name from the filename (strip extension).
      const name = file.name.replace(/\.xlsx$/i, "") || "New questionnaire";
      const created = await createQuestionnaire(name);
      await importExcel(created.id, file);
      return created;
    },
    onSuccess: (q) => {
      // Invalidate the list so the new row appears when the user
      // returns; navigate straight to Stage C authoring view.
      void qc.invalidateQueries({ queryKey: ["questionnaires"] });
      router.push(`/questionnaires/${q.id}`);
    },
    onError: (err: Error) => {
      setError(err.message);
    },
  });

  const items = listQ.data ?? [];
  const isEmpty = !listQ.isLoading && items.length === 0;

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">
            Questionnaires
          </h1>
          <p className="text-sm text-muted-foreground">
            Vendor security diligence questionnaires. Upload an Excel
            workbook to import questions; author answers with prior-
            answer suggestions and policy / evidence citations.
          </p>
        </div>
        {!isEmpty && !listQ.isLoading ? (
          <Button
            data-testid="questionnaires-upload-button"
            onClick={() => {
              // Programmatic upload entry — show the upload zone modal-style.
              // For v1 we keep it simple: clicking the header button focuses
              // the same UploadZone rendered below; if the user is on the
              // non-empty page we render the zone inline above the table.
              setError(null);
              // Scroll the upload zone into view to keep the affordance
              // discoverable without forcing a modal.
              document
                .querySelector('[data-testid="questionnaire-upload-zone"]')
                ?.scrollIntoView({ behavior: "smooth", block: "center" });
            }}
          >
            + Upload Excel
          </Button>
        ) : null}
      </div>

      {error ? (
        <Alert variant="destructive" data-testid="questionnaires-error">
          <AlertTitle>Upload failed</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      {listQ.isLoading ? <ListSkeleton /> : null}

      {/* Empty state — single hero CTA per slice 263 D1
          (user-confirmed: no roster cards, no helper-text card,
          no sample-questionnaire CTA). */}
      {isEmpty ? (
        <div className="py-8" data-testid="questionnaires-empty">
          <UploadZone
            busy={uploadMu.isPending}
            onFile={(f) => {
              setError(null);
              uploadMu.mutate(f);
            }}
            onValidationError={setError}
          />
        </div>
      ) : null}

      {/* Non-empty state — list of questionnaires + an inline upload zone
          so the operator can drop another workbook without leaving the
          page. The header button focuses this zone. */}
      {!isEmpty && !listQ.isLoading ? (
        <>
          <Card>
            <CardHeader>
              <CardTitle>Roster</CardTitle>
              <CardDescription>
                {items.length} questionnaire{items.length === 1 ? "" : "s"}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <QuestionnaireTable items={items} />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Upload another</CardTitle>
              <CardDescription>
                Drop an .xlsx workbook to import a new questionnaire.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <UploadZone
                busy={uploadMu.isPending}
                onFile={(f) => {
                  setError(null);
                  uploadMu.mutate(f);
                }}
                onValidationError={setError}
              />
            </CardContent>
          </Card>
        </>
      ) : null}
    </div>
  );
}

function QuestionnaireTable({ items }: { items: Questionnaire[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead className="w-32">Source</TableHead>
          <TableHead className="w-28">Status</TableHead>
          <TableHead className="w-40">Last modified</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((q) => (
          <TableRow
            key={q.id}
            data-testid="questionnaire-row"
            className="cursor-pointer"
          >
            <TableCell>
              <Link
                href={`/questionnaires/${q.id}`}
                className="text-sm font-medium hover:underline"
                data-testid="questionnaire-row-link"
              >
                {q.name}
              </Link>
              {q.source_filename ? (
                <div className="text-xs text-muted-foreground mt-0.5 font-mono truncate">
                  {q.source_filename}
                </div>
              ) : null}
            </TableCell>
            <TableCell className="text-xs">
              {q.source_label || "—"}
            </TableCell>
            <TableCell>
              <StatusBadge value={q.status} />
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {q.updated_at
                ? new Date(q.updated_at).toLocaleString()
                : "never"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function StatusBadge({ value }: { value: string }) {
  const variant =
    value === "final"
      ? "default"
      : value === "draft"
        ? "secondary"
        : "outline";
  return <Badge variant={variant}>{value || "draft"}</Badge>;
}

function ListSkeleton() {
  return (
    <div className="space-y-2" data-testid="questionnaires-loading">
      {Array.from({ length: 3 }).map((_, i) => (
        <Skeleton key={i} className="h-12 w-full" />
      ))}
    </div>
  );
}
