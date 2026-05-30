"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
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
import { Vendor, VendorBurndown } from "@/lib/api/vendors";

// Slice 024 — vendor lite list view. The filters are query-param-driven so
// a deep link survives reload and the user can bookmark "high + overdue".

type ListResp = { vendors: Vendor[] };

async function fetchVendors(params: URLSearchParams): Promise<Vendor[]> {
  const res = await fetch(`/api/vendors?${params.toString()}`);
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as ListResp;
  return body.vendors;
}

async function fetchBurndown(criticality?: string): Promise<VendorBurndown> {
  const qs = criticality ? `?criticality=${criticality}` : "";
  const res = await fetch(`/api/vendors/burndown${qs}`);
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as VendorBurndown;
}

export default function VendorsPage() {
  const router = useRouter();
  const search = useSearchParams();
  const criticality = search.get("criticality") ?? "";
  const overdueOnly = search.get("overdue") === "true";

  const params = new URLSearchParams();
  if (criticality) params.set("criticality", criticality);
  if (overdueOnly) params.set("overdue", "true");

  const vendorsQ = useQuery({
    queryKey: ["vendors", criticality, overdueOnly],
    queryFn: () => fetchVendors(params),
  });

  const burndownQ = useQuery({
    queryKey: ["vendors-burndown", criticality],
    queryFn: () => fetchBurndown(criticality || undefined),
  });

  useEffect(() => {
    if (vendorsQ.error instanceof APIError && vendorsQ.error.status === 401) {
      router.push("/login?from=/vendors");
    }
  }, [vendorsQ.error, router]);

  function setFilter(name: string, value: string | null) {
    const next = new URLSearchParams(search.toString());
    if (value === null || value === "") {
      next.delete(name);
    } else {
      next.set(name, value);
    }
    router.replace(`/vendors?${next.toString()}`);
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Vendors</h1>
          <p className="text-sm text-muted-foreground">
            Third-party register. Lite scope: contract dates, DPA, review
            cadence, criticality. Phase 2 adds questionnaire issuance.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {/* Slice 139 — vendor data export (csv|json|xlsx). The
              button group renders three direct links to the BFF;
              clicking each triggers the browser's file-save dialog.
              Owner emails are masked server-side to `*@domain.tld`. */}
          <ExportButtons />
          <Link href="/vendors/new" className={buttonVariants()}>
            Add vendor
          </Link>
        </div>
      </div>

      <BurndownCard
        burndown={burndownQ.data}
        loading={burndownQ.isLoading}
        error={burndownQ.error}
      />

      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-4">
          <div>
            <CardTitle>Vendor list</CardTitle>
            <CardDescription>
              Filter by criticality or surface overdue reviews only.
            </CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <FilterButton
              label="All"
              active={criticality === ""}
              onClick={() => setFilter("criticality", null)}
            />
            {(["high", "medium", "low"] as const).map((c) => (
              <FilterButton
                key={c}
                label={c}
                active={criticality === c}
                onClick={() => setFilter("criticality", c)}
              />
            ))}
            <FilterButton
              label="Overdue only"
              active={overdueOnly}
              onClick={() => setFilter("overdue", overdueOnly ? null : "true")}
            />
          </div>
        </CardHeader>
        <CardContent>
          {vendorsQ.isLoading ? <ListSkeleton /> : null}
          {vendorsQ.error &&
          !(
            vendorsQ.error instanceof APIError && vendorsQ.error.status === 401
          ) ? (
            <Alert variant="destructive">
              <AlertTitle>Could not load vendors</AlertTitle>
              <AlertDescription>
                {(vendorsQ.error as Error).message}
              </AlertDescription>
            </Alert>
          ) : null}
          {vendorsQ.data ? <VendorTable vendors={vendorsQ.data} /> : null}
        </CardContent>
      </Card>
    </div>
  );
}

