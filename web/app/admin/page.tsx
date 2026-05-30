// Slice 060 — /admin overview (AC-1).
//
// Five sub-area tiles with current-state summaries. SSO + Users + Audit
// surface the backend-gap state because the corresponding endpoints
// don't exist on main as of slice 060 — see docs/audit-log/admin-ui-review.md
// and the slice 060 PR description. API keys + Features pull live counts
// from the BFF.

"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  AdminCredential,
  AdminCredentialListResponse,
  FeatureFlag,
  FeatureFlagListResponse,
} from "@/lib/api/admin";

async function fetchCreds(): Promise<AdminCredential[]> {
  const res = await fetch(`/api/admin/credentials`);
  if (!res.ok) throw new Error(`credentials: ${res.status}`);
  const body = (await res.json()) as AdminCredentialListResponse;
  return body.items ?? [];
}

async function fetchFeatures(): Promise<FeatureFlag[]> {
  const res = await fetch(`/api/admin/features`);
  if (!res.ok) throw new Error(`features: ${res.status}`);
  const body = (await res.json()) as FeatureFlagListResponse;
  return body.items ?? [];
}

export default function AdminOverviewPage() {
  const creds = useQuery({ queryKey: ["admin-creds"], queryFn: fetchCreds });
  const flags = useQuery({
    queryKey: ["admin-features"],
    queryFn: fetchFeatures,
  });

  const credsSummary = creds.isLoading
    ? "loading…"
    : creds.error
      ? "unavailable"
      : `${creds.data?.length ?? 0} active`;

  const flagsSummary = flags.isLoading
    ? "loading…"
    : flags.error
      ? "unavailable"
      : (() => {
          const total = flags.data?.length ?? 0;
          const on = (flags.data ?? []).filter((f) => f.enabled).length;
          return `${on} of ${total} enabled`;
        })();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Admin</h1>
        <p className="text-sm text-muted-foreground">
          Self-administer the platform — SSO, users, API keys, feature flags,
          and the audit log live here. Day-to-day program work is outside this
          section.
        </p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <AdminTile
          href="/admin/sso"
          title="SSO"
          summary={
            <span className="text-muted-foreground">
              backend WIP — slice&nbsp;060.5
            </span>
          }
          description="OIDC identity-provider configuration · discovery preflight · test login."
        />
        <AdminTile
          href="/admin/users"
          title="Users"
          summary={
            <span className="text-muted-foreground">
              backend WIP — slice&nbsp;060.5
            </span>
          }
          description="List users, assign roles, manage local + SSO-provisioned accounts."
        />
        <AdminTile
          href="/admin/api-keys"
          title="API keys"
          summary={
            creds.isLoading ? <Skeleton className="h-4 w-24" /> : credsSummary
          }
          description="Issue, rotate, and revoke connector and CLI credentials. Bearer plaintext is shown once."
        />
        <AdminTile
          href="/admin/features"
          title="Features"
          summary={
            flags.isLoading ? <Skeleton className="h-4 w-24" /> : flagsSummary
          }
          description="Toggle feature flags per tenant. Disabling a module hides routes; data is preserved."
        />
        <AdminTile
          href="/admin/audit"
          title="Audit log"
          summary={
            <span className="text-muted-foreground">
              backend WIP — slice&nbsp;060.5
            </span>
          }
          description="Paginated read across the union of audit log tables. Filter by actor, event, time."
        />
        <Card>
          <CardHeader>
            <CardTitle>Slice 060 status</CardTitle>
            <CardDescription>
              What ships in this UI vs. what depends on backend work.
            </CardDescription>
          </CardHeader>
          <CardContent className="text-sm">
            <p className="mb-2">
              <Badge variant="secondary">live</Badge> API keys · Features
            </p>
            <p>
              <Badge variant="outline">stub</Badge> SSO · Users · Audit log — UI
              rendered, awaiting backend slice 060.5.
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function AdminTile({
  href,
  title,
  summary,
  description,
}: {
  href: string;
  title: string;
  summary: React.ReactNode;
  description: string;
}) {
  return (
    <Link href={href} className="block">
      <Card className="h-full transition-colors hover:bg-muted/40">
        <CardHeader>
          <div className="flex items-center justify-between gap-2">
            <CardTitle>{title}</CardTitle>
            <span className="text-xs font-medium tabular-nums text-foreground/80">
              {summary}
            </span>
          </div>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
      </Card>
    </Link>
  );
}
