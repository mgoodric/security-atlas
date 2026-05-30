"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { listAnchors } from "@/lib/api/anchors";
import { APIError } from "@/lib/api/base";
import {
  ANCHORS_EXPORT_FORMATS,
  ANCHORS_EXPORT_FORMAT_LABELS,
  buildAnchorsExportURL,
} from "@/lib/api/anchors-export";

export default function SCFCatalogPage() {
  const router = useRouter();
  const { data, isLoading, error } = useQuery({
    queryKey: ["anchors"],
    queryFn: () => listAnchorsFromCookieSession(),
  });

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      router.push("/login?from=/catalog/scf");
    }
  }, [error, router]);

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">SCF Anchors</h1>
          <p className="text-sm text-muted-foreground">
            Browse the Secure Controls Framework anchor catalog. Click any
            anchor to see the framework requirements that map to it.
          </p>
        </div>
        {/* Slice 174: Export buttons (CSV / JSON / XLSX) wire to the
            BFF proxy at `/api/anchors/export?format=...`, which
            forwards to the platform `GET /v1/anchors/export`
            endpoint. Each link is an `<a download>` so the browser
            honours the backend's Content-Disposition filename. */}
        <div className="flex flex-wrap gap-2">
          {ANCHORS_EXPORT_FORMATS.map((format) => (
            <a
              key={format}
              href={buildAnchorsExportURL(format)}
              download
              rel="noopener"
              className={buttonVariants({ variant: "outline", size: "sm" })}
              data-testid={`anchors-export-${format}`}
            >
              Export {ANCHORS_EXPORT_FORMAT_LABELS[format]}
            </a>
          ))}
        </div>
      </div>

      {isLoading ? <AnchorSkeletons /> : null}

      {error && !(error instanceof APIError && error.status === 401) ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load anchors</AlertTitle>
          <AlertDescription>{(error as Error).message}</AlertDescription>
        </Alert>
      ) : null}

      {data ? (
        <Card>
          <CardHeader>
            <CardTitle>{data.length} anchors</CardTitle>
            <CardDescription>
              Subset bundled with slice 005; full SCF catalog imports with slice
              006.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-32">SCF ID</TableHead>
                  <TableHead className="w-24">Family</TableHead>
                  <TableHead>Name</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.map((anchor) => (
                  <TableRow key={anchor.id} className="cursor-pointer">
                    <TableCell className="font-mono text-xs">
                      <Link href={`/catalog/scf/${anchor.id}`}>
                        {anchor.scf_id}
                      </Link>
                    </TableCell>
                    <TableCell>{anchor.family}</TableCell>
                    <TableCell>
                      <Link
                        href={`/catalog/scf/${anchor.id}`}
                        className="text-sm font-medium hover:underline"
                      >
                        {anchor.name}
                      </Link>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}

function AnchorSkeletons() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  );
}

// listAnchorsFromCookieSession hits a same-origin Next.js route handler
// (/api/anchors) that proxies the bearer cookie into the upstream API.
// Going through a route handler keeps the bearer httpOnly on the client.
async function listAnchorsFromCookieSession() {
  const res = await fetch("/api/anchors");
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as {
    anchors: Awaited<ReturnType<typeof listAnchors>>;
  };
  return body.anchors;
}
