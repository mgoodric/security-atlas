"use client";

// #1794 — the Statement of Applicability tab on the framework-scope page
// (spec #1744). Self-contained: it reads the page's scope list from the
// shared ["framework-scopes", fv] query and owns everything else.
//
// - Requirement list with a decision, justification and implementation
//   reference per requirement; a contradiction badge on an excluded
//   requirement that still has a passing active control behind it.
// - Bulk selection: one decision + justification on many requirements.
// - CSV import, reporting each rejected row with its line and reason.
// - Filters (decision, undecided, contradiction) and counts for the whole
//   revision.
// - CSV / xlsx export of the revision on screen.
//
// Decisions on an activated or superseded revision are frozen by the
// platform, so the tab renders them read-only. Editing a revision in
// review or approved sends it back to draft; the tab says so and refreshes
// the scope list so the Scopes tab shows the new state. Submit, approve
// and activate stay on the Scopes tab.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

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
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  ApplicabilityDecision,
  ApplicabilityList,
  ImportResult,
  RequirementApplicability,
  SOA_EXPORT_FORMATS,
  SOA_FILTERS,
  SoAFilter,
  applicabilityQuery,
  buildSoAExportURL,
  defaultRevision,
  isFrozen,
  soaTitle,
} from "@/lib/api/applicability";
import { APIError } from "@/lib/api/base";
import { FrameworkScope } from "@/lib/api/framework-scopes";
import { upstreamErrorMessage } from "@/lib/api/_shared";

type Framework = { id: string; slug: string; latest_version_id?: string };

const FILTER_LABELS: Record<SoAFilter, string> = {
  all: "All",
  applicable: "Applicable",
  excluded: "Excluded",
  undecided: "Undecided",
  contradiction: "Contradictions",
};

// readError surfaces the platform's {"error": "..."} message, so a
// "justification is required" 400 reads as itself rather than a status.
async function readError(res: Response): Promise<APIError> {
  const msg = await upstreamErrorMessage(res);
  return new APIError(res.status, msg);
}

async function fetchApplicability(
  scopeID: string,
  filter: SoAFilter,
): Promise<ApplicabilityList> {
  const res = await fetch(
    `/api/framework-scopes/${encodeURIComponent(
      scopeID,
    )}/applicability${applicabilityQuery(filter)}`,
  );
  if (!res.ok) throw await readError(res);
  return (await res.json()) as ApplicabilityList;
}

async function fetchFrameworks(): Promise<Framework[]> {
  const res = await fetch("/api/frameworks");
  if (!res.ok) throw await readError(res);
  return ((await res.json()) as { frameworks: Framework[] }).frameworks;
}

// useSoATitle names the tab and its card: "Statement of Applicability" for
// an ISO/IEC 27001 version, "Applicability" otherwise. The version is
// matched against each framework's latest version; an older version falls
// back to the generic title.
export function useSoATitle(frameworkVersionID: string): string {
  const frameworksQ = useQuery({
    queryKey: ["frameworks"],
    queryFn: fetchFrameworks,
  });
  return soaTitle(
    frameworksQ.data?.find((f) => f.latest_version_id === frameworkVersionID)
      ?.slug,
  );
}

type DecisionBody = {
  decision: ApplicabilityDecision;
  justification: string;
  implementation_ref: string;
};

type WriteResult = { approval_invalidated: boolean };

