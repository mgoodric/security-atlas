"use client";

// Slice 672 — read-only policy detail view (`/policies/[id]`).
//
// Closes the ATLAS-024 audit finding: policy titles in `/policies`
// linked to `/policies/{id}`, but the route did not exist, so every
// click was a hard shell-less Next 404. JUDGMENT D1 (decisions log):
// BUILD the route rather than remove the link — the backend read API
// (`GET /v1/policies/{id}`) + seeded `body_md` content already exist, so
// a read-only detail page is the honest fix.
//
// Mirrors the controls/[id] + vendors/[id] precedent: a client page that
// fetches its own BFF (`/api/policies/{id}`) via TanStack Query, with a
// loading skeleton, a 401 -> /login redirect, a 404 -> in-shell
// `notFound()`, and a destructive Alert for any other error. The BFF is
// the only tenant context (cookie session -> upstream RLS, invariant
// #6); the page never passes a tenant_id.
//
// #2116 renders the page through the shared DetailHeader (parent #2071).
// Its actions follow the workflow: a draft has Edit, Submit for review
// and Discard (overflow, confirmed inline); a policy under review has
// Approve; an approved one has Publish (its own form, for the effective
// and review dates); a published one has Create new version, which opens
// the create form as the draft's predecessor. One-click transitions
// confirm inline, then toast. The platform enforces every rule: the
// approval rule (the submitter cannot approve unless the organisation
// allows self-approval, and then it is flagged), the draft-only edit and
// discard, and the publish block on a policy with no linked control.
// The PDF link (`/v1/policies/{id}/pdf`) is a same-origin link the
// reverse proxy routes to the platform's chromedp renderer.
//
// #2465 — a published policy on the caller's pending list (their role is
// in its audience and they have not acknowledged this version within the
// year) shows a "Your acknowledgment" card with an Acknowledge button. The
// card states what the click attests, so it records on one click. The
// platform admits the ack and records it for the caller only.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect, useRef, useState } from "react";

import { DetailHeader } from "@/components/detail/detail-header";
import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { SelfApprovedBadge } from "@/components/self-approved-badge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmInline } from "@/components/ui/row-actions";
import { Skeleton } from "@/components/ui/skeleton";
import { Timestamp } from "@/components/ui/timestamp";
import { useToast } from "@/components/ui/toast";
import { invalidateApprovals } from "@/lib/api/approvals";
import { APIError } from "@/lib/api/base";
import {
  acknowledgePolicy,
  discardPolicyDraft,
  fetchMyAcknowledgments,
  MY_ACKS_QUERY_KEY,
  fetchPolicyDetail,
  transitionPolicy,
} from "@/lib/api/policies";
import { Markdown } from "@/components/markdown";

import {
  acknowledgedToast,
  audienceLabel,
  isDraft,
  myAckState,
  needsControlsWarning,
  NO_CONTROLS_WARNING,
  primaryAction,
  unrecognisedAudiences,
} from "../lifecycle";

function statusPillVariant(
  status: string,
): "default" | "secondary" | "outline" {
  switch (status) {
    case "published":
      return "default";
    case "retired":
      return "outline";
    default:
      return "secondary";
  }
}

