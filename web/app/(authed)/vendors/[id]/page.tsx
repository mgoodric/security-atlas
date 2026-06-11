"use client";

// Slice 686 — read-only vendor detail view (`/vendors/[id]`).
//
// Before this slice, landing on a vendor dropped the operator straight
// into the editable `<VendorForm>` (slice 679 left the edit form as the
// sole view). This page splits the read path from the edit path: the
// canonical `/vendors/[id]` route now renders a read-only summary, and the
// edit form moved to `/vendors/[id]/edit`. JUDGMENT D1 (decisions log):
// `[id]` = read-only + `[id]/edit` = form, matching every other detail
// page in the app (risks/[id] slice 681, policies/[id] slice 672,
// controls/[id]) — NOT a view/edit toggle.
//
// Mirrors the slice 681 risks/[id] precedent: a client page that fetches
// its own BFF (`/api/vendors/{id}`) via TanStack Query, with a loading
// skeleton, a 401 -> /login redirect, a 404 -> in-shell notFound(), and a
// destructive Alert for any other error. The BFF carries the only tenant
// context (cookie session -> upstream RLS, invariant #6); the page never
// passes a tenant_id, and it reuses the existing getVendor GET — NO second
// wire surface (slice 686 anti-criterion).
//
// AC-4 (review history): v1 has only the `last_review_date` scalar on the
// vendor row — there is no per-review ledger to render a history from. The
// detail surfaces the scalar honestly ("Last review"); a true history
// needs a `vendor_reviews` ledger, filed as a follow-on slice (decisions
// log D3). No migration here (anti-criterion).

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { notFound, useRouter } from "next/navigation";
import { use, useEffect } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { APIError } from "@/lib/api/base";
import { Vendor } from "@/lib/api/vendors";

import { dpaStatusLabel, formatDetailDate, ownerMailto } from "./detail-view";

async function fetchVendor(id: string): Promise<Vendor> {
  const res = await fetch(`/api/vendors/${encodeURIComponent(id)}`);
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as { vendor: Vendor };
  return body.vendor;
}

