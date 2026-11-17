"use client";

// #2117 — /audits/[id]: one audit period (parent #2071, "finish the
// record lifecycles").
//
// The header (DetailHeader, #2102) carries the name, status, framework
// and dates, and the period's one lifecycle action: Freeze, on an open
// period. Freezing is irreversible (invariant #10), so it confirms
// inline first, and the confirm says the evidence becomes immutable.
// A frozen period shows its freeze time, actor and hash, and the OSCAL
// export.
//
// The body is the per-control view. Each tenant control's row reads the
// period's horizon (GET /v1/audit-periods/{id}/control-state): how many
// evidence observations fall inside the period and the latest result.
// Opening a row mounts the audit workspace's ControlWorkspace for that
// control in this period, so the frozen evidence summary, populations,
// samples, walkthroughs and audit notes all come from the routes that
// workspace already uses. There is no period-scoped list of
// populations or samples on the platform; they hang off a control.
//
// Roles: the platform decides who may freeze and who may read the
// horizon. The page offers Freeze to everyone on an open period and
// shows the platform's 403 as a toast; it never widens or narrows that
// decision itself (#2291 is open on the policy).
//
// Auditors (#2118): the period's assigned auditors, with a picker of the
// tenant's other active auditors and a Remove per row. Only an admin may
// read or change them; anyone else sees the platform's 403 as a note in
// place of the list. An assigned auditor reaches the period through the
// auditor workspace (/audit).
//
// OSCAL export: the platform mounts the export route only when the
// OSCAL bridge is wired. The button fetches the bundle and, when the
// route is absent, says the bridge isn't running instead of saving an
// error body as a file.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect, useMemo, useState } from "react";

import { AnnotationDraftProvider } from "@/components/audit/annotation-draft-store";
import { ControlWorkspace } from "@/components/audit/control-workspace";
import { DetailHeader } from "@/components/detail/detail-header";
import { Term } from "@/components/help/term";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Person } from "@/components/ui/person";
import { ConfirmInline } from "@/components/ui/row-actions";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import {
  assignPeriodAuditor,
  fetchAuditPeriod,
  fetchAuditPeriods,
  fetchOscalBundle,
  fetchPeriodAuditors,
  fetchPeriodControlState,
  freezeAuditPeriod,
  removePeriodAuditor,
  type AuditPeriod,
  type AuditPeriodResponse,
} from "@/lib/api/audit-periods";
import { APIError } from "@/lib/api/base";
import {
  fetchTenantControlsList,
  type TenantControl,
} from "@/lib/api/controls-list";

import { resultBadgeVariant } from "../../evidence/[id]/view";
import {
  frameworkVersionLabel,
  isFrozen,
  periodRangeLabel,
  statusDotClass,
  statusPillClass,
} from "../format";
import {
  OSCAL_EXPORT_DOWNLOAD_LABEL,
  oscalExportDownloadFilename,
} from "../oscal-export";
import {
  auditorChangeErrorMessage,
  auditorLabel,
  FREEZE_CONFIRM_DETAIL,
  FREEZE_CONFIRM_PROMPT,
  freezeErrorMessage,
  oscalExportErrorMessage,
  summarizeControlState,
  toWorkspacePeriod,
} from "./view";

// Rows shown before "Show more". Each row makes one horizon read.
const CONTROLS_PAGE = 25;

function noRetryOn(statuses: number[]) {
  return (count: number, err: unknown) =>
    !(err instanceof APIError && statuses.includes(err.status)) && count < 2;
}

function BackLink() {
  return (
    <Link
      href="/audits"
      className={buttonVariants({ variant: "outline" })}
      data-testid="audit-period-back"
    >
      All audit periods
    </Link>
  );
}

