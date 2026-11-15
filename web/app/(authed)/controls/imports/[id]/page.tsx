"use client";

// #1785 — review one control import batch.
//
// Lists the batch's staged rows with their ranked SCF anchor candidates
// (source and "reaches N of M"), flags a row no candidate reaches, and
// decides rows: approve with a candidate, approve with any anchor found by
// search, reject with a reason, or bulk approve-clear. Implementation type
// and applicability can be set at approval; without them the platform uses
// the sheet's values, then manual attestation and every scope cell.
//
// Every decision is the platform's (internal/api/controlimports): it
// resolves the caller to the approving person and refuses a machine
// credential, so the page adds no checks of its own beyond requiring a
// reject reason before sending.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { use, useState } from "react";

import { ScfAnchorPicker } from "@/components/control/scf-anchor-picker";
import { DetailHeader } from "@/components/detail/detail-header";
import { Term } from "@/components/help/term";
import { FilterPills } from "@/components/list/filter-pills";
import { ScopePicker } from "@/components/scope/scope-picker";
import { NotFoundPanel } from "@/components/shell/not-found-panel";
import { PageShell } from "@/components/shell/page-shell";
import type { Predicate } from "@/components/scope/scope-picker-model";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { ConfirmInline } from "@/components/ui/row-actions";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { bffControlFetch } from "@/lib/api/_shared";
import type { Anchor } from "@/lib/api/anchors";
import { APIError } from "@/lib/api/base";
import {
  type ApproveClearResult,
  type BatchRow,
  type Candidate,
  type ImportBatch,
  type RowFilter,
  type RowList,
  IMPLEMENTATION_TYPES,
  IMPLEMENTATION_TYPE_LABELS,
  ROW_STATUSES,
  ROW_STATUS_LABELS,
  type RowStatus,
  approveClearMessage,
  batchPath,
  batchProgress,
  effectiveImplementationType,
  fitLabel,
  initialApplicability,
  parseApplicability,
  postJSON,
  rowPath,
  rowsQuery,
  sourceLabel,
  topCandidates,
} from "@/lib/api/control-imports";

// Suggestions are computed after staging; poll while they are pending.
const SUGGESTIONS_POLL_MS = 3000;

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

