"use client";

// #2113 — /exceptions/[id]: one exception (parent #2071, "finish the
// record lifecycles").
//
// The header (DetailHeader, #2102) carries the control, status and expiry,
// and the one lifecycle action for the state:
//
//   requested -> Approve (one click, confirmed inline); Deny is in the
//                overflow and opens its own route for the reason;
//   approved  -> Activate, its own route for the effective-from day;
//   active    -> Revoke, its own route for the reason;
//   denied / expired / revoked -> nothing; they are final.
//
// There is no Edit: an exception is never changed or renewed after it is
// requested (canvas §6.3); a new one is filed instead.
//
// Roles and the approval rule are the platform's: approver only, never
// the requester unless the organisation allows self-approval (#2101,
// #2231). The page offers each action and shows the platform's refusal
// as a toast; it never widens or narrows that decision itself.
//
// The scope is shown with the scope picker read-only. The justification
// renders through the shared Markdown component (#2094).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect, useRef, useState } from "react";

import { DetailHeader } from "@/components/detail/detail-header";
import { Markdown } from "@/components/markdown";
import { ScopePicker } from "@/components/scope/scope-picker";
import { SelfApprovedBadge } from "@/components/self-approved-badge";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Person } from "@/components/ui/person";
import { ConfirmInline } from "@/components/ui/row-actions";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { invalidateApprovals } from "@/lib/api/approvals";
import { APIError } from "@/lib/api/base";
import { fetchTenantControlsList } from "@/lib/api/controls-list";
import {
  fetchException,
  fetchExceptionAuditLog,
  transitionException,
  type Exception,
} from "@/lib/api/exceptions";

import { nextAction, statusPillClass } from "../lifecycle";

function noRetryOn(statuses: number[]) {
  return (count: number, err: unknown) =>
    !(err instanceof APIError && statuses.includes(err.status)) && count < 2;
}

function day(iso?: string | null): string {
  return iso ? iso.slice(0, 10) : "—";
}

function BackLink() {
  return (
    <Link
      href="/exceptions"
      className={buttonVariants({ variant: "outline" })}
      data-testid="exception-back"
    >
      All exceptions
    </Link>
  );
}