async function setDecision(
  scopeID: string,
  requirementID: string,
  body: DecisionBody,
): Promise<WriteResult> {
  const res = await fetch(
    `/api/framework-scopes/${encodeURIComponent(
      scopeID,
    )}/applicability/${encodeURIComponent(requirementID)}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) throw await readError(res);
  return (await res.json()) as WriteResult;
}

async function bulkSetDecision(
  scopeID: string,
  requirementIDs: string[],
  body: DecisionBody,
): Promise<WriteResult> {
  const res = await fetch(
    `/api/framework-scopes/${encodeURIComponent(scopeID)}/applicability/bulk`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ requirement_ids: requirementIDs, ...body }),
    },
  );
  if (!res.ok) throw await readError(res);
  return (await res.json()) as WriteResult;
}

// importDecisions treats 422 as a result, not an error: it means no row
// applied, and the body still lists every rejected row.
async function importDecisions(
  scopeID: string,
  csv: string,
): Promise<ImportResult> {
  const res = await fetch(
    `/api/framework-scopes/${encodeURIComponent(scopeID)}/applicability/import`,
    { method: "POST", headers: { "Content-Type": "text/csv" }, body: csv },
  );
  if (!res.ok && res.status !== 422) throw await readError(res);
  return (await res.json()) as ImportResult;
}

export function SoATab({
  frameworkVersionID,
  scopes,
}: {
  frameworkVersionID: string;
  scopes: FrameworkScope[];
}) {
  const qc = useQueryClient();
  const [pickedID, setPickedID] = useState<string | null>(null);
  const [filter, setFilter] = useState<SoAFilter>("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);

  const scope =
    scopes.find((s) => s.id === pickedID) ?? defaultRevision(scopes);

  const title = useSoATitle(frameworkVersionID);

  const listQ = useQuery({
    queryKey: ["framework-scope-applicability", scope?.id, filter],
    queryFn: () => fetchApplicability(scope!.id, filter),
    enabled: !!scope,
  });

  const afterWrite = (res: { approval_invalidated: boolean }) => {
    setError(null);
    setNotice(
      res.approval_invalidated
        ? "Approval invalidated: applicability changed, so the scope is back in draft. Resubmit it for review."
        : null,
    );
    qc.invalidateQueries({
      queryKey: ["framework-scope-applicability", scope?.id],
    });
    qc.invalidateQueries({
      queryKey: ["framework-scopes", frameworkVersionID],
    });
  };
  const onWriteError = (e: unknown) => setError((e as Error).message);

  const rowMut = useMutation({
    mutationFn: (v: { requirementID: string; body: DecisionBody }) =>
      setDecision(scope!.id, v.requirementID, v.body),
    onSuccess: afterWrite,
    onError: onWriteError,
  });
  const bulkMut = useMutation({
    mutationFn: (body: DecisionBody) =>
      bulkSetDecision(scope!.id, [...selected], body),
    onSuccess: (res) => {
      setSelected(new Set());
      afterWrite(res);
    },
    onError: onWriteError,
  });
  const importMut = useMutation({
    mutationFn: (csv: string) => importDecisions(scope!.id, csv),
    onSuccess: (res) => {
      setImportResult(res);
      afterWrite(res);
    },
    onError: onWriteError,
  });

  if (!scope) {
    return (
      <p
        className="py-12 text-center text-sm text-muted-foreground"
        data-testid="soa-no-scope"
      >
        No framework scope for this version yet. Applicability decisions belong
        to a scope revision, so create one first.
      </p>
    );
  }

  const frozen = isFrozen(scope);
  const counts = listQ.data?.counts;
  const rows = listQ.data?.requirements ?? [];
  const allSelected =
    rows.length > 0 && rows.every((r) => selected.has(r.requirement_id));

  return (
    <Card data-testid="soa-tab">
      <CardHeader>
        <CardTitle data-testid="soa-title">{title}</CardTitle>
        <CardDescription>
          A decision per requirement: applicable, or excluded with a
          justification. Requirements with no decision count as applicable.
          Decisions are approved, activated and superseded with the scope
          revision.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-end gap-3">
          <div className="space-y-1">
            <label htmlFor="soa-revision" className="text-xs font-medium">
              Scope revision
            </label>
            <Select
              id="soa-revision"
              data-testid="soa-revision"
              value={scope.id}
              onChange={(e) => {
                setPickedID(e.target.value);
                setSelected(new Set());
                setImportResult(null);
              }}
              className="w-72"
            >
              {scopes.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} ({s.state})
                </option>
              ))}
            </Select>
          </div>
          <Badge variant="outline" data-testid="soa-revision-state">
            {scope.state}
          </Badge>
          <div className="ml-auto flex gap-2">
            {SOA_EXPORT_FORMATS.map((f) => (
              <a
                key={f}
                href={buildSoAExportURL(frameworkVersionID, f, scope.id)}
                download
                data-testid={`soa-export-${f}`}
                className="inline-flex h-8 items-center rounded-lg border border-border px-2.5 text-sm hover:bg-muted"
              >
                Export {f.toUpperCase()}
              </a>
            ))}
          </div>
        </div>

        {frozen && (
          <p
            className="text-xs text-muted-foreground"
            data-testid="soa-read-only"
          >
            This revision is {scope.state}; its decisions are frozen. Change
            applicability on a new draft revision.
          </p>
        )}

        {notice && (
          <Alert data-testid="soa-notice">
            <AlertTitle>Heads up</AlertTitle>
            <AlertDescription>{notice}</AlertDescription>
          </Alert>
        )}
        {error && (
          <Alert
            variant="destructive"
            data-testid="soa-error"
            aria-live="polite"
          >
            <AlertTitle>Could not save</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <Counts counts={counts} />

        <div
          className="flex flex-wrap gap-1.5"
          role="group"
          aria-label="Filter"
        >
          {SOA_FILTERS.map((f) => (
            <button
              key={f}
              type="button"
              aria-pressed={filter === f}
              data-testid={`soa-filter-${f}`}
              onClick={() => {
                setFilter(f);
                setSelected(new Set());
              }}
              className={`rounded-md border px-2.5 py-1 text-xs ${
                filter === f
                  ? "border-primary bg-primary/10 font-medium text-primary"
                  : "border-border bg-card text-muted-foreground hover:bg-muted/40"
              }`}
            >
              {FILTER_LABELS[f]}
            </button>
          ))}
        </div>

        {!frozen && (
          <ImportPanel
            pending={importMut.isPending}
            result={importResult}
            onFile={(csv) => importMut.mutate(csv)}
          />
        )}

        {!frozen && selected.size > 0 && (
          <BulkBar
            count={selected.size}
            pending={bulkMut.isPending}
            onApply={(body) => bulkMut.mutate(body)}
          />
        )}

        {listQ.isLoading && <Skeleton className="h-32 w-full" />}
        {listQ.error && (
          <Alert variant="destructive">
            <AlertTitle>Could not load applicability</AlertTitle>
            <AlertDescription>
              {(listQ.error as Error).message}
            </AlertDescription>
          </Alert>
        )}
        {listQ.data && rows.length === 0 && (
          <p
            className="py-8 text-center text-sm text-muted-foreground"
            data-testid="soa-empty"
          >
            No requirements match this filter.
          </p>
        )}
        {listQ.data && rows.length > 0 && (
          <Table data-testid="soa-table">
            <TableHeader>
              <TableRow>
                {!frozen && (
                  <TableHead className="w-8">
                    <Checkbox
                      aria-label="Select all requirements"
                      data-testid="soa-select-all"
                      checked={allSelected}
                      onCheckedChange={(checked) =>
                        setSelected(
                          checked === true
                            ? new Set(rows.map((r) => r.requirement_id))
                            : new Set(),
                        )
                      }
                    />
                  </TableHead>
                )}
                <TableHead className="w-28">Requirement</TableHead>
                <TableHead>Title</TableHead>
                <TableHead className="w-36">Decision</TableHead>
                <TableHead>Justification</TableHead>
                <TableHead>Implementation reference</TableHead>
                {!frozen && <TableHead className="w-20" />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <RequirementRow
                  // Remount on a saved change so the row's edit state
                  // starts from the stored decision.
                  key={`${r.requirement_id}:${r.updated_at ?? ""}`}
                  row={r}
                  frozen={frozen}
                  selected={selected.has(r.requirement_id)}
                  onSelect={(on) =>
                    setSelected((prev) => {
                      const next = new Set(prev);
                      if (on) next.add(r.requirement_id);
                      else next.delete(r.requirement_id);
                      return next;
                    })
                  }
                  pending={rowMut.isPending}
                  onSave={(body) =>
                    rowMut.mutate({ requirementID: r.requirement_id, body })
                  }
                />
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

function Counts({
  counts,
}: {
  counts: ApplicabilityList["counts"] | undefined;
}) {
  const cells: { key: keyof ApplicabilityList["counts"]; label: string }[] = [
    { key: "applicable", label: "Applicable" },
    { key: "excluded", label: "Excluded" },
    { key: "undecided", label: "Undecided" },
    { key: "contradictions", label: "Contradictions" },
  ];
  return (
    <div
      className="grid grid-cols-2 gap-3 md:grid-cols-4"
      data-testid="soa-counts"
    >
      {cells.map((c) => (
        <div
          key={c.key}
          className="rounded-xl bg-muted/40 p-3 ring-1 ring-foreground/5"
        >
          <div className="text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
            {c.label}
          </div>
          <div
            className={`mt-1 font-mono text-xl font-semibold ${
              c.key === "contradictions" && (counts?.contradictions ?? 0) > 0
                ? "text-destructive"
                : ""
            }`}
            data-testid={`soa-count-${c.key}`}
          >
            {counts ? counts[c.key] : "–"}
          </div>
        </div>
      ))}
    </div>
  );
}

function BulkBar({
  count,
  pending,
  onApply,
}: {
  count: number;
  pending: boolean;
  onApply: (body: DecisionBody) => void;
}) {
  const [decision, setDecision] = useState<ApplicabilityDecision>("excluded");
  const [justification, setJustification] = useState("");
  const [implementationRef, setImplementationRef] = useState("");
  return (
    <div
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-muted/30 p-3"
      data-testid="soa-bulk-bar"
    >
      <span className="text-sm font-medium" data-testid="soa-bulk-count">
        {count} selected
      </span>
      <Select
        aria-label="Bulk decision"
        data-testid="soa-bulk-decision"
        value={decision}
        onChange={(e) => setDecision(e.target.value as ApplicabilityDecision)}
        className="w-36"
      >
        <option value="applicable">Applicable</option>
        <option value="excluded">Excluded</option>
      </Select>
      <Input
        aria-label="Bulk justification"
        data-testid="soa-bulk-justification"
        placeholder="Justification (required to exclude)"
        value={justification}
        onChange={(e) => setJustification(e.target.value)}
        className="w-72"
      />
      <Input
        aria-label="Bulk implementation reference"
        data-testid="soa-bulk-implementation-ref"
        placeholder="Implementation reference"
        value={implementationRef}
        onChange={(e) => setImplementationRef(e.target.value)}
        className="w-56"
      />
      <Button
        size="sm"
        data-testid="soa-bulk-apply"
        disabled={pending}
        onClick={() =>
          onApply({
            decision,
            justification,
            implementation_ref: implementationRef,
          })
        }
      >
        Apply to {count}
      </Button>
    </div>
  );
}

function ImportPanel({
  pending,
  result,
  onFile,
}: {
  pending: boolean;
  result: ImportResult | null;
  onFile: (csv: string) => void;
}) {
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="soa-import-file" className="text-xs font-medium">
          Import CSV
        </label>
        <input
          id="soa-import-file"
          data-testid="soa-import-file"
          type="file"
          accept=".csv,text/csv"
          disabled={pending}
          className="text-xs"
          onChange={async (e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (file) onFile(await file.text());
          }}
        />
        <span className="text-xs text-muted-foreground">
          Columns: requirement_code, decision, justification,
          implementation_ref.
        </span>
      </div>
      {result && (
        <Alert
          variant={result.rejected.length > 0 ? "destructive" : "default"}
          data-testid="soa-import-result"
        >
          <AlertTitle data-testid="soa-import-summary">
            Imported {result.applied} row{result.applied === 1 ? "" : "s"}
            {result.rejected.length > 0 &&
              `; ${result.rejected.length} rejected`}
          </AlertTitle>
          {result.rejected.length > 0 && (
            <AlertDescription>
              <ul className="list-disc pl-4">
                {result.rejected.map((rj) => (
                  <li key={rj.line} data-testid="soa-import-rejected">
                    Line {rj.line}
                    {rj.requirement_code && ` (${rj.requirement_code})`}:{" "}
                    {rj.reason}
                  </li>
                ))}
              </ul>
            </AlertDescription>
          )}
        </Alert>
      )}
    </div>
  );
}

function RequirementRow({
  row,
  frozen,
  selected,
  onSelect,
  pending,
  onSave,
}: {
  row: RequirementApplicability;
  frozen: boolean;
  selected: boolean;
  onSelect: (on: boolean) => void;
  pending: boolean;
  onSave: (body: DecisionBody) => void;
}) {
  const [decision, setDecision] = useState<ApplicabilityDecision | "">(
    row.decision ?? "",
  );
  const [justification, setJustification] = useState(row.justification);
  const [implementationRef, setImplementationRef] = useState(
    row.implementation_ref,
  );
  const dirty =
    decision !== (row.decision ?? "") ||
    justification !== row.justification ||
    implementationRef !== row.implementation_ref;

  return (
    <TableRow data-testid="soa-row" data-code={row.code}>
      {!frozen && (
        <TableCell>
          <Checkbox
            aria-label={`Select ${row.code}`}
            data-testid="soa-row-select"
            checked={selected}
            onCheckedChange={(checked) => onSelect(checked === true)}
          />
        </TableCell>
      )}
      <TableCell className="font-mono text-xs">
        {row.code}
        {row.contradiction && (
          <Badge
            variant="warning"
            className="ml-2"
            data-testid="soa-contradiction"
            title="Excluded, but an active control behind it is passing"
          >
            contradiction
          </Badge>
        )}
      </TableCell>
      <TableCell className="text-sm">{row.title}</TableCell>
      <TableCell>
        {frozen ? (
          <span data-testid="soa-row-decision">
            {row.decision ?? "undecided"}
          </span>
        ) : (
          <Select
            aria-label={`Decision for ${row.code}`}
            data-testid="soa-row-decision"
            value={decision}
            onChange={(e) =>
              setDecision(e.target.value as ApplicabilityDecision | "")
            }
          >
            <option value="" disabled>
              Undecided
            </option>
            <option value="applicable">Applicable</option>
            <option value="excluded">Excluded</option>
          </Select>
        )}
      </TableCell>
      <TableCell>
        {frozen ? (
          <span className="text-sm">{row.justification}</span>
        ) : (
          <Input
            aria-label={`Justification for ${row.code}`}
            data-testid="soa-row-justification"
            value={justification}
            onChange={(e) => setJustification(e.target.value)}
          />
        )}
      </TableCell>
      <TableCell>
        {frozen ? (
          <span className="text-sm">{row.implementation_ref}</span>
        ) : (
          <Input
            aria-label={`Implementation reference for ${row.code}`}
            data-testid="soa-row-implementation-ref"
            value={implementationRef}
            onChange={(e) => setImplementationRef(e.target.value)}
          />
        )}
      </TableCell>
      {!frozen && (
        <TableCell>
          {dirty && decision !== "" && (
            <Button
              size="sm"
              variant="outline"
              data-testid="soa-row-save"
              disabled={pending}
              onClick={() =>
                onSave({
                  decision,
                  justification,
                  implementation_ref: implementationRef,
                })
              }
            >
              Save
            </Button>
          )}
        </TableCell>
      )}
    </TableRow>
  );
}
