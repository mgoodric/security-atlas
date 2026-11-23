"use client";

// #2847 — review one risk import batch (spec #2812).
//
// Lists the batch's staged rows with their mapped fields, review flags
// (an unlinked owner, a treatment downgraded to untreated, a failed parent
// link) and unmapped values, then the rows refused at upload with their
// reasons. A filter narrows to the flagged rows. An approver approves every
// pending row, or approves and rejects single rows; anyone else sees the
// same page read-only. A GRC engineer and the uploader read only too (#2881):
// the batch read's can_decide says which.
//
// Every decision is the platform's (internal/api/risks/riskimports): it
// resolves the caller to the approving person and refuses a machine
// credential, so the page adds no checks of its own beyond hiding the
// buttons from a non-approver and requiring a reject reason before sending.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { use, useRef, useState } from "react";

import { DetailHeader } from "@/components/detail/detail-header";
import { FilterPills } from "@/components/list/filter-pills";
import { NotFoundPanel } from "@/components/shell/not-found-panel";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Person } from "@/components/ui/person";
import { ConfirmInline } from "@/components/ui/row-actions";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { bffControlFetch } from "@/lib/api/_shared";
import { APIError } from "@/lib/api/base";
import {
  type ApproveResult,
  type BatchProgress,
  type BatchRow,
  type RiskImportBatch,
  type RowDecision,
  type RowFilter,
  type RowStatus,
  NO_FILTER,
  ROW_STATUSES,
  ROW_STATUS_LABELS,
  approveAllMessage,
  approveAllWarning,
  batchPath,
  batchProgress,
  emptyRowsMessage,
  filterRows,
  flagLabel,
  mappedFields,
  postJSON,
  rejectReasonLabel,
  reviewMode,
  rowPath,
  unmappedEntries,
  unmappedPendingCount,
  unmappedValues,
} from "@/lib/api/risk-imports";

// ValueMappings lists the batch's distinct category and theme values and
// what each maps to (#2844), with how many map to nothing. A row carrying
// an unmapped value is rejected at approval.
function ValueMappings({ batch }: { batch: RiskImportBatch }) {
  const groups = [
    { label: "Category values", values: batch.category_values ?? [] },
    { label: "Theme values", values: batch.theme_values ?? [] },
  ].filter((g) => g.values.length > 0);
  if (groups.length === 0) return null;
  const unmapped = unmappedValues(batch).length;
  return (
    <section className="space-y-2 text-sm" data-testid="risk-import-values">
      <p>
        <span className="font-medium">Category and theme values</span>{" "}
        <Badge
          variant={unmapped > 0 ? "warning" : "pass"}
          data-testid="risk-import-unmapped-count"
        >
          {unmapped} unmapped
        </Badge>
      </p>
      {groups.map((g) => (
        <div key={g.label}>
          <span className="text-muted-foreground">{g.label}: </span>
          {g.values.map((v, i) => (
            <span key={`${v.kind}:${v.value}`} data-unmapped={!v.target}>
              {i > 0 ? ", " : ""}
              {v.value} ({v.row_count}) &rarr;{" "}
              {v.target ? (
                v.target
              ) : (
                <span className="font-medium text-destructive">unmapped</span>
              )}
            </span>
          ))}
        </div>
      ))}
    </section>
  );
}

function statusVariant(status: string) {
  switch (status) {
    case "approved":
      return "pass" as const;
    case "rejected":
      return "destructive" as const;
    case "duplicate":
      return "warning" as const;
    default:
      return "secondary" as const;
  }
}

function BackToRisks() {
  return (
    <Link
      href="/risks"
      className={buttonVariants({ variant: "outline", size: "sm" })}
    >
      Back to risks
    </Link>
  );
}

