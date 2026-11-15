"use client";

// #1802 — the "audited under" report (#1801) as a page.
//
// One row per control and criterion the control was tested under that its
// SCF anchor does not reach, through catalog plus the organisation's
// overlay edges. Each row shows the control's anchor and how well it fits,
// and the candidate anchors the catalog maps the criterion to in the
// current SCF release.
//
// "Add overlay edge" maps the criterion to the control's own anchor (the
// overlay POST of #1797, with its role guard). The source is pre-filled
// from the tested-under note; the rationale is the user's to write. The
// row leaves the list only after the platform has stored the edge; a
// refusal (403 role, 409 duplicate, 400 invalid) shows as a toast and
// inline beside the form, and the row stays.
//
// Filter by framework and group by control or framework are client-side
// over one read. The CSV export carries the framework filter.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Fragment, useEffect, useId, useMemo, useState } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { strmStyle } from "@/components/control/strm";
import { APIError } from "@/lib/api/base";
import {
  addOverlayEdge,
  auditedUnderExportURL,
  defaultSourceRef,
  edgeFor,
  fetchAuditedUnder,
  frameworksOf,
  groupRows,
  OVERLAY_RELATIONSHIPS,
  rowKey,
  type AuditedUnderReport,
  type AuditedUnderRow,
  type GroupBy,
} from "@/lib/api/audited-under";
import { pageTitle } from "@/lib/page-titles";

import { anchorFitLabel } from "../../controls/filters";

const QUERY_KEY = ["mapping-overlays", "audited-under"] as const;

const SELECT_CLASS =
  "h-8 rounded-md border bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

