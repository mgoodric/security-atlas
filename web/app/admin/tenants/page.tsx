// Slice 143 — /admin/tenants management page.
//
// LIST + CREATE for the slice-143 create-tenant flow. The page binds
// to the BFF at /api/admin/tenants which proxies to the platform's
// /v1/admin/tenants (super_admin-gated upstream).
//
// The list table renders one row per tenant with:
//   - name + slug (bootstrap row's slug may be null → renders "—")
//   - is_bootstrap_tenant badge (slice-198 first-install row)
//   - created_at + created_by_user_id (provenance — null for legacy
//     and bootstrap rows)
//
// LOAD-BEARING UX: the create form includes:
//   - Required name input (≤ 64 bytes, application-trimmed)
//   - Required slug input (^[a-z0-9][a-z0-9-]{0,62}$ — client-side
//     regex check mirrors the BFF + upstream validation)
//   - "Join as admin" checkbox (creator_joins_as='admin' when on;
//     'none' when off). Default off — the operator opts in to
//     ride-along access.
//
// LOAD-BEARING UX: on a successful create, the operator sees a
// result modal carrying the new tenant_id + (if creator_joins_as=
// 'admin') the new users.id. Both are useful for the next operator
// step (e.g., "now grant tenant_admin to other identities via the
// existing /admin/users surface"). The modal also documents that
// the new tenant ships with a default scope cell + builtin
// dimension so it's immediately usable.

"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { CreateTenantResponse, TenantRow } from "@/lib/api";

type TenantListResponse = { items: TenantRow[]; error?: string };

const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}$/;