export default function RiskImportReviewPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const qc = useQueryClient();
  const { toast } = useToast();
  const [filter, setFilter] = useState<RowFilter>(NO_FILTER);
  const [confirmingAll, setConfirmingAll] = useState(false);
  const allRef = useRef<HTMLButtonElement>(null);

  const batchQ = useQuery({
    queryKey: ["risk-import", id],
    queryFn: () => bffControlFetch<RiskImportBatch>(batchPath(id)),
  });
  // #2881: the batch read says whether this caller decides; a GRC
  // engineer and the uploader read only.
  const mode = reviewMode(batchQ.data);

  // A decision changes the batch and, for an approval, the tenant's
  // risks: drop the cached risks list so the Risks page shows them.
  function refresh() {
    void qc.invalidateQueries({ queryKey: ["risk-import", id] });
    void qc.invalidateQueries({ queryKey: ["risks"] });
  }

  const allM = useMutation({
    mutationFn: () => postJSON<ApproveResult>(`${batchPath(id)}/approve`),
    onSuccess: (r) => toast(approveAllMessage(r)),
    onSettled: () => {
      setConfirmingAll(false);
      refresh();
    },
  });

  if (batchQ.isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }
  if (batchQ.error instanceof APIError && batchQ.error.status === 404) {
    return <NotFoundPanel />;
  }
  if (batchQ.isError || !batchQ.data) {
    return (
      <PageShell title="Import risks" width="reading" actions={<BackToRisks />}>
        <Alert variant="destructive" data-testid="risk-import-load-error">
          <AlertDescription>
            Could not load this import: {batchQ.error?.message ?? "no response"}
          </AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  const batch = batchQ.data;
  const progress = batchProgress(batch);
  const rows = filterRows(batch.staged, filter);
  const unmappedColumns = batch.unmapped_columns ?? [];
  const rejected = batch.rejected ?? [];
  const unmappedPending = unmappedPendingCount(batch);
  const allWarning = approveAllWarning(batch);

  return (
    <DetailHeader
      title={batch.source_filename}
      titleTestId="risk-import-title"
      data-testid="risk-import-review"
      data-mode={mode}
      headerTestId="risk-import-header"
      status={
        <>
          <Badge variant="secondary">{batch.source_format.toUpperCase()}</Badge>
          <Badge
            variant={batch.status === "completed" ? "pass" : "info"}
            data-testid="risk-import-batch-status"
          >
            {batch.status === "completed" ? "Completed" : "Awaiting review"}
          </Badge>
        </>
      }
      subtitle={<ProgressLine progress={progress} />}
      back={<BackToRisks />}
    >
      {mode === "decide" ? (
        <div className="flex flex-wrap items-start gap-2">
          <Button
            size="sm"
            disabled={progress.pending === 0 || allM.isPending}
            ref={allRef}
            onClick={() => setConfirmingAll(true)}
            data-testid="risk-import-approve-all"
          >
            Approve all pending
          </Button>
          {confirmingAll ? (
            <ConfirmInline
              prompt={`Approve all ${progress.pending} pending ${
                progress.pending === 1 ? "row" : "rows"
              }?${
                unmappedPending > 0
                  ? ` ${unmappedPending} will be rejected for an unmapped value.`
                  : ""
              }`}
              confirmLabel="Approve all pending"
              busy={allM.isPending}
              onCancel={() => setConfirmingAll(false)}
              returnFocus={() => allRef.current}
              onConfirm={() => allM.mutate()}
              data-testid="risk-import-approve-all-confirm"
            >
              <span className="text-xs text-muted-foreground">
                Each approval creates a risk in your organisation.
                {progress.flagged > 0
                  ? ` ${progress.flagged} flagged ${
                      progress.flagged === 1 ? "row is" : "rows are"
                    } included.`
                  : ""}{" "}
                A row whose risk ID is already in use is marked duplicate.
                {allWarning ? (
                  <span
                    className="mt-1 block font-medium text-destructive"
                    data-testid="risk-import-approve-all-unmapped"
                  >
                    {allWarning}
                  </span>
                ) : null}
              </span>
            </ConfirmInline>
          ) : null}
        </div>
      ) : (
        <p
          className="text-sm text-muted-foreground"
          data-testid="risk-import-read-only"
        >
          Read-only: an admin or approver who did not upload this import
          approves or rejects its rows.
        </p>
      )}
      {allM.isError ? (
        <Alert
          variant="destructive"
          data-testid="risk-import-approve-all-error"
        >
          <AlertDescription>{allM.error.message}</AlertDescription>
        </Alert>
      ) : null}

      {unmappedColumns.length > 0 ? (
        <p className="text-sm" data-testid="risk-import-unmapped-columns">
          <span className="text-muted-foreground">
            Not imported into any risk field:{" "}
          </span>
          {unmappedColumns.join(", ")}
        </p>
      ) : null}

      <ValueMappings batch={batch} />

      <FilterPills
        pills={[
          {
            id: "status",
            label: "Status",
            value: filter.status,
            options: [
              { value: "", label: "All" },
              ...ROW_STATUSES.map((s) => ({
                value: s,
                label: ROW_STATUS_LABELS[s],
              })),
            ],
          },
          {
            id: "flags",
            label: "Flags",
            value: filter.flaggedOnly ? "flagged" : "",
            options: [
              { value: "", label: "All rows" },
              { value: "flagged", label: `Flagged (${progress.flagged})` },
            ],
          },
        ]}
        onChange={(pill, value) =>
          setFilter((f) =>
            pill === "status"
              ? { ...f, status: value as RowStatus | "" }
              : { ...f, flaggedOnly: value === "flagged" },
          )
        }
        meta={`Showing ${rows.length}`}
      />

      {rows.length === 0 ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="risk-import-rows-empty"
        >
          {emptyRowsMessage(batch, filter)}
        </p>
      ) : (
        <ul className="space-y-3" data-testid="risk-import-rows">
          {rows.map((row) => (
            <ImportRow
              key={row.id}
              batchID={id}
              row={row}
              canDecide={mode === "decide"}
              onDecided={refresh}
            />
          ))}
        </ul>
      )}

      {rejected.length > 0 ? (
        <section className="space-y-2" data-testid="risk-import-not-staged">
          <h2 className="text-sm font-medium">
            {batch.rejected_count} not staged at upload
          </h2>
          <ul className="space-y-1 text-sm">
            {rejected.map((r) => (
              <li
                key={r.row_number}
                data-testid="risk-import-not-staged-row"
                data-reason={r.reason}
              >
                Row {r.row_number}
                {r.external_ref ? (
                  <span className="font-mono"> ({r.external_ref})</span>
                ) : null}
                :{" "}
                <span className="font-medium">
                  {rejectReasonLabel(r.reason)}
                </span>
                {r.detail ? (
                  <span className="text-muted-foreground"> — {r.detail}</span>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </DetailHeader>
  );
}

function ProgressLine({ progress }: { progress: BatchProgress }) {
  const parts: [string, number, string][] = [
    ["Staged", progress.staged, "staged"],
    ["Pending", progress.pending, "pending"],
    ["Approved", progress.approved, "approved"],
    ["Rejected", progress.rejected, "rejected"],
  ];
  if (progress.duplicate > 0) {
    parts.push(["Duplicate", progress.duplicate, "duplicate"]);
  }
  // #2957: approved rows whose parent link waits on a pending row, here
  // or in another import. Shown only when there are some.
  if (progress.waiting > 0) {
    parts.push(["Waiting on a parent", progress.waiting, "waiting"]);
  }
  parts.push(["Flagged", progress.flagged, "flagged"]);
  parts.push(["Not staged", progress.notStaged, "not-staged"]);
  return (
    <p className="text-sm" data-testid="risk-import-progress">
      {parts.map(([label, n, key], i) => (
        <span key={key}>
          {i > 0 ? " · " : ""}
          {label}{" "}
          <span
            className="font-medium tabular-nums"
            data-testid={`risk-import-progress-${key}`}
          >
            {n}
          </span>
        </span>
      ))}
    </p>
  );
}

function ImportRow({
  batchID,
  row,
  canDecide,
  onDecided,
}: {
  batchID: string;
  row: BatchRow;
  canDecide: boolean;
  onDecided: () => void;
}) {
  const { toast } = useToast();
  const [rejecting, setRejecting] = useState(false);
  const rejectRef = useRef<HTMLButtonElement>(null);
  const reasonRef = useRef<HTMLTextAreaElement>(null);
  const [reason, setReason] = useState("");
  const label = row.external_ref || `row ${row.row_number}`;

  const approveM = useMutation({
    mutationFn: () =>
      postJSON<RowDecision>(rowPath(batchID, row.id, "approve")),
    onSuccess: (d) =>
      toast(
        d.status === "duplicate"
          ? `${label} is already a risk; marked duplicate`
          : `Approved ${label}`,
      ),
    onSettled: onDecided,
  });
  const rejectM = useMutation({
    mutationFn: () =>
      postJSON(rowPath(batchID, row.id, "reject"), { reason: reason.trim() }),
    onSuccess: () => {
      toast(`Rejected ${label}`);
      setRejecting(false);
    },
    onSettled: onDecided,
  });

  const pending = row.status === "pending";
  const busy = approveM.isPending || rejectM.isPending;
  const flags = row.flags ?? [];
  const unmapped = unmappedEntries(row);

  return (
    <li
      className="space-y-3 rounded-lg border p-4"
      data-testid="risk-import-row"
      data-row-id={row.id}
      data-external-ref={row.external_ref ?? ""}
      data-status={row.status}
      data-flagged={flags.length > 0 ? "true" : "false"}
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <p className="text-xs text-muted-foreground">Row {row.row_number}</p>
        <div className="flex flex-wrap items-center gap-2">
          {flags.map((f) => (
            <Badge
              key={f}
              variant="warning"
              data-testid="risk-import-row-flag"
              data-flag={f}
            >
              {flagLabel(f)}
            </Badge>
          ))}
          <Badge
            variant={statusVariant(row.status)}
            data-testid="risk-import-row-status"
          >
            {ROW_STATUS_LABELS[row.status as RowStatus] ?? row.status}
          </Badge>
        </div>
      </div>

      <dl
        className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm"
        data-testid="risk-import-row-fields"
      >
        {mappedFields(row).map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className={k === "Risk ID" ? "font-mono" : undefined}>
              {k === "Owner" && row.owner_user_id ? (
                <Person id={row.owner_user_id} testId="risk-import-row-owner" />
              ) : (
                v
              )}
            </dd>
          </div>
        ))}
      </dl>

      {unmapped.length > 0 ? (
        <div data-testid="risk-import-row-unmapped">
          <p className="text-xs text-muted-foreground">
            Unmapped values (kept on the import, not on the risk)
          </p>
          <dl className="mt-1 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-xs">
            {unmapped.map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="text-muted-foreground">{k}</dt>
                <dd>{v}</dd>
              </div>
            ))}
          </dl>
        </div>
      ) : null}

      {!pending ? (
        <Decision row={row} />
      ) : canDecide ? (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              disabled={busy}
              onClick={() => approveM.mutate()}
              data-testid="risk-import-approve-row"
            >
              Approve
            </Button>
            <Button
              variant="outline"
              size="sm"
              ref={rejectRef}
              disabled={busy}
              onClick={() => setRejecting(true)}
              data-testid="risk-import-reject-row"
            >
              Reject
            </Button>
          </div>
          {rejecting ? (
            <ConfirmInline
              prompt={`Reject ${label}? It is excluded from this import and the reason is kept.`}
              confirmLabel="Reject"
              busy={rejectM.isPending || reason.trim() === ""}
              initialFocus={reasonRef}
              returnFocus={() => rejectRef.current}
              onCancel={() => {
                setRejecting(false);
                setReason("");
              }}
              onConfirm={() => rejectM.mutate()}
              data-testid="risk-import-reject-confirm"
            >
              <textarea
                ref={reasonRef}
                aria-label="Reason for rejecting"
                placeholder="Reason (required)"
                className="w-full rounded-md border p-2 text-sm"
                rows={2}
                maxLength={2000}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                data-testid="risk-import-reject-reason"
              />
            </ConfirmInline>
          ) : null}
          {approveM.isError || rejectM.isError ? (
            <Alert variant="destructive" data-testid="risk-import-row-error">
              <AlertDescription>
                {(approveM.error ?? rejectM.error)?.message}
              </AlertDescription>
            </Alert>
          ) : null}
        </>
      ) : null}
    </li>
  );
}

function Decision({ row }: { row: BatchRow }) {
  if (row.status === "approved") {
    return (
      <p className="text-sm" data-testid="risk-import-row-decision">
        Approved.{" "}
        {row.risk_id ? (
          <Link
            href={`/risks/${encodeURIComponent(row.risk_id)}`}
            className="underline"
          >
            Open risk
          </Link>
        ) : null}
      </p>
    );
  }
  if (row.status === "duplicate") {
    return (
      <p className="text-sm" data-testid="risk-import-row-decision">
        Risk ID already in use; nothing created.{" "}
        {row.risk_id ? (
          <Link
            href={`/risks/${encodeURIComponent(row.risk_id)}`}
            className="underline"
          >
            Open the existing risk
          </Link>
        ) : null}
      </p>
    );
  }
  return (
    <p className="text-sm" data-testid="risk-import-row-decision">
      Rejected: {row.rejection_reason}
    </p>
  );
}