export default function VendorDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();

  const { data, isLoading, error } = useQuery({
    queryKey: ["vendor", id],
    queryFn: () => fetchVendor(id),
    // A 404 (genuinely-missing / cross-tenant id) and a 401 are terminal
    // states — do not retry them.
    retry: (count, err) =>
      !(
        err instanceof APIError &&
        (err.status === 404 || err.status === 401)
      ) && count < 2,
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/vendors/${id}`);
    }
  }, [error, id, router]);

  // 404 -> in-shell not-found boundary (the nearest (authed)/not-found.tsx
  // catches it inside the authed layout shell, so nav stays present).
  if (error instanceof APIError && error.status === 404) {
    notFound();
  }

  if (isLoading) {
    return (
      <div className="space-y-6" data-testid="vendor-detail-loading">
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
        <Alert variant="destructive" data-testid="vendor-detail-error">
          <AlertTitle>Could not load vendor</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      </div>
    );
  }

  if (!data) {
    // 401 redirect in flight, or no data yet — render nothing.
    return null;
  }

  const vendor = data;
  const mailto = ownerMailto(vendor.owner_user);

  return (
    <div className="space-y-6" data-testid="vendor-detail">
      <BackLink />

      {/* ============ HEADER ============ */}
      <header
        className="flex flex-wrap items-start justify-between gap-4"
        data-testid="vendor-detail-header"
      >
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <CriticalityBadge value={vendor.criticality} />
            {vendor.overdue ? (
              <Badge variant="destructive" data-testid="vendor-detail-status">
                overdue
              </Badge>
            ) : (
              <Badge variant="secondary" data-testid="vendor-detail-status">
                on time
              </Badge>
            )}
          </div>
          <h1
            className="text-2xl font-semibold tracking-tight"
            data-testid="vendor-detail-name"
          >
            {vendor.name}
          </h1>
          {vendor.domain ? (
            <p
              className="text-sm text-muted-foreground"
              data-testid="vendor-detail-domain"
            >
              {vendor.domain}
            </p>
          ) : null}
        </div>
        <Link
          href={`/vendors/${id}/edit`}
          className={buttonVariants()}
          data-testid="vendor-detail-edit"
        >
          Edit
        </Link>
      </header>

      {/* ============ SUMMARY ============ */}
      <Card data-testid="vendor-detail-summary-card">
        <CardHeader className="border-b">
          <CardTitle>Vendor summary</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2">
            <Field label="Owner" testid="vendor-detail-owner">
              {mailto ? (
                <a
                  href={mailto}
                  className="text-primary hover:underline"
                  data-testid="vendor-detail-owner-mailto"
                >
                  {vendor.owner_user.trim()}
                </a>
              ) : (
                <span>{vendor.owner_user.trim() || "unassigned"}</span>
              )}
            </Field>
            <Field label="Criticality" testid="vendor-detail-criticality">
              <span className="capitalize">{vendor.criticality}</span>
            </Field>
            <Field label="Review cadence" testid="vendor-detail-cadence">
              <span className="capitalize">{vendor.review_cadence}</span>
            </Field>
            <Field label="Last review" testid="vendor-detail-last-review">
              <span className="font-mono">
                {vendor.last_review_date
                  ? formatDetailDate(vendor.last_review_date)
                  : "never"}
              </span>
            </Field>
            <Field label="Contract start" testid="vendor-detail-contract-start">
              <span className="font-mono">
                {formatDetailDate(vendor.contract_start)}
              </span>
            </Field>
            <Field label="Contract end" testid="vendor-detail-contract-end">
              <span className="font-mono">
                {formatDetailDate(vendor.contract_end)}
              </span>
            </Field>
            <Field label="DPA status" testid="vendor-detail-dpa">
              <span>
                {dpaStatusLabel(vendor.dpa_signed, vendor.dpa_signed_at)}
              </span>
            </Field>
            {vendor.linked_sow_uri ? (
              <Field label="Linked SOW" testid="vendor-detail-sow">
                <span className="break-all font-mono text-xs">
                  {vendor.linked_sow_uri}
                </span>
              </Field>
            ) : null}
          </dl>
        </CardContent>
      </Card>

      {/* ============ NOTES ============ */}
      <Card data-testid="vendor-detail-notes-card">
        <CardHeader className="border-b">
          <CardTitle>Notes</CardTitle>
        </CardHeader>
        <CardContent>
          {vendor.notes.trim() ? (
            <p
              className="whitespace-pre-wrap break-words text-sm text-foreground"
              data-testid="vendor-detail-notes"
            >
              {vendor.notes}
            </p>
          ) : (
            <p
              className="text-sm text-muted-foreground"
              data-testid="vendor-detail-notes-empty"
            >
              This vendor has no notes.
            </p>
          )}
        </CardContent>
      </Card>

      {/* ============ REVIEW HISTORY (AC-4) ============ */}
      {/* v1 carries only the `last_review_date` scalar — there is no
          per-review ledger to render a history from. The summary above
          surfaces the scalar; a true history needs a `vendor_reviews`
          ledger, filed as a follow-on slice (decisions log D3). This
          honest placeholder names the gap rather than faking a timeline. */}
      <Card data-testid="vendor-detail-review-history-card">
        <CardHeader className="border-b">
          <CardTitle>Review history</CardTitle>
        </CardHeader>
        <CardContent>
          <p
            className="text-sm text-muted-foreground"
            data-testid="vendor-detail-review-history-scalar"
          >
            v1 records a single last-review date
            {vendor.last_review_date ? (
              <>
                {" "}
                (
                <span className="font-mono">
                  {formatDetailDate(vendor.last_review_date)}
                </span>
                ).
              </>
            ) : (
              <> — no review recorded yet.</>
            )}{" "}
            A per-review timeline arrives with the vendor-review ledger.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

function BackLink() {
  return (
    <div className="text-sm">
      <Link
        href="/vendors"
        className="text-muted-foreground hover:underline"
        data-testid="vendor-detail-back"
      >
        ← Vendor register
      </Link>
    </div>
  );
}

function CriticalityBadge({ value }: { value: string }) {
  const variant =
    value === "high"
      ? "destructive"
      : value === "medium"
        ? "secondary"
        : "outline";
  return (
    <Badge variant={variant} data-testid="vendor-detail-criticality-badge">
      {value}
    </Badge>
  );
}

function Field({
  label,
  children,
  testid,
}: {
  label: string;
  children: React.ReactNode;
  testid: string;
}) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-muted-foreground">
        {label}
      </dt>
      <dd className="mt-0.5 text-sm text-foreground" data-testid={testid}>
        {children}
      </dd>
    </div>
  );
}