export default function ControlImportReviewPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const qc = useQueryClient();
  const { toast } = useToast();
  const [filter, setFilter] = useState<RowFilter>({
    status: "",
    noCandidateReaches: false,
  });
  const [confirmingClear, setConfirmingClear] = useState(false);

  const batchQ = useQuery({
    queryKey: ["control-import", id],
    queryFn: () => bffControlFetch<ImportBatch>(batchPath(id)),
    refetchInterval: (q) =>
      q.state.data?.suggestions_status === "pending"
        ? SUGGESTIONS_POLL_MS
        : false,
  });
  const rowsQ = useQuery({
    queryKey: ["control-import", id, "rows", filter],
    queryFn: () =>
      bffControlFetch<RowList>(`${batchPath(id)}/rows${rowsQuery(filter)}`),
    refetchInterval: (q) =>
      q.state.data?.suggestions_status === "pending"
        ? SUGGESTIONS_POLL_MS
        : false,
  });

  // A decision changes the batch and, for an approval, the tenant's
  // controls: drop the cached controls list so the Controls page shows
  // the new control when the reviewer goes back to it.
  function refresh() {
    void qc.invalidateQueries({ queryKey: ["control-import", id] });
    void qc.invalidateQueries({ queryKey: ["controls"] });
  }

  const clearM = useMutation({
    mutationFn: () =>
      postJSON<ApproveClearResult>(`${batchPath(id)}/approve-clear`),
    onSuccess: (r) => toast(approveClearMessage(r)),
    onError: (err) => {
      // A concurrent approve-clear of this batch is a 409: not an error in
      // what the reviewer did, so it is reported as a toast.
      if (err instanceof APIError && err.status === 409) toast(err.message);
    },
    onSettled: () => {
      setConfirmingClear(false);
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
      <PageShell
        title="Import controls"
        width="reading"
        actions={
          <Link
            href="/controls"
            className={buttonVariants({ variant: "outline", size: "sm" })}
          >
            Back to controls
          </Link>
        }
      >
        <Alert variant="destructive" data-testid="control-import-load-error">
          <AlertDescription>
            Could not load this import: {batchQ.error?.message ?? "no response"}
          </AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  const batch = batchQ.data;
  const progress = batchProgress(batch);
  const rows = rowsQ.data?.rows ?? [];
  const exportHref = `${batchPath(id)}/export`;

  return (
    <DetailHeader
      title={batch.source_filename}
      titleTestId="control-import-title"
      data-testid="control-import-review"
      headerTestId="control-import-header"
      status={
        <>
          <Badge variant="secondary">
            {batch.framework.slug} {batch.framework.version}
          </Badge>
          {batch.scf_release ? (
            <Badge variant="secondary">SCF {batch.scf_release.version}</Badge>
          ) : null}
          {batch.suggestions_status !== "ready" ? (
            <Badge
              variant={
                batch.suggestions_status === "pending" ? "info" : "warning"
              }
              data-testid="control-import-suggestions-status"
            >
              Suggestions {batch.suggestions_status}
            </Badge>
          ) : null}
        </>
      }
      subtitle={<ProgressLine progress={progress} />}
      back={
        <Link
          href="/controls"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Back to controls
        </Link>
      }
    >
      <div className="flex flex-wrap items-start gap-2">
        <Button
          variant="secondary"
          size="sm"
          disabled={progress.pending === 0 || clearM.isPending}
          onClick={() => setConfirmingClear(true)}
          data-testid="control-import-approve-clear"
        >
          Approve clear rows
        </Button>
        {progress.approved > 0 ? (
          <a
            href={exportHref}
            download
            rel="noopener"
            className={buttonVariants({ variant: "outline", size: "sm" })}
            data-testid="control-import-export"
          >
            Export approved
          </a>
        ) : null}
        {confirmingClear ? (
          <ConfirmInline
            prompt="Approve every pending row whose top candidate comes from the crosswalk and reaches all of its tested criteria?"
            confirmLabel="Approve clear rows"
            busy={clearM.isPending}
            onCancel={() => setConfirmingClear(false)}
            onConfirm={() => clearM.mutate()}
            data-testid="control-import-approve-clear-confirm"
          >
            <span className="text-xs text-muted-foreground">
              Each approval creates a control in your organisation. Other rows
              stay pending.
            </span>
          </ConfirmInline>
        ) : null}
      </div>
      {clearM.isError &&
      !(clearM.error instanceof APIError && clearM.error.status === 409) ? (
        <Alert variant="destructive" data-testid="control-import-clear-error">
          <AlertDescription>{clearM.error.message}</AlertDescription>
        </Alert>
      ) : null}

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
            id: "fit",
            label: "Fit",
            value: filter.noCandidateReaches ? "none" : "",
            options: [
              { value: "", label: "All rows" },
              { value: "none", label: "No candidate reaches" },
            ],
          },
        ]}
        onChange={(pill, value) =>
          setFilter((f) =>
            pill === "status"
              ? { ...f, status: value as RowStatus | "" }
              : { ...f, noCandidateReaches: value === "none" },
          )
        }
        meta={rowsQ.data ? `Showing ${rows.length}` : undefined}
      />

      {rowsQ.isLoading ? (
        <Skeleton className="h-32 w-full" />
      ) : rowsQ.isError ? (
        <Alert variant="destructive">
          <AlertDescription>{rowsQ.error.message}</AlertDescription>
        </Alert>
      ) : rows.length === 0 ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="control-import-rows-empty"
        >
          No staged rows match these filters.
        </p>
      ) : (
        <ul className="space-y-3" data-testid="control-import-rows">
          {rows.map((row) => (
            <ImportRow
              key={row.id}
              batchID={id}
              row={row}
              onDecided={refresh}
            />
          ))}
        </ul>
      )}

      {(batch.rejected ?? []).length > 0 ? (
        <details data-testid="control-import-not-staged">
          <summary className="cursor-pointer text-sm font-medium">
            {batch.rejected_count} not staged at upload
          </summary>
          <ul className="mt-2 space-y-1 text-sm">
            {(batch.rejected ?? []).map((r) => (
              <li key={r.row_number}>
                Row {r.row_number}
                {r.external_id ? ` (${r.external_id})` : ""}: {r.detail}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </DetailHeader>
  );
}

function ProgressLine({
  progress,
}: {
  progress: ReturnType<typeof batchProgress>;
}) {
  const parts: [string, number, string][] = [
    ["Staged", progress.staged, "staged"],
    ["Approved", progress.approved, "approved"],
    ["Rejected", progress.rejected, "rejected"],
    ["Pending", progress.pending, "pending"],
  ];
  if (progress.duplicate > 0) {
    parts.push(["Duplicate", progress.duplicate, "duplicate"]);
  }
  return (
    <p className="text-sm" data-testid="control-import-progress">
      {parts.map(([label, n, key], i) => (
        <span key={key}>
          {i > 0 ? " · " : ""}
          {label}{" "}
          <span
            className="font-medium tabular-nums"
            data-testid={`control-import-progress-${key}`}
          >
            {n}
          </span>
        </span>
      ))}
    </p>
  );
}

type ApproveChoice = { candidate_rank: number } | { scf_id: string };

function ImportRow({
  batchID,
  row,
  onDecided,
}: {
  batchID: string;
  row: BatchRow;
  onDecided: () => void;
}) {
  const { toast } = useToast();
  const [implType, setImplType] = useState("");
  const [settingScope, setSettingScope] = useState(false);
  // While the picker is open, the predicate it shows. Seeded on open from
  // the sheet (initialApplicability), so a visible Everything is sent as
  // {op: "true"}. undefined: nothing is sent and the platform keeps the
  // sheet's value (picker closed, or a predicate shown as advanced).
  // null: the picker is mid-choice and approval must wait.
  const [appl, setAppl] = useState<Predicate | null | undefined>(undefined);
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");

  const anchorsQ = useQuery({
    queryKey: ["anchors"],
    queryFn: () =>
      bffControlFetch<{ anchors: Anchor[] }>("/api/anchors").then(
        (b) => b.anchors,
      ),
    enabled: searching,
  });

  const approveM = useMutation({
    mutationFn: (choice: ApproveChoice) =>
      postJSON<{ scf_id?: string }>(rowPath(batchID, row.id, "approve"), {
        ...choice,
        ...(implType ? { implementation_type: implType } : {}),
        ...(settingScope && appl != null ? { applicability_expr: appl } : {}),
      }),
    onSuccess: (d) => {
      toast(`Approved ${row.external_id} with ${d.scf_id ?? "the anchor"}`);
      setSearching(false);
    },
    onSettled: onDecided,
  });
  const rejectM = useMutation({
    mutationFn: () =>
      postJSON(rowPath(batchID, row.id, "reject"), { reason: reason.trim() }),
    onSuccess: () => {
      toast(`Rejected ${row.external_id}`);
      setRejecting(false);
    },
    onSettled: onDecided,
  });

  const pending = row.status === "pending";
  const busy = approveM.isPending || rejectM.isPending;
  const blocked = busy || (settingScope && appl === null);
  const candidates = topCandidates(row);
  const tested = row.tested_under ?? [];

  return (
    <li
      className="space-y-3 rounded-lg border p-4"
      data-testid="control-import-row"
      data-row-id={row.id}
      data-external-id={row.external_id}
      data-status={row.status}
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">
            Row {row.row_number} ·{" "}
            <span className="font-mono">{row.external_id}</span>
          </p>
          <p className="font-medium">{row.title}</p>
          {row.description ? (
            <p className="text-sm text-muted-foreground">{row.description}</p>
          ) : null}
          <p className="mt-1 text-xs text-muted-foreground">
            Tested under:{" "}
            {tested.length > 0 ? tested.map((t) => t.code).join(", ") : "none"}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {row.no_candidate_reaches ? (
            <Badge
              variant="warning"
              data-testid="control-import-row-no-candidate-reaches"
            >
              No candidate reaches a tested criterion
            </Badge>
          ) : null}
          <Badge
            variant={statusVariant(row.status)}
            data-testid="control-import-row-status"
          >
            {ROW_STATUS_LABELS[row.status as RowStatus] ?? row.status}
          </Badge>
        </div>
      </div>

      {!pending ? (
        <Decision row={row} />
      ) : (
        <>
          {candidates.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No candidates yet. Search for an <Term id="anchor">anchor</Term>{" "}
              instead.
            </p>
          ) : (
            <ol className="space-y-2" data-testid="control-import-candidates">
              {candidates.map((c) => (
                <CandidateItem
                  key={c.rank}
                  candidate={c}
                  disabled={blocked}
                  onApprove={() => approveM.mutate({ candidate_rank: c.rank })}
                />
              ))}
            </ol>
          )}

          <div className="flex flex-wrap items-end gap-3">
            <label className="space-y-1 text-xs">
              <span className="block text-muted-foreground">
                Implementation type
              </span>
              <Select
                value={implType}
                onChange={(e) => setImplType(e.target.value)}
                className="w-56"
                data-testid="control-import-impl-type"
              >
                <option value="">
                  {IMPLEMENTATION_TYPE_LABELS[
                    effectiveImplementationType(row)
                  ] ?? effectiveImplementationType(row)}{" "}
                  ({row.implementation_type ? "from sheet" : "default"})
                </option>
                {IMPLEMENTATION_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {IMPLEMENTATION_TYPE_LABELS[t]}
                  </option>
                ))}
              </Select>
            </label>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                const opening = !settingScope;
                setSettingScope(opening);
                setAppl(
                  opening ? initialApplicability(row.applicability) : undefined,
                );
              }}
              data-testid="control-import-set-applicability"
            >
              {settingScope ? "Keep sheet applicability" : "Set applicability"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setSearching((v) => !v)}
              data-testid="control-import-search-anchor"
            >
              {searching ? "Close search" : "Search another anchor"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setRejecting(true)}
              disabled={busy}
              data-testid="control-import-reject"
            >
              Reject
            </Button>
          </div>

          {settingScope ? (
            <div data-testid="control-import-applicability">
              <ScopePicker
                value={parseApplicability(row.applicability)}
                onChange={setAppl}
              />
            </div>
          ) : null}

          {searching ? (
            <ScfAnchorPicker
              prompt="Approve this control with any anchor of the batch's SCF release."
              anchors={anchorsQ.data ?? []}
              loading={anchorsQ.isLoading}
              query={query}
              onQueryChange={setQuery}
              onPick={(scfID) => approveM.mutate({ scf_id: scfID })}
              pending={blocked}
            />
          ) : null}

          {rejecting ? (
            <ConfirmInline
              prompt={`Reject ${row.external_id}? It is excluded from this import and the reason is kept.`}
              confirmLabel="Reject"
              busy={rejectM.isPending || reason.trim() === ""}
              onCancel={() => {
                setRejecting(false);
                setReason("");
              }}
              onConfirm={() => rejectM.mutate()}
              data-testid="control-import-reject-confirm"
            >
              <textarea
                aria-label="Reason for rejecting"
                placeholder="Reason (required)"
                className="w-full rounded-md border p-2 text-sm"
                rows={2}
                maxLength={2000}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                data-testid="control-import-reject-reason"
              />
            </ConfirmInline>
          ) : null}

          {approveM.isError || rejectM.isError ? (
            <Alert variant="destructive" data-testid="control-import-row-error">
              <AlertDescription>
                {(approveM.error ?? rejectM.error)?.message}
              </AlertDescription>
            </Alert>
          ) : null}
        </>
      )}
    </li>
  );
}

