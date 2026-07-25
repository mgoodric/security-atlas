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
// Read-only (slice 672 anti-criteria): NO submit / approve / publish /
// edit affordances. The PDF link (`/v1/policies/{id}/pdf`) is the one
// outbound action — a same-origin link the reverse proxy routes to the
// platform's chromedp renderer (D5 in the decisions log).

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { APIError } from "@/lib/api/base";
import { fetchPolicyDetail } from "@/lib/api/policies";
import { renderMarkdown } from "@/lib/markdown";

function statusPillVariant(
  status: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "published":
      return "default";
    case "retired":
      return "destructive";
    default:
      return "secondary";
  }
}

function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—";
  return iso.slice(0, 10);
}

export default function PolicyDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();

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
      <div className="space-y-6" data-testid="policy-detail-loading">
        <Skeleton className="h-10 w-2/3" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  if (error && !(error instanceof APIError && error.status === 401)) {
    return (
      <div className="space-y-6">
        <BackLink />
        <Alert variant="destructive" data-testid="policy-detail-error">
          <AlertTitle>Could not load policy</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </div>
    );
  }

  if (!data) {
    // 401 redirect in flight, or no data yet — render nothing.
    return null;
  }

  const { policy, ack_rate } = data;
  const bodyHtml = renderMarkdown(policy.body_md);

  return (
    <div className="space-y-6" data-testid="policy-detail">
      <BackLink />

      {/* ============ HEADER ============ */}
      <header className="space-y-3" data-testid="policy-detail-header">
        <div className="flex flex-wrap items-center gap-2">
          <Badge
            variant={statusPillVariant(policy.status)}
            data-testid="policy-detail-status"
            className="capitalize"
          >
            {policy.status}
          </Badge>
          <span
            className="font-mono text-xs text-muted-foreground"
            data-testid="policy-detail-version"
          >
            {policy.version}
          </span>
        </div>
        <h1
          className="text-2xl font-semibold tracking-tight"
          data-testid="policy-detail-title"
        >
          {policy.title}
        </h1>
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
          <div>
            <dt className="inline text-muted-foreground">Effective </dt>
            <dd className="inline font-mono text-foreground">
              {formatDate(policy.effective_date)}
            </dd>
          </div>
          <div>
            <dt className="inline text-muted-foreground">Published </dt>
            <dd className="inline font-mono text-foreground">
              {formatDate(policy.published_at)}
            </dd>
          </div>
        </dl>
      </header>

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
          {bodyHtml ? (
            <div
              className="prose prose-sm max-w-none break-words"
              data-testid="policy-detail-body"
              // Safe: renderMarkdown HTML-escapes the input before any
              // transform and only emits a fixed grammar of safe tags
              // (see web/lib/markdown.ts). No raw body_md byte reaches
              // the DOM unescaped.
              dangerouslySetInnerHTML={{ __html: bodyHtml }}
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
    </div>
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