export default function ExceptionDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [confirming, setConfirming] = useState(false);
  const approveRef = useRef<HTMLButtonElement>(null);

  const exQ = useQuery({
    queryKey: ["exceptions", id],
    queryFn: () => fetchException(id),
    retry: noRetryOn([400, 401, 404]),
  });
  const controlsQ = useQuery({
    queryKey: ["controls-list", "tenant"],
    queryFn: fetchTenantControlsList,
    staleTime: 30_000,
  });

  const approve = useMutation({
    mutationFn: () => transitionException(id, "approve"),
    onSuccess: (resp) => {
      qc.setQueryData(["exceptions", id], resp);
      void qc.invalidateQueries({ queryKey: ["exceptions"] });
      void invalidateApprovals(qc);
      setConfirming(false);
      toast("Exception approved");
    },
    onError: (err) => {
      setConfirming(false);
      toast(`Could not approve: ${(err as Error).message}`);
    },
  });

  const error = exQ.error;
  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/exceptions/${encodeURIComponent(id)}`);
    }
  }, [error, id, router]);

  if (
    error instanceof APIError &&
    (error.status === 404 || error.status === 400)
  ) {
    notFound();
  }

  if (exQ.isLoading) {
    return (
      <PageShell
        title={
          <Skeleton className="inline-block h-8 w-96 max-w-full align-middle" />
        }
        data-testid="exception-loading"
      >
        <Skeleton className="h-48 w-full" />
      </PageShell>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <PageShell title="Exception" actions={<BackLink />}>
        <Alert variant="destructive" data-testid="exception-error">
          <AlertTitle>Could not load the exception</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  const ex = exQ.data?.exception;
  if (!ex) return null;
  const control = controlsQ.data?.find((c) => c.id === ex.control_id);
  const controlLabel = control
    ? `${control.scf_id} ${control.title}`
    : ex.control_id;
  const next = nextAction(ex.status);
  const base = `/exceptions/${encodeURIComponent(ex.id)}`;

  return (
    <DetailHeader
      data-testid="exception-detail"
      titleTestId="exception-title"
      headerTestId="exception-header"
      title={`Exception: ${controlLabel}`}
      status={
        <>
          <span
            className={`inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium ${statusPillClass(
              ex.status,
            )}`}
            data-testid="exception-status"
          >
            {ex.status}
          </span>
          <SelfApprovedBadge
            selfApproved={ex.self_approved}
            testId="exception-self-approved"
          />
        </>
      }
      subtitle={
        <p className="text-sm text-muted-foreground">
          Exception for{" "}
          <Link
            href={`/controls/${encodeURIComponent(ex.control_id)}`}
            className="underline-offset-2 hover:underline"
            data-testid="exception-control-link"
          >
            {controlLabel}
          </Link>{" "}
          · expires{" "}
          <span className="font-mono text-xs" data-testid="exception-expires">
            {day(ex.expires_at)}
          </span>
        </p>
      }
      back={<BackLink />}
      primaryActionRef={approveRef}
      primaryAction={
        next?.kind === "approve"
          ? {
              label: approve.isPending ? "Approving…" : "Approve",
              onClick: () => setConfirming(true),
              disabled: confirming || approve.isPending,
              "data-testid": "exception-approve",
            }
          : next?.kind === "link"
            ? {
                label: next.label,
                href: `${base}/${next.action}`,
                "data-testid": `exception-${next.action}`,
              }
            : undefined
      }
      overflow={
        ex.status === "requested"
          ? [
              {
                id: "deny",
                label: "Deny…",
                onSelect: () => router.push(`${base}/deny`),
                "data-testid": "exception-deny",
              },
            ]
          : []
      }
    >
      {next?.kind === "approve" && confirming ? (
        <ConfirmInline
          prompt="Approve this exception?"
          confirmLabel="Approve"
          ariaLabel="Confirm: approve exception"
          busy={approve.isPending}
          onCancel={() => setConfirming(false)}
          returnFocus={() => approveRef.current}
          onConfirm={() => approve.mutate()}
          data-testid="exception-approve-confirm"
        >
          <span className="text-muted-foreground">
            It still has to be activated before the control evaluates as
            excepted.
          </span>
        </ConfirmInline>
      ) : null}
      <DetailsCard ex={ex} />
      <ScopeCard ex={ex} />
      <AuditCard id={ex.id} />
    </DetailHeader>
  );
}

function DetailsCard({ ex }: { ex: Exception }) {
  // #2385: who acted renders through <Person>, never the raw ID.
  const by = (at: string, actor: string | null | undefined, key: string) => (
    <>
      {day(at)} by <Person id={actor} testId={`exception-${key}-by`} />
    </>
  );
  const rows: [string, React.ReactNode, string][] = [
    [
      "Requested",
      by(ex.requested_at, ex.requested_by, "requested"),
      "requested",
    ],
  ];
  if (ex.approved_at)
    rows.push([
      "Approved",
      by(ex.approved_at, ex.approved_by, "approved"),
      "approved",
    ]);
  if (ex.denied_at)
    rows.push(["Denied", by(ex.denied_at, ex.denied_by, "denied"), "denied"]);
  if (ex.activated_at)
    rows.push([
      "Activated",
      `${day(ex.activated_at)}, effective ${day(ex.effective_from)}`,
      "activated",
    ]);
  if (ex.revoked_at)
    rows.push([
      "Revoked",
      by(ex.revoked_at, ex.revoked_by, "revoked"),
      "revoked",
    ]);
  if (ex.expired_at) rows.push(["Expired", day(ex.expired_at), "expired"]);

  return (
    <Card data-testid="exception-details">
      <CardHeader>
        <CardTitle>Request</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4 text-sm">
        <div>
          <p className="text-xs font-medium text-muted-foreground">
            Justification
          </p>
          <Markdown
            source={ex.justification}
            data-testid="exception-justification"
          />
        </div>
        <div>
          <p className="text-xs font-medium text-muted-foreground">
            Compensating controls
          </p>
          {ex.compensating_controls.length > 0 ? (
            <ul className="list-disc pl-5" data-testid="exception-compensating">
              {ex.compensating_controls.map((c, i) => (
                <li key={i}>{c}</li>
              ))}
            </ul>
          ) : (
            <p className="text-muted-foreground">None given.</p>
          )}
        </div>
        <dl className="grid grid-cols-1 gap-2 sm:grid-cols-[8rem_1fr]">
          {rows.map(([label, value, key]) => (
            <div key={key} className="contents">
              <dt className="text-muted-foreground">{label}</dt>
              <dd data-testid={`exception-${key}-at`}>{value}</dd>
            </div>
          ))}
          <dt className="text-muted-foreground">Expires</dt>
          <dd className="font-mono text-xs">{day(ex.expires_at)}</dd>
        </dl>
      </CardContent>
    </Card>
  );
}

function ScopeCard({ ex }: { ex: Exception }) {
  return (
    <Card data-testid="exception-scope">
      <CardHeader>
        <CardTitle>Scope</CardTitle>
      </CardHeader>
      <CardContent>
        {/* key remounts the read-only picker if the record reloads. */}
        <ScopePicker
          key={JSON.stringify(ex.scope_cell_predicate)}
          value={ex.scope_cell_predicate}
          readOnly
        />
      </CardContent>
    </Card>
  );
}

function AuditCard({ id }: { id: string }) {
  const logQ = useQuery({
    queryKey: ["exceptions", id, "audit-log"],
    queryFn: () => fetchExceptionAuditLog(id),
  });
  const entries = logQ.data?.entries ?? [];
  return (
    <Card data-testid="exception-audit-log">
      <CardHeader>
        <CardTitle>History</CardTitle>
      </CardHeader>
      <CardContent>
        {logQ.isLoading ? <Skeleton className="h-16 w-full" /> : null}
        {logQ.isError ? (
          <p className="text-sm text-muted-foreground">
            Could not load the history: {(logQ.error as Error).message}
          </p>
        ) : null}
        {entries.length > 0 ? (
          <ol className="space-y-2 text-sm">
            {entries.map((e) => (
              <li key={e.id} data-testid="exception-audit-entry">
                <span className="font-mono text-xs text-muted-foreground">
                  {e.occurred_at.slice(0, 16).replace("T", " ")}
                </span>{" "}
                <span className="font-medium">{e.action}</span> by{" "}
                <Person id={e.actor} testId="exception-audit-actor" />
                {e.reason ? (
                  <span className="text-muted-foreground">: {e.reason}</span>
                ) : null}
              </li>
            ))}
          </ol>
        ) : null}
      </CardContent>
    </Card>
  );
}