function CandidateItem({
  candidate: c,
  disabled,
  onApprove,
}: {
  candidate: Candidate;
  disabled: boolean;
  onApprove: () => void;
}) {
  const fit = fitLabel(c);
  return (
    <li
      className="flex flex-wrap items-start justify-between gap-2 rounded-md border px-3 py-2"
      data-testid="control-import-candidate"
      data-scf-id={c.scf_id}
    >
      <div className="min-w-0 space-y-0.5">
        <p className="text-sm">
          <span className="font-mono">{c.scf_id}</span> {c.title}
        </p>
        <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <Badge
            variant="secondary"
            data-testid="control-import-candidate-source"
          >
            {sourceLabel(c)}
          </Badge>
          {fit ? (
            <span data-testid="control-import-candidate-fit">{fit}</span>
          ) : null}
        </p>
        {c.rationale ? (
          <p className="text-xs text-muted-foreground">{c.rationale}</p>
        ) : null}
      </div>
      <Button
        size="sm"
        disabled={disabled}
        onClick={onApprove}
        data-testid="control-import-approve-candidate"
      >
        Approve with {c.scf_id}
      </Button>
    </li>
  );
}

function Decision({ row }: { row: BatchRow }) {
  if (row.status === "approved") {
    return (
      <p className="text-sm" data-testid="control-import-row-decision">
        Approved with <span className="font-mono">{row.approved_scf_id}</span>.{" "}
        {row.control_id ? (
          <Link
            href={`/controls/${encodeURIComponent(row.control_id)}`}
            className="underline"
          >
            Open control
          </Link>
        ) : null}
      </p>
    );
  }
  if (row.status === "duplicate") {
    return (
      <p className="text-sm" data-testid="control-import-row-decision">
        Already imported.{" "}
        {row.control_id ? (
          <Link
            href={`/controls/${encodeURIComponent(row.control_id)}`}
            className="underline"
          >
            Open the existing control
          </Link>
        ) : null}
      </p>
    );
  }
  return (
    <p className="text-sm" data-testid="control-import-row-decision">
      Rejected: {row.rejection_reason}
    </p>
  );
}