export default function AuditPeriodDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [confirming, setConfirming] = useState(false);

  const periodQ = useQuery<AuditPeriodResponse>({
    queryKey: ["audits", id],
    queryFn: () => fetchAuditPeriod(id),
    retry: noRetryOn([400, 401, 404]),
  });

  // The single-period read does not join the catalog; the list does, so
  // the readable framework label comes from the list (usually cached
  // from /audits already).
  const listQ = useQuery({
    queryKey: ["audits", "list"],
    queryFn: fetchAuditPeriods,
  });

  const freeze = useMutation({
    mutationFn: () => freezeAuditPeriod(id),
    onSuccess: (resp) => {
      queryClient.setQueryData(["audits", id], resp);
      void queryClient.invalidateQueries({ queryKey: ["audits", "list"] });
      setConfirming(false);
      toast("Audit period frozen.");
    },
    onError: (err) => {
      setConfirming(false);
      toast(freezeErrorMessage(err));
    },
  });

  const error = periodQ.error;
  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/audits/${encodeURIComponent(id)}`);
    }
  }, [error, id, router]);

  if (
    error instanceof APIError &&
    (error.status === 404 || error.status === 400)
  ) {
    notFound();
  }

  if (periodQ.isLoading) {
    return (
      <PageShell
        title={
          <Skeleton className="inline-block h-8 w-96 max-w-full align-middle" />
        }
        data-testid="audit-period-loading"
      >
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </PageShell>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <PageShell title="Audit period" actions={<BackLink />}>
        <Alert variant="destructive" data-testid="audit-period-error">
          <AlertTitle>Could not load the audit period</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  const fetched = periodQ.data?.audit_period;
  if (!fetched) return null;
  const label = listQ.data?.audit_periods.find((p) => p.id === fetched.id)
    ?.framework_label;
  const period: AuditPeriod = { framework_label: label, ...fetched };
  const frozen = isFrozen(period);
  const fw = frameworkVersionLabel(period);

  return (
    <DetailHeader
      data-testid="audit-period-detail"
      titleTestId="audit-period-name"
      headerTestId="audit-period-header"
      title={period.name}
      status={
        <span
          className={`inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-[11px] font-medium ${statusPillClass(
            period.status,
          )}`}
          data-testid="audit-period-status"
        >
          <span
            className={`h-1.5 w-1.5 rounded-full ${statusDotClass(
              period.status,
            )}`}
          />
          {period.status}
        </span>
      }
      subtitle={
        <p className="text-sm text-muted-foreground">
          <Term id="audit-period">Audit period</Term> for{" "}
          <span
            className={fw.readable ? "" : "font-mono text-xs"}
            title={period.framework_version_id}
            data-testid="audit-period-framework"
          >
            {fw.text}
          </span>{" "}
          ·{" "}
          <span className="font-mono text-xs" data-testid="audit-period-dates">
            {periodRangeLabel(period)}
          </span>
        </p>
      }
      back={<BackLink />}
      primaryAction={
        frozen
          ? undefined
          : {
              label: freeze.isPending ? "Freezing…" : "Freeze",
              onClick: () => setConfirming(true),
              disabled: confirming || freeze.isPending,
              "data-testid": "audit-period-freeze",
            }
      }
    >
      {!frozen && confirming ? (
        <ConfirmInline
          prompt={FREEZE_CONFIRM_PROMPT}
          confirmLabel="Freeze period"
          ariaLabel="Confirm: freeze audit period"
          busy={freeze.isPending}
          onCancel={() => setConfirming(false)}
          onConfirm={() => freeze.mutate()}
          data-testid="audit-period-freeze-confirm"
        >
          <span className="text-muted-foreground">{FREEZE_CONFIRM_DETAIL}</span>
        </ConfirmInline>
      ) : null}
      {frozen ? <FreezeCard period={period} /> : null}
      <AuditorsCard periodId={period.id} />
      <ControlsCard period={period} />
    </DetailHeader>
  );
}

function FreezeCard({ period }: { period: AuditPeriod }) {
  return (
    <Card data-testid="audit-period-freeze-card">
      <CardHeader>
        <CardTitle>
          <Term id="freeze">Frozen</Term>
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4">
        <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-[10rem_1fr]">
          <dt className="text-muted-foreground">Frozen at</dt>
          <dd
            className="font-mono text-xs"
            data-testid="audit-period-frozen-at"
          >
            {period.frozen_at ?? "—"}
          </dd>
          <dt className="text-muted-foreground">Frozen by</dt>
          <dd data-testid="audit-period-frozen-by">
            <Person id={period.frozen_by} />
          </dd>
          <dt className="text-muted-foreground">Freeze hash</dt>
          <dd
            className="font-mono text-xs break-all"
            data-testid="audit-period-frozen-hash"
          >
            {period.frozen_hash ?? "—"}
          </dd>
        </dl>
        <OscalExportButton period={period} />
      </CardContent>
    </Card>
  );
}

function OscalExportButton({ period }: { period: AuditPeriod }) {
  const { toast } = useToast();
  const [failure, setFailure] = useState<string | null>(null);
  const exportQ = useMutation({
    mutationFn: () => fetchOscalBundle(period.id),
    onSuccess: (blob) => {
      setFailure(null);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = oscalExportDownloadFilename(period.id, period.frozen_at);
      document.body.appendChild(a);
      a.click();
      a.remove();
      // Let the click's download start before the URL goes away.
      setTimeout(() => URL.revokeObjectURL(url), 0);
    },
    onError: (err) => {
      const msg = oscalExportErrorMessage(err);
      setFailure(msg);
      toast(msg);
    },
  });
  return (
    <div className="flex flex-col items-start gap-2">
      <Button
        type="button"
        variant="outline"
        onClick={() => exportQ.mutate()}
        disabled={exportQ.isPending}
        data-testid="audit-period-oscal-export"
      >
        {exportQ.isPending ? "Exporting…" : OSCAL_EXPORT_DOWNLOAD_LABEL}
      </Button>
      {failure ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="audit-period-oscal-export-unavailable"
        >
          {failure}
        </p>
      ) : null}
    </div>
  );
}

function AuditorsCard({ periodId }: { periodId: string }) {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [picked, setPicked] = useState("");
  const queryKey = ["audits", periodId, "auditors"];
  const auditorsQ = useQuery({
    queryKey,
    queryFn: () => fetchPeriodAuditors(periodId),
    retry: noRetryOn([401, 403, 404]),
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey });
  const assign = useMutation({
    mutationFn: (userID: string) => assignPeriodAuditor(periodId, userID),
    onSuccess: () => {
      setPicked("");
      void refresh();
      toast("Auditor assigned.");
    },
    onError: (err) => toast(auditorChangeErrorMessage(err, "assign")),
  });
  const remove = useMutation({
    mutationFn: (userID: string) => removePeriodAuditor(periodId, userID),
    onSuccess: () => {
      void refresh();
      toast("Auditor removed.");
    },
    onError: (err) => {
      void refresh();
      toast(auditorChangeErrorMessage(err, "remove"));
    },
  });

  const refused =
    auditorsQ.error instanceof APIError && auditorsQ.error.status === 403;
  const auditors = auditorsQ.data?.auditors ?? [];
  const candidates = auditorsQ.data?.candidates ?? [];

  return (
    <Card data-testid="audit-period-auditors">
      <CardHeader>
        <CardTitle>Auditors</CardTitle>
        <p className="text-sm text-muted-foreground">
          Assigned auditors open this period in the audit workspace. The
          assignment covers this period only.
        </p>
      </CardHeader>
      <CardContent className="grid gap-3">
        {auditorsQ.isLoading ? <Skeleton className="h-16 w-full" /> : null}
        {refused ? (
          <p
            className="text-sm text-muted-foreground"
            data-testid="audit-period-auditors-refused"
          >
            Only an admin can see and change this period&apos;s auditors.
          </p>
        ) : auditorsQ.isError ? (
          <Alert
            variant="destructive"
            data-testid="audit-period-auditors-error"
          >
            <AlertTitle>Could not load the auditors</AlertTitle>
            <AlertDescription>
              {(auditorsQ.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : null}
        {auditorsQ.isSuccess ? (
          <>
            {auditors.length === 0 ? (
              <p
                className="text-sm text-muted-foreground"
                data-testid="audit-period-auditors-empty"
              >
                No auditors assigned yet.
              </p>
            ) : (
              <ul className="grid gap-2">
                {auditors.map((a) => (
                  <li
                    key={a.user_id}
                    className="flex items-center justify-between gap-3 text-sm"
                    data-testid="audit-period-auditor"
                    data-user-id={a.user_id}
                  >
                    <span>{auditorLabel(a)}</span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={remove.isPending}
                      onClick={() => remove.mutate(a.user_id)}
                      aria-label={`Remove ${auditorLabel(a)}`}
                      data-testid="audit-period-auditor-remove"
                    >
                      Remove
                    </Button>
                  </li>
                ))}
              </ul>
            )}
            {candidates.length > 0 ? (
              <form
                className="flex flex-col gap-2 sm:flex-row sm:items-end"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (picked) assign.mutate(picked);
                }}
              >
                <div className="grid flex-1 gap-1">
                  <label
                    htmlFor="audit-period-auditor-picker"
                    className="text-xs text-muted-foreground"
                  >
                    Assign an auditor
                  </label>
                  <Select
                    id="audit-period-auditor-picker"
                    value={picked}
                    onChange={(e) => setPicked(e.target.value)}
                    data-testid="audit-period-auditor-picker"
                  >
                    <option value="">Choose an auditor…</option>
                    {candidates.map((c) => (
                      <option key={c.user_id} value={c.user_id}>
                        {auditorLabel(c)}
                      </option>
                    ))}
                  </Select>
                </div>
                <Button
                  type="submit"
                  disabled={!picked || assign.isPending}
                  data-testid="audit-period-auditor-assign"
                >
                  {assign.isPending ? "Assigning…" : "Assign"}
                </Button>
              </form>
            ) : (
              <p
                className="text-sm text-muted-foreground"
                data-testid="audit-period-auditors-no-candidates"
              >
                No other users hold the auditor role. Give a user the auditor
                role in Admin, Users to assign them here.
              </p>
            )}
          </>
        ) : null}
      </CardContent>
    </Card>
  );
}

function ControlsCard({ period }: { period: AuditPeriod }) {
  const [shown, setShown] = useState(CONTROLS_PAGE);
  const [openId, setOpenId] = useState<string | null>(null);
  const controlsQ = useQuery({
    queryKey: ["controls-list", "tenant"],
    queryFn: fetchTenantControlsList,
  });
  const workspacePeriod = useMemo(() => toWorkspacePeriod(period), [period]);
  const controls = controlsQ.data ?? [];
  const visible = controls.slice(0, shown);

  return (
    <Card data-testid="audit-period-controls">
      <CardHeader>
        <CardTitle>Controls</CardTitle>
        <p className="text-sm text-muted-foreground">
          Every control in the tenant, not only those in this period&apos;s
          framework.{" "}
          {isFrozen(period)
            ? "Evidence observed up to the freeze."
            : "Evidence observed so far; the horizon moves until the period is frozen."}{" "}
          Open a control for its populations, samples, walkthroughs and audit
          notes.
        </p>
      </CardHeader>
      <CardContent className="grid gap-3">
        {controlsQ.isLoading ? <Skeleton className="h-32 w-full" /> : null}
        {controlsQ.isError ? (
          <Alert
            variant="destructive"
            data-testid="audit-period-controls-error"
          >
            <AlertTitle>Could not load controls</AlertTitle>
            <AlertDescription>
              {(controlsQ.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : null}
        {controlsQ.isSuccess && controls.length === 0 ? (
          <p
            className="text-sm text-muted-foreground"
            data-testid="audit-period-controls-empty"
          >
            No controls yet. Add controls to see their evidence in this period.
          </p>
        ) : null}
        {visible.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-muted-foreground">
                <tr>
                  <th className="py-2 pr-3 font-medium">Control</th>
                  <th className="py-2 pr-3 font-medium">Observations</th>
                  <th className="py-2 pr-3 font-medium">Latest result</th>
                  <th className="py-2 pr-3 font-medium">Latest observed</th>
                  <th className="py-2 font-medium">
                    <span className="sr-only">Open</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {visible.map((c) => (
                  <ControlStateRow
                    key={c.id}
                    periodId={period.id}
                    control={c}
                    open={openId === c.id}
                    onToggle={() => setOpenId(openId === c.id ? null : c.id)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {controls.length > shown ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="justify-self-start"
            onClick={() => setShown(shown + CONTROLS_PAGE)}
            data-testid="audit-period-controls-more"
          >
            Show more ({controls.length - shown} left)
          </Button>
        ) : null}
        {openId ? (
          <div
            className="rounded-lg border p-4"
            data-testid="audit-period-control-workspace"
          >
            <AnnotationDraftProvider>
              <ControlWorkspace controlId={openId} period={workspacePeriod} />
            </AnnotationDraftProvider>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function ControlStateRow({
  periodId,
  control,
  open,
  onToggle,
}: {
  periodId: string;
  control: TenantControl;
  open: boolean;
  onToggle: () => void;
}) {
  const stateQ = useQuery({
    queryKey: ["audits", periodId, "control-state", control.id],
    queryFn: () => fetchPeriodControlState(periodId, control.id),
    retry: noRetryOn([401, 403, 404]),
  });
  const summary = stateQ.data ? summarizeControlState(stateQ.data) : null;
  const refused =
    stateQ.error instanceof APIError && stateQ.error.status === 403;

  return (
    <tr
      className="border-t align-top"
      data-testid="audit-period-control-row"
      data-control-id={control.id}
    >
      <td className="py-2 pr-3">
        <span className="font-mono text-xs text-muted-foreground">
          {control.scf_id}
        </span>{" "}
        <span className="text-foreground">{control.title}</span>
      </td>
      <td className="py-2 pr-3 font-mono text-xs">
        {stateQ.isLoading ? (
          <Skeleton className="h-4 w-8" />
        ) : (
          <span data-testid="audit-period-control-count">
            {summary ? summary.count : "—"}
          </span>
        )}
      </td>
      <td className="py-2 pr-3" data-testid="audit-period-control-result">
        {summary?.latestResult ? (
          <Badge variant={resultBadgeVariant(summary.latestResult)}>
            {summary.latestResult}
          </Badge>
        ) : stateQ.isError ? (
          <span className="text-xs text-muted-foreground">
            {refused ? "Not visible to your role" : "Could not load"}
          </span>
        ) : stateQ.isSuccess ? (
          <span className="text-xs text-muted-foreground">No evidence</span>
        ) : null}
      </td>
      <td className="py-2 pr-3 font-mono text-xs text-muted-foreground">
        {summary?.latestObservedAt?.slice(0, 10) ?? ""}
      </td>
      <td className="py-2 text-right">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          aria-expanded={open}
          onClick={onToggle}
          data-testid="audit-period-control-open"
        >
          {open ? "Close" : "Open"}
        </Button>
      </td>
    </tr>
  );
}