// #1747 — the import validates source_url as an absolute http(s) URL;
// re-check before rendering it as a link so no other scheme can reach an
// href.
function safeHttpUrl(raw: string | null | undefined): string | null {
  if (!raw) return null;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

export default function PolicyDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [confirming, setConfirming] = useState<"submit" | "approve" | null>(
    null,
  );
  const primaryRef = useRef<HTMLButtonElement>(null);

  const transition = useMutation({
    mutationFn: (t: "submit" | "approve") => transitionPolicy(id, t),
    onSuccess: async (_p, t) => {
      setConfirming(null);
      await qc.invalidateQueries({ queryKey: ["policy", id] });
      void qc.invalidateQueries({ queryKey: ["policies"] });
      void invalidateApprovals(qc);
      toast(t === "submit" ? "Policy submitted for review" : "Policy approved");
    },
    onError: (err, t) => {
      setConfirming(null);
      toast(
        `Could not ${t === "submit" ? "submit" : "approve"} policy: ${
          (err as Error).message
        }`,
      );
    },
  });
  const discard = useMutation({
    mutationFn: () => discardPolicyDraft(id),
    onSuccess: async () => {
      // The cached detail is left alone: refetching it now would 404 into
      // notFound() before the list loads.
      await qc.invalidateQueries({ queryKey: ["policies"] });
      toast("Draft discarded");
      router.push("/policies");
    },
    onError: (err) => {
      toast(`Could not discard draft: ${(err as Error).message}`);
    },
  });

  const acknowledge = useMutation({
    // The version id is the one the card shows, so a re-publish since the
    // page loaded is a 409, not an ack of a version never seen (#2668).
    mutationFn: (policyVersionId: string) =>
      acknowledgePolicy(id, policyVersionId),
    onSuccess: async (ack) => {
      await qc.invalidateQueries({ queryKey: MY_ACKS_QUERY_KEY });
      void qc.invalidateQueries({ queryKey: ["policy", id] });
      void qc.invalidateQueries({ queryKey: ["policies"] });
      toast(acknowledgedToast(ack));
    },
    onError: (err) => {
      // A 409 means the policy changed under the page; refetch so the card
      // shows what is in force now.
      void qc.invalidateQueries({ queryKey: MY_ACKS_QUERY_KEY });
      void qc.invalidateQueries({ queryKey: ["policy", id] });
      toast(`Could not acknowledge policy: ${(err as Error).message}`);
    },
  });
  // The caller's own pending acknowledgments. A failure here only hides
  // the card; the policy itself still renders.
  const myAcks = useQuery({
    queryKey: MY_ACKS_QUERY_KEY,
    queryFn: fetchMyAcknowledgments,
    retry: false,
  });

  const { data, isLoading, error } = useQuery({
    queryKey: ["policy", id],
    queryFn: () => fetchPolicyDetail(id),
    // A 404 (genuinely-missing id) is a terminal state — do not retry it.
    retry: (count, err) =>
      !(
        err instanceof APIError &&
        (err.status === 404 || err.status === 401)
      ) && count < 2,
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/policies/${id}`);
    }
  }, [error, id, router]);

  // 404 -> render the in-shell not-found boundary (AC-3). notFound()
  // throws during render; the nearest `(authed)/not-found.tsx` catches
  // it inside the authed layout shell, so the sidebar/nav stay present.
  if (error instanceof APIError && error.status === 404) {
    notFound();
  }

  if (isLoading) {
    return (
      <PageShell
        title={
          <Skeleton className="inline-block h-8 w-96 max-w-full align-middle" />
        }
        data-testid="policy-detail-loading"
      >
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </PageShell>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <PageShell title="Policy" actions={<BackLink />}>
        <Alert variant="destructive" data-testid="policy-detail-error">
          <AlertTitle>Could not load policy</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </PageShell>
    );
  }

  if (!data) {
    // 401 redirect in flight, or no data yet — render nothing.
    return null;
  }

  const { policy, ack_rate } = data;
  const sourceHref = safeHttpUrl(policy.source_url);
  const primary = primaryAction(policy);
  const draft = isDraft(policy);
  const busy = confirming !== null || transition.isPending || discard.isPending;
  const base = `/policies/${encodeURIComponent(id)}`;
  const ackState = myAckState(
    policy,
    myAcks.data?.pending,
    acknowledge.data,
    myAcks.data?.current,
  );

  return (
    <DetailHeader
      data-testid="policy-detail"
      titleTestId="policy-detail-title"
      back={<BackLink />}
      title={policy.title}
      edit={
        draft
          ? { href: `${base}/edit`, "data-testid": "policy-detail-edit" }
          : undefined
      }
      primaryActionRef={primaryRef}
      primaryAction={
        primary
          ? {
              label: primary.label,
              ...("href" in primary
                ? { href: primary.href }
                : {
                    onClick: () =>
                      setConfirming(primary.kind as "submit" | "approve"),
                  }),
              disabled: busy,
              "data-testid": `policy-action-${primary.kind}`,
            }
          : undefined
      }
      overflow={
        draft
          ? [
              {
                id: "discard",
                label: "Discard draft",
                destructive: true,
                confirmPrompt:
                  "Discard this draft? It is deleted; the discard is kept in the policy audit log.",
                confirmLabel: "Discard",
                disabledReason: busy
                  ? "Finish the open confirmation first."
                  : undefined,
                onSelect: () => discard.mutate(),
                "data-testid": "policy-action-discard",
              },
            ]
          : []
      }
      status={
        <div className="flex flex-wrap items-center gap-2">
          <Badge
            variant={statusPillVariant(policy.status)}
            data-testid="policy-detail-status"
            className="capitalize"
          >
            {policy.status.replace("_", " ")}
          </Badge>
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="policy-detail-version"
          >
            {policy.version}
          </span>
          {policy.imported ? (
            <Badge variant="outline" data-testid="policy-detail-imported">
              Imported
            </Badge>
          ) : null}
          <SelfApprovedBadge
            selfApproved={policy.self_approved}
            testId="policy-detail-self-approved"
          />
        </div>
      }
      subtitle={
        <div className="mt-1 space-y-1" data-testid="policy-detail-header">
          <dl className="flex flex-wrap items-center gap-x-6 gap-y-1 text-sm">
            <div>
              <dt className="inline text-muted-foreground">Owner role </dt>
              <dd
                className="inline text-foreground"
                data-testid="policy-detail-owner"
              >
                {policy.owner_role}
              </dd>
            </div>
            {policy.acknowledgment_required_roles.length > 0 ? (
              <div>
                {/* #2524 — who is asked to acknowledge. A free-text value
                    from before the audience enum is flagged: no one holds
                    it as a role. */}
                <dt className="inline text-muted-foreground">Audience </dt>
                <dd
                  className="inline text-foreground"
                  data-testid="policy-detail-audience"
                >
                  {policy.acknowledgment_required_roles.map((a, i) => (
                    <span key={a}>
                      {i > 0 ? ", " : null}
                      {audienceLabel(a)}
                      {unrecognisedAudiences([a]).length > 0 ? (
                        <span
                          className="text-xs text-destructive"
                          data-testid={`policy-detail-audience-unrecognised-${a}`}
                        >
                          {" "}
                          (unrecognised audience)
                        </span>
                      ) : null}
                    </span>
                  ))}
                </dd>
              </div>
            ) : null}
            <div>
              <dt className="inline text-muted-foreground">Effective </dt>
              <dd className="inline font-mono text-foreground">
                <Timestamp value={policy.effective_date} style="date" />
              </dd>
            </div>
            <div>
              <dt className="inline text-muted-foreground">Published </dt>
              <dd className="inline font-mono text-foreground">
                <Timestamp value={policy.published_at} style="date" />
              </dd>
            </div>
            {policy.next_review_at ? (
              <div>
                {/* #2009 — a superseded version keeps the date it was due,
                  as history; only the published version has a review next. */}
                <dt className="inline text-muted-foreground">
                  {policy.status === "superseded"
                    ? "Review was due "
                    : "Next review "}
                </dt>
                <dd
                  className="inline font-mono text-foreground"
                  data-testid="policy-detail-next-review"
                >
                  <Timestamp value={policy.next_review_at} style="date" />
                </dd>
              </div>
            ) : null}
          </dl>
          {policy.imported ? (
            // #1747 — the history this policy carried in on import. The
            // approver is the name recorded before the tool, not a user.
            <dl
              className="flex flex-wrap items-center gap-x-6 gap-y-1 text-sm"
              data-testid="policy-detail-import-history"
            >
              <div>
                <dt className="inline text-muted-foreground">Approved by </dt>
                <dd
                  className="inline text-foreground"
                  data-testid="policy-detail-historical-approver"
                >
                  {policy.historical_approver_name ?? "—"}
                </dd>
              </div>
              <div>
                <dt className="inline text-muted-foreground">Approved </dt>
                <dd
                  className="inline font-mono text-foreground"
                  data-testid="policy-detail-historical-approved-at"
                >
                  <Timestamp
                    value={policy.historical_approved_at}
                    style="date"
                  />
                </dd>
              </div>
              {sourceHref ? (
                <div>
                  <dt className="inline text-muted-foreground">Source </dt>
                  <dd className="inline">
                    <a
                      href={sourceHref}
                      className="text-primary hover:underline break-all"
                      target="_blank"
                      rel="noopener noreferrer"
                      data-testid="policy-detail-source-link"
                    >
                      {sourceHref}
                    </a>
                  </dd>
                </div>
              ) : null}
            </dl>
          ) : null}
        </div>
      }
    >
      {confirming ? (
        <ConfirmInline
          prompt={
            confirming === "submit"
              ? "Submit this draft for review? It can no longer be edited; an approver approves it next."
              : "Approve this policy? It can then be published."
          }
          confirmLabel={confirming === "submit" ? "Submit" : "Approve"}
          busy={transition.isPending}
          onCancel={() => setConfirming(null)}
          returnFocus={() => primaryRef.current}
          onConfirm={() => transition.mutate(confirming)}
          data-testid="policy-action-confirm"
        />
      ) : null}

      {needsControlsWarning(policy) ? (
        <Alert data-testid="policy-detail-no-controls-warning">
          <AlertTitle>No linked controls</AlertTitle>
          <AlertDescription>{NO_CONTROLS_WARNING}</AlertDescription>
        </Alert>
      ) : null}

      {policy.predecessor_id ? (
        <p className="text-sm text-muted-foreground">
          Revises{" "}
          <Link
            href={`/policies/${encodeURIComponent(policy.predecessor_id)}`}
            className="text-primary hover:underline"
            data-testid="policy-detail-predecessor"
          >
            the previous version
          </Link>
          .
        </p>
      ) : null}

      <p className="text-sm" data-testid="policy-detail-linked-controls">
        <span className="text-muted-foreground">Linked controls </span>
        {policy.linked_control_ids.length === 0
          ? "none"
          : policy.linked_control_ids.map((cid, i) => (
              <span key={cid}>
                {i > 0 ? ", " : ""}
                <Link
                  href={`/controls/${encodeURIComponent(cid)}`}
                  className="font-mono text-xs text-primary hover:underline"
                >
                  {cid}
                </Link>
              </span>
            ))}
      </p>

      {ackState ? (
        <Card size="sm" data-testid="policy-detail-my-ack">
          <CardContent className="flex flex-wrap items-center justify-between gap-3">
            {ackState.kind === "pending" ? (
              <>
                <div className="text-sm">
                  <div className="font-medium">
                    Your acknowledgment is needed
                  </div>
                  <div className="text-muted-foreground">
                    Acknowledging confirms you have read version{" "}
                    {policy.version} of this policy.
                    {ackState.lastAcknowledgedAt ? (
                      <>
                        {" "}
                        You last acknowledged it on{" "}
                        <Timestamp
                          value={ackState.lastAcknowledgedAt}
                          style="date"
                        />
                        ; that no longer counts.
                      </>
                    ) : null}
                  </div>
                </div>
                <Button
                  onClick={() => acknowledge.mutate(ackState.policyVersionId)}
                  disabled={acknowledge.isPending}
                  data-testid="policy-action-acknowledge"
                >
                  Acknowledge
                </Button>
              </>
            ) : (
              <div className="text-sm" data-testid="policy-detail-my-ack-done">
                {ackState.version
                  ? `You acknowledged version ${ackState.version} on `
                  : "You acknowledged this policy on "}
                <Timestamp value={ackState.acknowledgedAt} style="date" />.
              </div>
            )}
          </CardContent>
        </Card>
      ) : null}

      {/* ============ ACK RATE (published only) ============ */}
      {ack_rate ? (
        <Card size="sm" data-testid="policy-detail-ack-rate">
          <CardContent>
            <div className="text-[11px] uppercase tracking-wider text-muted-foreground">
              Acknowledgment
            </div>
            <div className="mt-1 text-2xl font-semibold">
              {ack_rate.percent == null
                ? "—"
                : `${ack_rate.percent.toFixed(0)}%`}
            </div>
            <div className="mt-0.5 text-xs text-muted-foreground">
              {ack_rate.denominator === 0
                ? "no required-role users"
                : `${ack_rate.numerator} of ${ack_rate.denominator} acknowledged`}
            </div>
          </CardContent>
        </Card>
      ) : null}

      {/* ============ BODY (markdown) ============ */}
      <Card data-testid="policy-detail-body-card">
        <CardHeader className="border-b">
          <CardTitle>Policy text</CardTitle>
        </CardHeader>
        <CardContent>
          {policy.body_md.trim() ? (
            <Markdown
              source={policy.body_md}
              data-testid="policy-detail-body"
            />
          ) : (
            <p
              className="text-sm text-muted-foreground"
              data-testid="policy-detail-body-empty"
            >
              This policy has no body text.
            </p>
          )}
        </CardContent>
      </Card>

      {/* ============ PDF LINK ============ */}
      <div data-testid="policy-detail-pdf-link">
        <Link
          href={`/v1/policies/${encodeURIComponent(id)}/pdf`}
          className="text-sm text-primary hover:underline"
          target="_blank"
          rel="noopener noreferrer"
        >
          Download as PDF →
        </Link>
      </div>
    </DetailHeader>
  );
}

function BackLink() {
  return (
    <div className="text-sm">
      <Link
        href="/policies"
        className="text-muted-foreground hover:underline"
        data-testid="policy-detail-back"
      >
        ← Policy library
      </Link>
    </div>
  );
}
