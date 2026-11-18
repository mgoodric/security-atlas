"use client";

// #2119 — /approvals: what is waiting for the caller's approval, grouped
// by type (exceptions, risk acceptances, policies, framework scopes).
// Each row links to the record's approval action, where the approve
// itself happens and the platform applies its gate again.
//
// The list is the platform's (GET /v1/approvals via /api/approvals): it
// holds exactly what each type's approve handler would accept from the
// caller, so this page never decides who may approve. The caller's own
// requests appear only when the organisation allows self-approval, and
// are marked. The page shares its query with the sidebar badge
// (approvalsQuery, keyed by tenant) and refetches on every visit, so returning here
// after an approval shows the list and the badge one shorter.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Person } from "@/components/ui/person";
import { Timestamp } from "@/components/ui/timestamp";
import {
  approvalsQuery,
  approvalHref,
  fetchApprovals,
  groupApprovals,
  type ApprovalsResponse,
} from "@/lib/api/approvals";
import { useSessionTenant } from "@/components/session-tenant-context";
import { pageTitle } from "@/lib/page-titles";
import { Term } from "@/components/help/term";

export default function ApprovalsPage() {
  const tenantId = useSessionTenant();
  const q = useQuery<ApprovalsResponse>({
    ...approvalsQuery(tenantId),
    queryFn: fetchApprovals,
    refetchOnMount: "always",
    retry: false,
  });

  return (
    <PageShell
      title={pageTitle("/approvals")}
      subtitle={
        <>
          Records waiting for your approval as an{" "}
          <Term id="approver">approver</Term>: requested exceptions, proposed
          risk acceptances, policies under review and framework scopes in
          review. Each needs a{" "}
          <Term id="second-person-rule">second person</Term>, not the one who
          asked. Open one to approve it.
        </>
      }
      bodyClassName="space-y-6"
    >
      {q.isPending ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="approvals-loading"
        >
          Loading…
        </p>
      ) : q.isError ? (
        <Alert variant="destructive" data-testid="approvals-error">
          <AlertTitle>Could not load approvals</AlertTitle>
          <AlertDescription>{(q.error as Error).message}</AlertDescription>
        </Alert>
      ) : q.data && q.data.total === 0 ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="approvals-empty"
        >
          Nothing is waiting for your approval.
        </p>
      ) : q.data ? (
        <ApprovalGroups data={q.data} />
      ) : null}
    </PageShell>
  );
}

function ApprovalGroups({ data }: { data: ApprovalsResponse }) {
  return (
    <>
      {groupApprovals(data.items).map((g) => (
        <section
          key={g.type}
          aria-labelledby={`approvals-group-${g.type}`}
          data-testid={`approvals-group-${g.type}`}
          className="space-y-2"
        >
          <h2
            id={`approvals-group-${g.type}`}
            className="text-sm font-semibold text-foreground"
          >
            {g.label}{" "}
            <span
              className="font-normal text-muted-foreground"
              data-testid={`approvals-count-${g.type}`}
            >
              {data.counts[g.type]}
            </span>
          </h2>
          <ul className="divide-y rounded-md border">
            {g.items.map((item) => (
              <li
                key={item.id}
                data-testid="approvals-row"
                data-type={item.type}
              >
                <Link
                  href={approvalHref(item)}
                  className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-sm hover:bg-foreground/5"
                  data-testid={`approvals-row-link-${item.id}`}
                >
                  <span className="font-medium text-foreground">
                    {item.title}
                  </span>
                  {item.self_approval ? (
                    <Badge
                      variant="outline"
                      data-testid="approvals-row-self"
                      title="Your own request. Your organisation allows self-approval; approving it records a self-approval."
                    >
                      Your request · self-approval
                    </Badge>
                  ) : null}
                  <span className="ml-auto text-xs text-muted-foreground">
                    <Person id={item.requested_by} />
                    {item.requested_at ? (
                      <>
                        {" · "}
                        <Timestamp value={item.requested_at} />
                      </>
                    ) : null}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
          {data.counts[g.type] > g.items.length ? (
            <p className="text-xs text-muted-foreground">
              Showing the oldest {g.items.length} of {data.counts[g.type]}.
            </p>
          ) : null}
        </section>
      ))}
    </>
  );
}
