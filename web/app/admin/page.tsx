// Slice 060 — /admin overview (AC-1).
//
// Five sub-area tiles. API keys + Features carry a live count from the
// BFF; SSO, Users and Audit log carry no summary (#2092 dropped their
// stale backend-status badges and the slice-status card).

"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  AdminCredential,
  AdminCredentialListResponse,
  FeatureFlag,
  FeatureFlagListResponse,
} from "@/lib/api/admin";
import { PageShell } from "@/components/shell/page-shell";
import { pageTitle } from "@/lib/page-titles";
import { Term } from "@/components/help/term";

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

// #2503: the layout admits a tenant admin too, who manages only the
// tenant-scoped pages (SSO, Users, Features, Audit log). The platform tiles,
// and the API key count fetch, are for a super-admin (`is_admin`). Fail-closed: a failed
// read shows the tenant-scoped tiles only.
async function fetchIsPlatformAdmin(): Promise<boolean> {
  const res = await fetch(`/api/admin/me`, { cache: "no-store" });
  if (!res.ok) return false;
  const body = (await res.json()) as { is_admin?: unknown };
  return body.is_admin === true;
}

export default function AdminOverviewPage() {
  const platformQ = useQuery({
    queryKey: ["admin-me", "is-admin"],
    queryFn: fetchIsPlatformAdmin,
  });
  const platform = platformQ.data === true;
  const creds = useQuery({
    queryKey: ["admin-creds"],
    queryFn: fetchCreds,
    enabled: platform,
  });
  // #2503: features are tenant-scoped, so every admin the layout admits
  // reads them.
  const flags = useQuery({
    queryKey: ["admin-features"],
    queryFn: fetchFeatures,
  });

  const flagsSummary = flags.isLoading
    ? "loading…"
    : flags.error
      ? "unavailable"
      : (() => {
          const total = flags.data?.length ?? 0;
          const on = (flags.data ?? []).filter((f) => f.enabled).length;
          return `${on} of ${total} enabled`;
        })();

  if (!platform) {
    return (
      <PageShell
        title={pageTitle("/admin")}
        subtitle="Manage your organisation's sign-in, members and their roles, and modules, and read its audit log."
      >
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <AdminTile
            href="/admin/sso"
            title="SSO"
            description="OIDC identity-provider configuration · discovery preflight · test login."
          />
          <AdminTile
            href="/admin/users"
            title="Users"
            description="List users, assign roles, manage local + SSO-provisioned accounts."
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
            description="Paginated read across the union of audit log tables. Filter by actor, event, time."
          />
        </div>
      </PageShell>
    );
  }

  const credsSummary = creds.isLoading
    ? "loading…"
    : creds.error
      ? "unavailable"
      : `${creds.data?.length ?? 0} active`;

  return (
    <PageShell
      title={pageTitle("/admin")}
      subtitle={
        <>
          Self-administer the platform — <Term id="sso">SSO</Term>, users,{" "}
          <Term id="api-key">API keys</Term>, feature flags, and the audit log
          live here. Day-to-day program work is outside this section.
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <AdminTile
          href="/admin/sso"
          title="SSO"
          description="OIDC identity-provider configuration · discovery preflight · test login."
        />
        <AdminTile
          href="/admin/users"
          title="Users"
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
          description="Paginated read across the union of audit log tables. Filter by actor, event, time."
        />
      </div>
    </PageShell>
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
  summary?: React.ReactNode;
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
