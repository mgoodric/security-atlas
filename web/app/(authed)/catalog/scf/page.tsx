"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";

import { ListPagination, paginateRows } from "@/components/list/pagination";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
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
import { APIError } from "@/lib/api/base";
import {
  ANCHORS_EXPORT_FORMATS,
  ANCHORS_EXPORT_FORMAT_LABELS,
  buildAnchorsExportURL,
} from "@/lib/api/anchors-export";
import { fetchControlsCatalog } from "@/lib/api/controls-list";

import { catalogTruncationNote } from "../../controls/view";
import {
  CATALOG_PAGE_SIZE,
  anchorHasControl,
  catalogCountLabel,
  searchAnchors,
} from "./view";

export default function SCFCatalogPage() {
  const router = useRouter();
  // Issue #2066 — walk every page of the catalog (the platform's default
  // page is 100 rows) through the same `/v1/anchors` read /controls uses.
  // Its rows carry `control_count`, which drives the "Has control" marker.
  const { data, isLoading, error } = useQuery({
    queryKey: ["anchors", "catalog"],
    queryFn: () => fetchControlsCatalog(),
  });
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const matches = useMemo(
    () => searchAnchors(data?.anchors ?? [], query),
    [data, query],
  );
  const rows = paginateRows(matches, page, CATALOG_PAGE_SIZE);
  const truncation = data
    ? catalogTruncationNote(data.anchors.length, data.total)
    : "";

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
            <CardTitle data-testid="catalog-scf-count">
              {catalogCountLabel(data.anchors.length, data.total)}
            </CardTitle>
            <CardDescription>
              Search by SCF code, title or family. &ldquo;Has control&rdquo;
              marks anchors that at least one of your controls uses.
              {truncation ? ` ${truncation}.` : null}
            </CardDescription>
            <Input
              type="search"
              aria-label="Search SCF anchors"
              placeholder="Search by code, title or family"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setPage(1);
              }}
              className="mt-2 max-w-sm"
              data-testid="catalog-scf-search"
            />
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
                {rows.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={3}
                      className="text-sm text-muted-foreground"
                      data-testid="catalog-scf-empty"
                    >
                      No anchors match &ldquo;{query}&rdquo;.
                    </TableCell>
                  </TableRow>
                ) : null}
                {rows.map((anchor) => (
                  <TableRow
                    key={anchor.id}
                    className="cursor-pointer"
                    data-testid="catalog-scf-row"
                  >
                    <TableCell className="font-mono text-xs">
                      <Link href={`/catalog/scf/${anchor.id}`}>
                        {anchor.scf_id}
                      </Link>
                    </TableCell>
                    <TableCell>{anchor.family}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Link
                          href={`/catalog/scf/${anchor.id}`}
                          className="text-sm font-medium hover:underline"
                        >
                          {anchor.name}
                        </Link>
                        {anchorHasControl(anchor) ? (
                          <Badge
                            variant="pass"
                            data-testid="catalog-scf-has-control"
                          >
                            Has control
                          </Badge>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
          <ListPagination
            currentPage={page}
            pageSize={CATALOG_PAGE_SIZE}
            totalCount={matches.length}
            onPageChange={setPage}
            testIdPrefix="catalog-scf-pagination"
          />
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
