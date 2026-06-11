"use client";

import { useQuery } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { use, useEffect } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { APIError } from "@/lib/api/base";
import { Vendor } from "@/lib/api/vendors";

import { VendorForm } from "../vendor-form";
import { DeleteVendorButton } from "../delete-vendor-button";
import { updateVendorFromCookieSession } from "../actions";

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
  const { data, isLoading, error } = useQuery({
    queryKey: ["vendor", id],
    queryFn: () => fetchVendor(id),
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push(`/login?from=/vendors/${id}`);
    }
  }, [error, id, router]);

  return (
    <div className="space-y-6">
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
              router.push("/vendors");
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
