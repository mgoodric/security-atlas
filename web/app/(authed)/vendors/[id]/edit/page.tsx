"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { use, useEffect } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { APIError } from "@/lib/api/base";
import { Vendor } from "@/lib/api/vendors";

import { VendorForm } from "../../vendor-form";
import { DeleteVendorButton } from "../../delete-vendor-button";
import { updateVendorFromCookieSession } from "../../actions";

async function fetchVendor(id: string): Promise<Vendor> {
  const res = await fetch(`/api/vendors/${encodeURIComponent(id)}`);
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as { vendor: Vendor };
  return body.vendor;
}

export default function VendorEditPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({
    queryKey: ["vendor", id],
    queryFn: () => fetchVendor(id),
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/vendors/${id}/edit`);
    }
  }, [error, id, router]);

  return (
    <div className="space-y-6">
      <div className="text-sm">
        <Link
          href={`/vendors/${id}`}
          className="text-muted-foreground hover:underline"
          data-testid="vendor-edit-back"
        >
          ← Vendor detail
        </Link>
      </div>
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Edit vendor</h1>
        <p className="text-sm text-muted-foreground">
          Save updates the record and re-binds scope cells. Delete removes the
          row and its cell bindings.
        </p>
      </div>
      {isLoading ? <Skeleton className="h-96 w-full" /> : null}
      {error && !(error instanceof APIError && error.status === 401) ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load vendor</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      ) : null}
      {data ? (
        <>
          <VendorForm
            initial={data}
            onSubmit={async (body) => {
              await updateVendorFromCookieSession(id, body);
              // Slice 691 — refresh the detail surface the operator is
              // redirected to. The detail and edit pages share the
              // ["vendor", id] key, so without this invalidation the
              // read-only detail serves the cached pre-review vendor and
              // the derived review-status badge (overdue -> on time) and
              // the Last-review field stay stale until a hard reload.
              //
              // Await the detail + history invalidations so their refetch
              // is in flight BEFORE we navigate (avoids the shared-key
              // read-back race noted in slice 424): the detail page mounts
              // against an already-invalidated cache and refetches rather
              // than painting the stale cached body.
              await Promise.all([
                qc.invalidateQueries({ queryKey: ["vendor", id] }),
                qc.invalidateQueries({ queryKey: ["vendor-reviews", id] }),
              ]);
              // The list + burndown are prefix-matched (their keys carry
              // filter params, e.g. ["vendors", criticality, overdueOnly]);
              // a partial-key invalidate marks every variant stale so the
              // list has no overdue row on return to /vendors. These need
              // not block the navigation.
              void qc.invalidateQueries({ queryKey: ["vendors"] });
              void qc.invalidateQueries({ queryKey: ["vendors-burndown"] });
              router.push(`/vendors/${id}`);
            }}
            submitLabel="Save changes"
          />
          <div className="flex items-center justify-between rounded-lg border border-destructive/30 bg-destructive/5 p-4">
            <div>
              <p className="text-sm font-medium">Delete vendor</p>
              <p className="text-xs text-muted-foreground">
                Removes the row and its cell bindings. This cannot be undone.
              </p>
            </div>
            <DeleteVendorButton
              vendorId={id}
              vendorName={data.name}
              onDeleted={() => router.push("/vendors")}
            />
          </div>
        </>
      ) : null}
    </div>
  );
}