function formatCreatedAt(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

export default function AdminTenantsPage() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [joinAsAdmin, setJoinAsAdmin] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [resultModal, setResultModal] = useState<CreateTenantResponse | null>(
    null,
  );

  const { data, isLoading, isError } = useQuery({
    queryKey: ["admin", "tenants"],
    queryFn: async () => {
      const res = await fetch("/api/admin/tenants", { cache: "no-store" });
      const body = (await res.json()) as TenantListResponse;
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body.items ?? [];
    },
  });

  const createMutation = useMutation({
    mutationFn: async (req: {
      name: string;
      slug: string;
      creator_joins_as: "admin" | "none";
    }) => {
      const res = await fetch("/api/admin/tenants", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(req),
      });
      const body = (await res.json()) as {
        error?: string;
      } & CreateTenantResponse;
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body;
    },
    onSuccess: async (out) => {
      setName("");
      setSlug("");
      setJoinAsAdmin(false);
      setCreateError(null);
      setResultModal(out);
      await queryClient.invalidateQueries({ queryKey: ["admin", "tenants"] });
    },
    onError: (err: Error) => {
      setCreateError(err.message);
    },
  });

  function clientValidate(): string | null {
    if (name.trim() === "") return "name is required";
    if (name.length > 64) return "name exceeds 64-byte cap";
    if (slug.trim() === "") return "slug is required";
    if (!SLUG_PATTERN.test(slug.trim())) {
      return "slug must match ^[a-z0-9][a-z0-9-]{0,62}$";
    }
    return null;
  }

  // Slice 363 — form-error association. When the submit-error Alert is
  // mounted, every input in the form points its aria-describedby at the
  // Alert's stable id so SR users hear the error when tabbing back to
  // an input. See `web/components/ui/checkbox.tsx` header for the full
  // convention.
  const createErrorId = createError ? "create-tenant-error" : undefined;

  return (
    <div className="space-y-6" data-testid="admin-tenants-page">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Tenants</h1>
        <p className="text-sm text-muted-foreground">
          Platform-global tenant identities. Super admins create new tenants
          here. Each new tenant ships with one builtin <code>environment</code>{" "}
          scope dimension and a default <code>All</code> scope cell so it is
          immediately usable for control + evidence work.
        </p>
      </div>

      <Alert>
        <AlertTitle>Create discipline</AlertTitle>
        <AlertDescription>
          Tenant create is rate-limited to 100 tenants per super admin per
          rolling 24h window. The slug is the URL-safe stable handle (lower-
          case ASCII, digits, hyphens; 1-63 chars). Tenant deletion is{" "}
          <strong>not</strong> available in this release — provision carefully.
        </AlertDescription>
      </Alert>

      <Card>
        <CardHeader>
          <CardTitle>Create new tenant</CardTitle>
          <CardDescription>
            Name is the human-readable label rendered in the tenant switcher.
            Slug is a URL-safe handle. If you check <em>Join as admin</em>, the
            handler also writes a per-tenant users row + a tenant_admin role
            grant anchored to your existing OIDC identity, so your next sign-in
            surfaces the new tenant in your switcher.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              const v = clientValidate();
              if (v) {
                setCreateError(v);
                return;
              }
              setCreateError(null);
              createMutation.mutate({
                name: name.trim(),
                slug: slug.trim(),
                creator_joins_as: joinAsAdmin ? "admin" : "none",
              });
            }}
          >
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="space-y-1">
                <label
                  htmlFor="tenant-name"
                  className="text-xs font-medium text-muted-foreground"
                >
                  Name
                </label>
                <Input
                  id="tenant-name"
                  data-testid="tenant-name-input"
                  placeholder="Acme Corp"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  spellCheck={false}
                  autoComplete="off"
                  aria-describedby={createErrorId}
                />
              </div>
              <div className="space-y-1">
                <label
                  htmlFor="tenant-slug"
                  className="text-xs font-medium text-muted-foreground"
                >
                  Slug
                </label>
                <Input
                  id="tenant-slug"
                  data-testid="tenant-slug-input"
                  placeholder="acme-corp"
                  value={slug}
                  onChange={(e) => setSlug(e.target.value)}
                  spellCheck={false}
                  autoComplete="off"
                  aria-describedby={createErrorId}
                />
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Checkbox
                id="tenant-join-as-admin"
                data-testid="tenant-join-as-admin"
                checked={joinAsAdmin}
                onCheckedChange={(checked) => setJoinAsAdmin(checked === true)}
                aria-describedby={createErrorId}
              />
              <label
                htmlFor="tenant-join-as-admin"
                className="text-sm text-foreground"
              >
                Join as admin (anchor my OIDC identity to the new tenant with
                the <code>admin</code> role)
              </label>
            </div>
            <Button
              type="submit"
              data-testid="create-tenant-submit"
              disabled={createMutation.isPending}
            >
              {createMutation.isPending ? "Creating…" : "Create tenant"}
            </Button>
            {createError ? (
              <Alert
                variant="destructive"
                id="create-tenant-error"
                aria-live="polite"
              >
                <AlertTitle>Create failed</AlertTitle>
                <AlertDescription data-testid="create-tenant-error">
                  {createError}
                </AlertDescription>
              </Alert>
            ) : null}
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Current tenants</CardTitle>
          <CardDescription>
            All tenant identity rows on the platform. The bootstrap row (
            <code>is_bootstrap_tenant=true</code>) was provisioned at first
            install via the OIDC sign-in path (slice 198).
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="text-sm text-muted-foreground">Loading…</div>
          ) : isError ? (
            <Alert variant="destructive">
              <AlertTitle>Could not load tenants</AlertTitle>
              <AlertDescription>
                The list endpoint returned an error. Refresh to retry.
              </AlertDescription>
            </Alert>
          ) : (data ?? []).length === 0 ? (
            <div className="rounded-lg border border-dashed p-12 text-center text-sm text-muted-foreground">
              No tenants. This should be impossible after first install —
              bootstrap always provisions one. Check the migration ran.
            </div>
          ) : (
            <Table data-testid="tenants-table">
              <TableHeader>
                <TableRow>
                  <TableHead>Tenant</TableHead>
                  <TableHead>Slug</TableHead>
                  <TableHead>Origin</TableHead>
                  <TableHead>Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data ?? []).map((t) => (
                  <TableRow key={t.id} data-testid={`tenant-row-${t.id}`}>
                    <TableCell>
                      <div className="font-medium">{t.name}</div>
                      <div className="font-mono text-xs text-muted-foreground">
                        {t.id}
                      </div>
                    </TableCell>
                    <TableCell>
                      <code className="text-sm">{t.slug ?? "—"}</code>
                    </TableCell>
                    <TableCell>
                      {t.is_bootstrap_tenant ? (
                        <Badge variant="secondary">
                          bootstrap_first_install
                        </Badge>
                      ) : (
                        <Badge variant="default">manual_create</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatCreatedAt(t.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog
        open={resultModal !== null}
        onOpenChange={(open) => {
          if (!open) setResultModal(null);
        }}
      >
        <DialogPortal>
          <DialogContent data-testid="create-tenant-result">
            <DialogHeader>
              <DialogTitle>Tenant created</DialogTitle>
              <DialogDescription>
                The new tenant is live. It ships with one builtin{" "}
                <code>environment</code> scope dimension and a default{" "}
                <code>All</code> scope cell. Next steps: grant additional admin
                identities via <code>/admin/users</code>, then onboard
                connectors + control owners.
              </DialogDescription>
            </DialogHeader>
            {resultModal ? (
              <div className="space-y-2 text-sm">
                <div>
                  <span className="font-medium">Name:</span>{" "}
                  {resultModal.tenant.name}
                </div>
                <div>
                  <span className="font-medium">Slug:</span>{" "}
                  <code>{resultModal.tenant.slug ?? "—"}</code>
                </div>
                <div>
                  <span className="font-medium">Tenant ID:</span>{" "}
                  <code
                    className="font-mono text-xs"
                    data-testid="result-tenant-id"
                  >
                    {resultModal.tenant.id}
                  </code>
                </div>
                {resultModal.creator_admin_user_id ? (
                  <div>
                    <span className="font-medium">
                      Your new users row in this tenant:
                    </span>{" "}
                    <code
                      className="font-mono text-xs"
                      data-testid="result-creator-user-id"
                    >
                      {resultModal.creator_admin_user_id}
                    </code>
                  </div>
                ) : (
                  <div className="text-muted-foreground">
                    You did <strong>not</strong> opt in to admin access on this
                    tenant. To access it, sign in via OIDC after another admin
                    grants you a per-tenant role.
                  </div>
                )}
              </div>
            ) : null}
            <DialogFooter>
              <Button onClick={() => setResultModal(null)}>Close</Button>
            </DialogFooter>
          </DialogContent>
        </DialogPortal>
      </Dialog>
    </div>
  );
}
