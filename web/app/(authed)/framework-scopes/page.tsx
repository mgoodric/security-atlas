"use client";

// #2081 — Framework scopes index. Before this page a framework's scope
// (`/framework-scopes/<framework_version_id>`) was reachable only by URL;
// the sidebar's Framework scopes entry lands here and lists one row per
// framework in the catalog (GET /v1/frameworks via the BFF), each linking
// to its latest version's scope.
//
// A framework with no loaded version has nothing to scope, so its row
// says so instead of linking.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { PageShell } from "@/components/shell/page-shell";
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
import { pageTitle } from "@/lib/page-titles";
import { Term } from "@/components/help/term";

import { newScopeHref } from "./wizard";

type Framework = {
  id: string;
  name: string;
  slug: string;
  issuer: string;
  latest_version_id?: string;
};

async function fetchFrameworks(): Promise<Framework[]> {
  const res = await fetch("/api/frameworks");
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText}`);
  }
  return ((await res.json()) as { frameworks: Framework[] }).frameworks;
}

const SUBTITLE = (
  <>
    Which systems each framework applies to. Open a framework to edit, review
    and activate its <Term id="framework-scope">scope</Term> and its{" "}
    <Term id="statement-of-applicability">Statement of Applicability</Term>, or
    start a new one.
  </>
);

export default function FrameworkScopesIndexPage() {
  const frameworksQ = useQuery({
    queryKey: ["frameworks"],
    queryFn: fetchFrameworks,
  });

  return (
    <PageShell
      title={pageTitle("/framework-scopes")}
      subtitle={SUBTITLE}
      data-testid="framework-scopes-index"
      actions={
        // #2115: the wizard picks the framework version itself.
        <Link
          href={newScopeHref()}
          className={buttonVariants({ size: "sm" })}
          data-testid="framework-scopes-new"
        >
          New scope
        </Link>
      }
    >
      {frameworksQ.isLoading ? (
        <Skeleton className="h-32 w-full" />
      ) : frameworksQ.isError ? (
        <Alert variant="destructive" data-testid="framework-scopes-load-error">
          <AlertTitle>Could not load frameworks</AlertTitle>
          <AlertDescription>
            {(frameworksQ.error as Error).message}
          </AlertDescription>
        </Alert>
      ) : (frameworksQ.data ?? []).length === 0 ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="framework-scopes-empty"
        >
          No frameworks are loaded yet.
        </p>
      ) : (
        <div className="min-w-0 overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Framework</TableHead>
                <TableHead>Issuer</TableHead>
                <TableHead>Scope</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(frameworksQ.data ?? []).map((f) => (
                <TableRow key={f.id} data-testid="framework-scopes-row">
                  <TableCell className="font-medium">{f.name}</TableCell>
                  <TableCell>{f.issuer}</TableCell>
                  <TableCell>
                    {f.latest_version_id ? (
                      <Link
                        href={`/framework-scopes/${encodeURIComponent(
                          f.latest_version_id,
                        )}`}
                        aria-label={`Open scope: ${f.name}`}
                        className="underline underline-offset-2"
                      >
                        Open scope
                      </Link>
                    ) : (
                      <span className="text-muted-foreground">
                        No version loaded
                      </span>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </PageShell>
  );
}