function VendorTable({ vendors }: { vendors: Vendor[] }) {
  if (vendors.length === 0) {
    return (
      <p className="py-12 text-center text-sm text-muted-foreground">
        No vendors yet. Click <span className="font-medium">Add vendor</span> to
        retire the spreadsheet.
      </p>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead className="w-28">Criticality</TableHead>
          <TableHead className="w-28">Cadence</TableHead>
          <TableHead className="w-32">Last review</TableHead>
          <TableHead className="w-20">DPA</TableHead>
          <TableHead className="w-24">Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {vendors.map((v) => (
          <TableRow key={v.id} className="cursor-pointer">
            <TableCell>
              <Link
                href={`/vendors/${v.id}`}
                className="text-sm font-medium hover:underline"
              >
                {v.name}
              </Link>
              {v.domain ? (
                <span className="ml-2 text-xs text-muted-foreground">
                  {v.domain}
                </span>
              ) : null}
            </TableCell>
            <TableCell>
              <CriticalityBadge value={v.criticality} />
            </TableCell>
            <TableCell className="text-xs">{v.review_cadence}</TableCell>
            <TableCell className="text-xs">
              {v.last_review_date ?? (
                <span className="text-muted-foreground">never</span>
              )}
            </TableCell>
            <TableCell>
              {v.dpa_signed ? (
                <Badge variant="secondary">signed</Badge>
              ) : (
                <Badge variant="outline">no</Badge>
              )}
            </TableCell>
            <TableCell>
              {v.overdue ? (
                <Badge variant="destructive">overdue</Badge>
              ) : (
                <Badge variant="secondary">on time</Badge>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function CriticalityBadge({ value }: { value: string }) {
  const variant =
    value === "high"
      ? "destructive"
      : value === "medium"
        ? "secondary"
        : "outline";
  return <Badge variant={variant}>{value}</Badge>;
}

function BurndownCard({
  burndown,
  loading,
  error,
}: {
  burndown: VendorBurndown | undefined;
  loading: boolean;
  error: unknown;
}) {
  if (loading) return <Skeleton className="h-24 w-full" />;
  if (error || !burndown) return null;
  const pct = Math.round(burndown.total.on_time_fraction * 100);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Review burndown</CardTitle>
        <CardDescription>
          Reviews on schedule. Feeds the dashboard + the quarterly board pack.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-wrap gap-6">
        <Stat
          label="On-time"
          value={`${pct}%`}
          sub={`${burndown.total.total} vendors`}
        />
        <Stat label="Overdue" value={`${burndown.total.overdue}`} />
        {burndown.bands.map((b) => (
          <Stat
            key={b.criticality}
            label={`${b.criticality} on-time`}
            value={`${Math.round(b.on_time_fraction * 100)}%`}
            sub={`${b.overdue}/${b.total} overdue`}
          />
        ))}
      </CardContent>
    </Card>
  );
}

function Stat({
  label,
  value,
  sub,
}: {
  label: string;
  value: string;
  sub?: string;
}) {
  return (
    <div className="min-w-32">
      <div className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      <div className="text-2xl font-semibold tabular-nums">{value}</div>
      {sub ? <div className="text-xs text-muted-foreground">{sub}</div> : null}
    </div>
  );
}

function FilterButton({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      size="sm"
      variant={active ? "default" : "outline"}
      onClick={onClick}
    >
      {label}
    </Button>
  );
}

function ListSkeleton() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 5 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  );
}

// ExportButtons renders three direct-download links to the slice-139
// vendor export BFF — one per format. Each is an `<a>` (not a fetch)
// so the browser's native file-save dialog handles the download. The
// BFF streams the platform response back unchanged; the row cap +
// concurrency cap + role gate live server-side.
//
// `data-testid` tokens are stable contract points for the Playwright
// e2e spec (`web/e2e/vendors-export.spec.ts`).
function ExportButtons() {
  return (
    <div
      className="flex items-center gap-1"
      data-testid="vendors-export-buttons"
    >
      <span className="text-xs text-muted-foreground">Export:</span>
      {(["csv", "json", "xlsx"] as const).map((fmt) => (
        <a
          key={fmt}
          href={`/api/admin/vendors/export?format=${fmt}`}
          className={buttonVariants({ variant: "outline", size: "sm" })}
          data-testid={`vendors-export-${fmt}`}
          // <a download> is intentionally NOT set — the backend's
          // Content-Disposition header carries the canonical
          // filename (slice 135 BuildFilename) and the browser
          // honors it for the file-save dialog. Setting `download`
          // here would override that with the link text.
        >
          {fmt.toUpperCase()}
        </a>
      ))}
    </div>
  );
}