export default function AuditedUnderPage() {
  const router = useRouter();
  const q = useQuery({
    queryKey: QUERY_KEY,
    queryFn: fetchAuditedUnder,
    retry: false,
  });
  const [framework, setFramework] = useState("");
  const [groupBy, setGroupBy] = useState<GroupBy>("control");
  const [openKey, setOpenKey] = useState<string | null>(null);

  useEffect(() => {
    if (q.error instanceof APIError && q.error.status === 401) {
      router.push("/login?from=/catalog/audited-under");
    }
  }, [q.error, router]);

  const allRows = useMemo(() => q.data?.rows ?? [], [q.data]);
  const frameworks = useMemo(() => frameworksOf(allRows), [allRows]);
  const rows = framework
    ? allRows.filter((r) => r.framework === framework)
    : allRows;
  const groups = groupRows(rows, groupBy);

  return (
    <PageShell
      title={pageTitle("/catalog/audited-under")}
      subtitle="Criteria your controls were tested under that their SCF anchor does not reach. Add an overlay edge to record the mapping your audit relied on."
      data-testid="audited-under-page"
      actions={
        <a
          href={auditedUnderExportURL(framework)}
          download
          rel="noopener"
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid="audited-under-export-csv"
        >
          Export CSV
        </a>
      }
    >
      {q.isLoading ? <Skeleton className="h-40 w-full" /> : null}

      {q.isError && !(q.error instanceof APIError && q.error.status === 401) ? (
        <Alert variant="destructive" data-testid="audited-under-error">
          <AlertTitle>Could not load the report</AlertTitle>
          <AlertDescription>
            {q.error instanceof Error ? q.error.message : "Request failed"}
          </AlertDescription>
        </Alert>
      ) : null}

      {q.data ? (
        <>
          <div className="flex flex-wrap items-center gap-4 text-sm">
            <label className="flex items-center gap-2">
              <span>Framework</span>
              <select
                className={SELECT_CLASS}
                value={framework}
                onChange={(e) => setFramework(e.target.value)}
                data-testid="audited-under-framework"
              >
                <option value="">All frameworks</option>
                {frameworks.map((f) => (
                  <option key={f} value={f}>
                    {f}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex items-center gap-2">
              <span>Group by</span>
              <select
                className={SELECT_CLASS}
                value={groupBy}
                onChange={(e) => setGroupBy(e.target.value as GroupBy)}
                data-testid="audited-under-group-by"
              >
                <option value="control">Control</option>
                <option value="framework">Framework</option>
              </select>
            </label>
            <span
              className="text-muted-foreground"
              data-testid="audited-under-count"
            >
              {rows.length === 1 ? "1 gap" : `${rows.length} gaps`}
              {q.data.candidate_scf_release
                ? ` · candidates from SCF ${q.data.candidate_scf_release}`
                : null}
            </span>
          </div>

          {rows.length === 0 ? (
            <p
              className="text-sm text-muted-foreground"
              data-testid="audited-under-empty"
            >
              Every criterion your controls were tested under is reached by
              their SCF anchor.
            </p>
          ) : (
            groups.map((g) => (
              <section
                key={g.key}
                className="space-y-2"
                data-testid="audited-under-group"
              >
                <h2 className="text-sm font-semibold">
                  {g.label}{" "}
                  <span className="font-mono text-xs font-normal text-muted-foreground">
                    {g.detail}
                  </span>
                </h2>
                <div className="list-table-wrap">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Criterion</TableHead>
                        <TableHead>
                          {groupBy === "control" ? "Framework" : "Control"}
                        </TableHead>
                        <TableHead>Anchor and fit</TableHead>
                        <TableHead>Candidate anchors</TableHead>
                        <TableHead className="w-40">
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {g.rows.map((r) => (
                        <GapRow
                          key={rowKey(r)}
                          row={r}
                          groupBy={groupBy}
                          open={openKey === rowKey(r)}
                          onOpen={() => setOpenKey(rowKey(r))}
                          onClose={() => setOpenKey(null)}
                        />
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </section>
            ))
          )}
        </>
      ) : null}
    </PageShell>
  );
}

function GapRow({
  row,
  groupBy,
  open,
  onOpen,
  onClose,
}: {
  row: AuditedUnderRow;
  groupBy: GroupBy;
  open: boolean;
  onOpen: () => void;
  onClose: () => void;
}) {
  const anchored = Boolean(row.anchor_id && row.anchor_code);
  return (
    <Fragment>
      <TableRow data-testid="audited-under-row" data-row-key={rowKey(row)}>
        <TableCell>
          <span
            className="font-mono text-xs"
            data-testid="audited-under-criterion"
          >
            {row.requirement_code}
          </span>
          <span className="block text-xs text-muted-foreground">
            {row.requirement_title}
          </span>
          {row.tested_under_note ? (
            <span className="block text-xs text-muted-foreground">
              Tested under: {row.tested_under_note}
            </span>
          ) : null}
        </TableCell>
        <TableCell>
          {groupBy === "control" ? (
            <span className="text-sm">
              {row.framework}{" "}
              <span className="text-xs text-muted-foreground">
                {row.framework_version}
              </span>
            </span>
          ) : (
            <Link
              href={`/controls/${encodeURIComponent(row.control_id)}`}
              className="text-sm underline underline-offset-3"
            >
              {row.control_title}
            </Link>
          )}
        </TableCell>
        <TableCell>
          {anchored ? (
            <span
              className="font-mono text-xs"
              data-testid="audited-under-anchor"
            >
              {row.anchor_code}
              {row.anchor_scf_release ? `@${row.anchor_scf_release}` : null}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">No anchor</span>
          )}
          <span className="block">
            <Badge
              variant={row.anchor_fit === "exact" ? "outline" : "warning"}
              title={row.anchor_fit_note}
              data-testid="audited-under-fit"
              data-anchor-fit={row.anchor_fit}
            >
              {anchorFitLabel(row.anchor_fit)}
            </Badge>
          </span>
          {row.anchor_fit_note ? (
            <span className="block text-xs text-muted-foreground">
              {row.anchor_fit_note}
            </span>
          ) : null}
        </TableCell>
        <TableCell>
          {row.candidate_anchors.length === 0 ? (
            <span className="text-xs text-muted-foreground">None</span>
          ) : (
            <ul className="space-y-1" data-testid="audited-under-candidates">
              {row.candidate_anchors.map((c) => (
                <li
                  key={c.anchor_id}
                  className="text-xs"
                  data-testid="audited-under-candidate"
                >
                  <Link
                    href={`/catalog/scf/${encodeURIComponent(c.anchor_id)}`}
                    className="font-mono underline underline-offset-3"
                  >
                    {c.anchor_code}
                  </Link>{" "}
                  <span className="text-muted-foreground">
                    {c.anchor_title} ({strmStyle(c.relationship_type).label},{" "}
                    {c.strength})
                  </span>
                </li>
              ))}
            </ul>
          )}
        </TableCell>
        <TableCell className="text-right">
          {anchored ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onOpen}
              disabled={open}
              data-testid="audited-under-add-edge"
            >
              Add overlay edge
            </Button>
          ) : (
            <span className="text-xs text-muted-foreground">
              Anchor the control first
            </span>
          )}
        </TableCell>
      </TableRow>
      {open ? (
        <TableRow>
          <TableCell colSpan={5} className="whitespace-normal">
            <AddEdgeForm row={row} onClose={onClose} />
          </TableCell>
        </TableRow>
      ) : null}
    </Fragment>
  );
}

function AddEdgeForm({
  row,
  onClose,
}: {
  row: AuditedUnderRow;
  onClose: () => void;
}) {
  const id = useId();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [rationale, setRationale] = useState("");
  const [sourceRef, setSourceRef] = useState(() => defaultSourceRef(row));
  const [relationship, setRelationship] = useState<string>(
    OVERLAY_RELATIONSHIPS[0],
  );
  const [strength, setStrength] = useState("0.5");

  const strengthNum = Number(strength);
  const edge = edgeFor(row, {
    relationship_type: relationship,
    strength: strengthNum,
    rationale,
    source_ref: sourceRef,
  });
  const valid =
    edge !== null &&
    edge.rationale !== "" &&
    edge.source_ref !== "" &&
    strength.trim() !== "" &&
    Number.isFinite(strengthNum) &&
    strengthNum >= 0 &&
    strengthNum <= 1;

  const add = useMutation({
    mutationFn: () => addOverlayEdge(edge!),
    onSuccess: () => {
      // The platform stored the edge, so this row is reached: close it in
      // place, then re-read, since the edge may reach other controls'
      // rows on the same anchor too.
      qc.setQueryData<AuditedUnderReport>(QUERY_KEY, (prev) =>
        prev
          ? {
              ...prev,
              rows: prev.rows.filter((r) => rowKey(r) !== rowKey(row)),
              count: prev.count - 1,
            }
          : prev,
      );
      void qc.invalidateQueries({ queryKey: QUERY_KEY });
      toast(
        `Overlay edge added: ${row.framework} ${row.requirement_code} to ${row.anchor_code}`,
      );
      onClose();
    },
    onError: (err) => {
      toast(`Overlay edge not added: ${(err as Error).message}`);
    },
  });

  return (
    <form
      className="flex max-w-xl flex-col gap-3 rounded-lg border bg-muted/30 p-3 text-sm"
      aria-label={`Add overlay edge for ${row.requirement_code}`}
      data-testid="audited-under-edge-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (valid && !add.isPending) add.mutate();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <p>
        Map{" "}
        <span className="font-mono">
          {row.framework} {row.requirement_code}
        </span>{" "}
        to <span className="font-mono">{row.anchor_code}</span>, the anchor of{" "}
        {row.control_title}.
      </p>
      <label className="flex flex-col gap-1" htmlFor={`${id}-rationale`}>
        <span>Rationale (required)</span>
        <textarea
          id={`${id}-rationale`}
          className="min-h-20 w-full rounded-md border bg-background p-2 text-sm"
          value={rationale}
          onChange={(e) => setRationale(e.target.value)}
          maxLength={4000}
          required
          autoFocus
          data-testid="audited-under-edge-rationale"
        />
      </label>
      <label className="flex flex-col gap-1" htmlFor={`${id}-source`}>
        <span>Source</span>
        <Input
          id={`${id}-source`}
          value={sourceRef}
          onChange={(e) => setSourceRef(e.target.value)}
          maxLength={1000}
          required
          data-testid="audited-under-edge-source"
        />
      </label>
      <div className="flex flex-wrap gap-4">
        <label className="flex items-center gap-2" htmlFor={`${id}-rel`}>
          <span>Relationship</span>
          <select
            id={`${id}-rel`}
            className={SELECT_CLASS}
            value={relationship}
            onChange={(e) => setRelationship(e.target.value)}
            data-testid="audited-under-edge-relationship"
          >
            {OVERLAY_RELATIONSHIPS.map((r) => (
              <option key={r} value={r}>
                {strmStyle(r).label}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-2" htmlFor={`${id}-strength`}>
          <span>Strength</span>
          <Input
            id={`${id}-strength`}
            type="number"
            min={0}
            max={1}
            step={0.1}
            className="w-20"
            value={strength}
            onChange={(e) => setStrength(e.target.value)}
            data-testid="audited-under-edge-strength"
          />
        </label>
      </div>
      {add.isError ? (
        <Alert variant="destructive" data-testid="audited-under-edge-error">
          <AlertDescription>{(add.error as Error).message}</AlertDescription>
        </Alert>
      ) : null}
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onClose}
          data-testid="audited-under-edge-cancel"
        >
          Cancel
        </Button>
        <Button
          type="submit"
          size="sm"
          disabled={!valid || add.isPending}
          data-testid="audited-under-edge-submit"
        >
          {add.isPending ? "Adding..." : "Add overlay edge"}
        </Button>
      </div>
    </form>
  );
}
