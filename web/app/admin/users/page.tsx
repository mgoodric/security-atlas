// Slice 479 — /admin/users (wires the slice-060 scaffold to slice 478).
//
// The slice-060 page was a scaffold: the role-permission matrix plus a
// placeholder user table, documenting a backend gap ("no /v1/admin/users
// HTTP endpoint"). Slice 478 shipped that endpoint (list + assign +
// revoke, with the super-admin cross-tenant surface and the self-assign
// path); this slice makes it usable.
//
// What this page DOES:
//   - Lists users with tenant + roles from GET /api/admin/users (AC-1).
//     For a super_admin the response is the cross-tenant shape (each row
//     carries tenant_id); for a tenant-admin it is the within-tenant
//     shape. The page derives `crossTenant` from the response and gates
//     the cross-tenant controls on it (P0-479-2 — a tenant-admin sees no
//     cross-tenant controls).
//   - Assign-to-tenant: a dialog (user_id + tenant_id + role checkboxes)
//     posts to /api/admin/users; on success the list refetches and the
//     inline result surfaces (AC-2).
//   - Revoke: a per-row action behind a confirm dialog (AC-3).
//   - Self-assign: "Add me to a tenant" for super_admins; on success the
//     UI explains a re-auth (full re-login) is needed before the new
//     tenant appears in the switcher, and links to the sign-in flow
//     (AC-4 / P0-479-3 — NO auto-switch).
//
// AUTHZ-HONEST (AC-5 / P0-479-1): the server (478) is the gate. This page
// surfaces the server's decisions honestly: cross-tenant controls render
// only when the response proves super_admin scope, and any 403 from a
// mutation renders as a clear inline error, never a dead button or a
// silent failure.
//
// The slice-060 role-permission matrix is retained below the user table
// (AC-1 "retained or linked"). It remains the canonical role reference.

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
import { Button, buttonVariants } from "@/components/ui/button";
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
import { Select } from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  RoleBadge,
  ROLE_DESCRIPTIONS,
  ROLES,
  type Role,
} from "@/components/admin/roles";
import type {
  AdminUserRow,
  AdminUserListResult,
  AssignUserResponse,
  TenantRow,
} from "@/lib/api/admin";
import {
  userOptions,
  tenantOptions,
  tenantFieldMode,
} from "@/lib/admin/assign-options";

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

type AdminUserListBody = AdminUserListResult & { error?: string };

export default function UsersPage() {
  const queryClient = useQueryClient();

  // Assign dialog state.
  const [assignOpen, setAssignOpen] = useState(false);
  const [selfAssign, setSelfAssign] = useState(false);
  const [assignUserID, setAssignUserID] = useState("");
  const [assignTenantID, setAssignTenantID] = useState("");
  const [assignRoles, setAssignRoles] = useState<Role[]>([]);
  const [assignError, setAssignError] = useState<string | null>(null);
  const [reauthNotice, setReauthNotice] = useState<AssignUserResponse | null>(
    null,
  );

  // Revoke confirm state.
  const [revokeTarget, setRevokeTarget] = useState<AdminUserRow | null>(null);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["admin", "users"],
    queryFn: async (): Promise<AdminUserListResult> => {
      const res = await fetch("/api/admin/users", { cache: "no-store" });
      const body = (await res.json()) as AdminUserListBody;
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body;
    },
  });

  // The session tenant — used as the revoke tenant_id fallback for the
  // within-tenant (tenant-admin) list shape, whose rows carry no
  // tenant_id. Best-effort; failure leaves it undefined and the
  // within-tenant revoke button is disabled (see below).
  const { data: me } = useQuery({
    queryKey: ["me", "session-tenant"],
    queryFn: async (): Promise<{ tenant_id?: string }> => {
      const res = await fetch("/api/me", { cache: "no-store" });
      if (!res.ok) return {};
      return (await res.json()) as { tenant_id?: string };
    },
  });

  const crossTenant = data?.cross_tenant ?? false;
  const items = data?.items ?? [];

  // AC-2 / AC-4: the cross-tenant tenant list is super_admin-only. Gate the
  // fetch on `crossTenant` so a tenant-admin's browser never receives the
  // other-tenant names (closes STRIDE-I at the fetch boundary — decisions
  // log D3). No second authority probe — `crossTenant` is the existing
  // signal (P0-527-2).
  const { data: tenantsData } = useQuery({
    queryKey: ["admin", "tenants"],
    enabled: crossTenant,
    queryFn: async (): Promise<TenantRow[]> => {
      const res = await fetch("/api/admin/tenants", { cache: "no-store" });
      const body = (await res.json()) as {
        items?: TenantRow[];
        error?: string;
      };
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body.items ?? [];
    },
  });

  // AC-1: user options come from the ALREADY-LOADED list — no second fetch.
  const userOpts = userOptions(items);
  // AC-2/AC-4: tenant options only exist for a super_admin (the gated query).
  const tenantOpts = tenantOptions(tenantsData ?? []);
  // AC-3: pinned (tenant-admin) vs chooser (super_admin) — driven by the
  // existing cross_tenant signal + the already-fetched session tenant.
  const tenantMode = tenantFieldMode({
    crossTenant,
    sessionTenantId: me?.tenant_id,
  });

  // resolveRevokeTenant picks the row's tenant_id (cross-tenant shape) or
  // falls back to the session tenant (within-tenant shape).
  function resolveRevokeTenant(row: AdminUserRow): string | undefined {
    return row.tenant_id ?? me?.tenant_id;
  }

  const assignMutation = useMutation({
    mutationFn: async (req: {
      user_id?: string;
      tenant_id: string;
      roles: string[];
      self_assign: boolean;
    }) => {
      const res = await fetch("/api/admin/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(req),
      });
      const body = (await res.json()) as {
        error?: string;
      } & AssignUserResponse;
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body;
    },
    onSuccess: async (out, vars) => {
      setAssignOpen(false);
      setAssignUserID("");
      setAssignTenantID("");
      setAssignRoles([]);
      setAssignError(null);
      await queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
      // AC-4: after a self-assign, the new tenant is NOT yet in the
      // caller's available_tenants until a fresh token is minted. Surface
      // the re-auth requirement (P0-479-3: no auto-switch).
      if (vars.self_assign) {
        setReauthNotice(out);
      }
    },
    onError: (err: Error) => {
      setAssignError(err.message);
    },
  });

  const revokeMutation = useMutation({
    mutationFn: async (req: { user_id: string; tenant_id: string }) => {
      const res = await fetch("/api/admin/users/revoke", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(req),
      });
      const body = (await res.json()) as { error?: string; ok?: boolean };
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body;
    },
    onSuccess: async () => {
      setRevokeTarget(null);
      setRevokeError(null);
      await queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
    },
    onError: (err: Error) => {
      setRevokeError(err.message);
    },
  });

  function openAssign(opts: { self?: boolean; userID?: string }) {
    setSelfAssign(opts.self === true);
    setAssignUserID(opts.userID ?? "");
    // AC-3: a tenant-admin's tenant is pinned to their session tenant — seed
    // the field so the (read-only) value posts on submit. A super_admin
    // starts with an empty chooser.
    setAssignTenantID(tenantMode.kind === "pinned" ? tenantMode.tenantId : "");
    setAssignRoles([]);
    setAssignError(null);
    setAssignOpen(true);
  }

  function toggleRole(role: Role, checked: boolean) {
    setAssignRoles((prev) =>
      checked ? [...new Set([...prev, role])] : prev.filter((r) => r !== role),
    );
  }

  function clientValidateAssign(): string | null {
    if (tenantMode.kind === "unresolved") {
      return "your session tenant is still resolving — reload and retry";
    }
    if (assignTenantID.trim() === "") return "select a tenant";
    if (!UUID_PATTERN.test(assignTenantID.trim())) {
      return "tenant_id must be a UUID";
    }
    if (assignRoles.length === 0) return "select at least one role";
    if (!selfAssign) {
      if (assignUserID.trim() === "") return "select a user";
      if (!UUID_PATTERN.test(assignUserID.trim())) {
        return "user_id must be a UUID";
      }
    }
    return null;
  }

  const assignErrorId = assignError ? "assign-user-error" : undefined;

  return (
    <div className="space-y-6" data-testid="admin-users-page">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Users</h1>
          <p className="text-sm text-muted-foreground">
            User accounts with their tenant memberships and RBAC roles. Roles
            are the coarse authorization signal; ABAC predicates layer over them
            per scope.
          </p>
        </div>
        <div className="flex shrink-0 gap-2">
          <Button data-testid="open-assign-user" onClick={() => openAssign({})}>
            Assign user to tenant
          </Button>
          {/* AC-4 / P0-479-2: self-assign is a cross-tenant action — only a
              super_admin (cross-tenant response shape) sees it. */}
          {crossTenant ? (
            <Button
              variant="secondary"
              data-testid="open-self-assign"
              onClick={() => openAssign({ self: true })}
            >
              Add me to a tenant
            </Button>
          ) : null}
        </div>
      </div>

      <Alert>
        <AlertTitle>
          {crossTenant
            ? "Cross-tenant view (super admin)"
            : "Tenant-scoped view"}
        </AlertTitle>
        <AlertDescription>
          {crossTenant ? (
            <>
              You are a super admin: this list spans every tenant, and you can
              assign any identity (including yourself) to any tenant. The server
              is the authority — actions you are not permitted to take are
              rejected with a clear error.
            </>
          ) : (
            <>
              This list is scoped to your tenant. Assignment is limited to your
              own tenant. Cross-tenant management requires super admin.
            </>
          )}
        </AlertDescription>
      </Alert>

      <Card>
        <CardHeader>
          <CardTitle>Users</CardTitle>
          <CardDescription>
            {crossTenant
              ? "One row per user-in-tenant membership across the platform."
              : "Users in your tenant, with their roles."}{" "}
            Bound to <code>GET /v1/admin/users</code> (slice 478).
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="text-sm text-muted-foreground">Loading…</div>
          ) : isError ? (
            <Alert variant="destructive" data-testid="users-load-error">
              <AlertTitle>Could not load users</AlertTitle>
              <AlertDescription>
                {(error as Error)?.message ??
                  "The list endpoint returned an error."}
              </AlertDescription>
            </Alert>
          ) : items.length === 0 ? (
            <div className="rounded-lg border border-dashed p-12 text-center text-sm text-muted-foreground">
              No users to show. Provision users via{" "}
              <code>atlas-cli users create-local</code> or the SSO callback,
              then assign them to a tenant.
            </div>
          ) : (
            <Table data-testid="users-table">
              <TableHeader>
                <TableRow>
                  <TableHead>User</TableHead>
                  {crossTenant ? <TableHead>Tenant</TableHead> : null}
                  <TableHead>Status</TableHead>
                  <TableHead>Roles</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((u) => (
                  <TableRow
                    key={`${u.tenant_id ?? "t"}-${u.id}`}
                    data-testid={`user-row-${u.id}`}
                  >
                    <TableCell>
                      <div className="font-medium">{u.display_name || "—"}</div>
                      <div className="text-xs text-muted-foreground">
                        {u.email}
                      </div>
                      <div className="font-mono text-[10px] text-muted-foreground">
                        {u.id}
                      </div>
                    </TableCell>
                    {crossTenant ? (
                      <TableCell>
                        <code className="font-mono text-xs">{u.tenant_id}</code>
                      </TableCell>
                    ) : null}
                    <TableCell>
                      <Badge
                        variant={
                          u.status === "active" ? "secondary" : "outline"
                        }
                      >
                        {u.status}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {u.roles.length === 0 ? (
                          <span className="text-xs text-muted-foreground">
                            none
                          </span>
                        ) : (
                          u.roles.map((r) =>
                            (ROLES as readonly string[]).includes(r) ? (
                              <RoleBadge key={r} role={r as Role} />
                            ) : (
                              <Badge
                                key={r}
                                variant="outline"
                                className="font-mono text-[10px]"
                              >
                                {r}
                              </Badge>
                            ),
                          )
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        data-testid={`revoke-user-${u.id}`}
                        onClick={() => {
                          setRevokeError(null);
                          setRevokeTarget(u);
                        }}
                      >
                        Revoke roles
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* AC-1: the slice-060 role-permission matrix is retained as the
          canonical role reference. */}
      <Card>
        <CardHeader>
          <CardTitle>Role-permission matrix</CardTitle>
          <CardDescription>
            Five canonical RBAC roles per{" "}
            <code>Plans/canvas/09-tech-stack.md</code> §9.5. Permissions noted
            at the coarse level; fine cuts layer on via ABAC predicates.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-32">Role</TableHead>
                <TableHead className="w-64">One-line description</TableHead>
                <TableHead>Coarse permissions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ROLES.map((role) => (
                <TableRow key={role}>
                  <TableCell>
                    <RoleBadge role={role} />
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {ROLE_DESCRIPTIONS[role].oneLine}
                  </TableCell>
                  <TableCell className="text-xs">
                    <ul className="list-disc space-y-0.5 pl-4">
                      {ROLE_DESCRIPTIONS[role].permissions.map((p) => (
                        <li key={p}>{p}</li>
                      ))}
                    </ul>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {/* Assign dialog (AC-2 / AC-4). Shared by assign-other and self-assign;
          self-assign hides the user_id input and uses the caller's identity. */}
      <Dialog
        open={assignOpen}
        onOpenChange={(open) => {
          if (!open) {
            setAssignOpen(false);
            setAssignError(null);
          }
        }}
      >
        <DialogPortal>
          <DialogContent data-testid="assign-user-dialog">
            <DialogHeader>
              <DialogTitle>
                {selfAssign ? "Add me to a tenant" : "Assign user to tenant"}
              </DialogTitle>
              <DialogDescription>
                {selfAssign
                  ? "Grant your own identity a membership + role(s) in a tenant. A re-login is required afterward before the tenant appears in your switcher."
                  : "Grant a user a membership + role(s) in a tenant. The server enforces authority — cross-tenant assignment requires super admin."}
              </DialogDescription>
            </DialogHeader>
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault();
                const v = clientValidateAssign();
                if (v) {
                  setAssignError(v);
                  return;
                }
                setAssignError(null);
                assignMutation.mutate({
                  tenant_id: assignTenantID.trim(),
                  roles: assignRoles,
                  user_id: selfAssign ? undefined : assignUserID.trim(),
                  self_assign: selfAssign,
                });
              }}
            >
              {/* AC-1: user dropdown from the already-loaded list (no second
                  fetch). Hidden in self-assign mode (the caller is the
                  target). */}
              {!selfAssign ? (
                <div className="space-y-1">
                  <label
                    htmlFor="assign-user-id"
                    className="text-xs font-medium text-muted-foreground"
                  >
                    User
                  </label>
                  <Select
                    id="assign-user-id"
                    data-testid="assign-user-select"
                    value={assignUserID}
                    onChange={(e) => setAssignUserID(e.target.value)}
                    aria-describedby={assignErrorId}
                  >
                    <option value="" disabled>
                      Select a user…
                    </option>
                    {userOpts.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </Select>
                </div>
              ) : null}

              {/* AC-2/AC-3/AC-4: tenant field. Super_admin gets the populated
                  cross-tenant dropdown; a tenant-admin's tenant is PINNED to
                  their session tenant (read-only, NOT a chooser — P0-527-1 /
                  P0-479-2). */}
              <div className="space-y-1">
                <label
                  htmlFor="assign-tenant-id"
                  className="text-xs font-medium text-muted-foreground"
                >
                  Tenant
                </label>
                {tenantMode.kind === "chooser" ? (
                  <Select
                    id="assign-tenant-id"
                    data-testid="assign-tenant-select"
                    value={assignTenantID}
                    onChange={(e) => setAssignTenantID(e.target.value)}
                    aria-describedby={assignErrorId}
                  >
                    <option value="" disabled>
                      Select a tenant…
                    </option>
                    {tenantOpts.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </Select>
                ) : tenantMode.kind === "pinned" ? (
                  <Input
                    id="assign-tenant-id"
                    data-testid="assign-tenant-pinned"
                    value={tenantMode.tenantId}
                    readOnly
                    aria-readonly="true"
                    spellCheck={false}
                    className="font-mono text-xs text-muted-foreground"
                  />
                ) : (
                  <Input
                    id="assign-tenant-id"
                    data-testid="assign-tenant-unresolved"
                    value=""
                    readOnly
                    aria-readonly="true"
                    placeholder="resolving your session tenant…"
                  />
                )}
                {tenantMode.kind === "pinned" ? (
                  <p className="text-[11px] text-muted-foreground">
                    Pinned to your tenant. Cross-tenant assignment requires
                    super admin.
                  </p>
                ) : null}
              </div>
              <fieldset className="space-y-2" aria-describedby={assignErrorId}>
                <legend className="text-xs font-medium text-muted-foreground">
                  Roles (select one or more)
                </legend>
                {ROLES.map((role) => {
                  const checked = assignRoles.includes(role);
                  return (
                    <div key={role} className="flex items-center gap-2">
                      <Checkbox
                        id={`assign-role-${role}`}
                        data-testid={`assign-role-${role}`}
                        checked={checked}
                        onCheckedChange={(c) => toggleRole(role, c === true)}
                      />
                      <label
                        htmlFor={`assign-role-${role}`}
                        className="text-sm text-foreground"
                      >
                        <code className="text-xs">{role}</code>{" "}
                        <span className="text-muted-foreground">
                          — {ROLE_DESCRIPTIONS[role].oneLine}
                        </span>
                      </label>
                    </div>
                  );
                })}
              </fieldset>
              {assignError ? (
                <Alert
                  variant="destructive"
                  id="assign-user-error"
                  aria-live="polite"
                >
                  <AlertTitle>Assignment failed</AlertTitle>
                  <AlertDescription data-testid="assign-user-error">
                    {assignError}
                  </AlertDescription>
                </Alert>
              ) : null}
              <DialogFooter>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    setAssignOpen(false);
                    setAssignError(null);
                  }}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  data-testid="assign-user-submit"
                  disabled={
                    assignMutation.isPending || tenantMode.kind === "unresolved"
                  }
                >
                  {assignMutation.isPending
                    ? "Assigning…"
                    : selfAssign
                      ? "Add me"
                      : "Assign"}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </DialogPortal>
      </Dialog>

      {/* AC-4 re-auth notice (self-assign success). P0-479-3: we do NOT
          auto-switch; we explain the re-login requirement and link to it. */}
      <Dialog
        open={reauthNotice !== null}
        onOpenChange={(open) => {
          if (!open) setReauthNotice(null);
        }}
      >
        <DialogPortal>
          <DialogContent data-testid="reauth-notice">
            <DialogHeader>
              <DialogTitle>You&rsquo;ve been added to the tenant</DialogTitle>
              <DialogDescription>
                Your membership + role(s) are saved. One more step before the
                tenant shows up in your switcher.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-3 text-sm">
              <p>
                Your current session token was minted before this membership
                existed, so the new tenant is not yet in your{" "}
                <code>available_tenants</code>. Sign out and sign back in to
                mint a fresh token; the tenant will then appear in the switcher
                and you can switch into it.
              </p>
              {reauthNotice ? (
                <p className="text-muted-foreground">
                  Tenant{" "}
                  <code
                    className="font-mono text-xs"
                    data-testid="reauth-tenant-id"
                  >
                    {reauthNotice.tenant_id}
                  </code>{" "}
                  · roles{" "}
                  <code className="font-mono text-xs">
                    {reauthNotice.roles.join(", ")}
                  </code>
                </p>
              ) : null}
            </div>
            <DialogFooter>
              <Button variant="ghost" onClick={() => setReauthNotice(null)}>
                Later
              </Button>
              <a
                href="/login?from=/admin/users"
                data-testid="reauth-relogin-link"
                className={buttonVariants()}
              >
                Sign out &amp; sign back in
              </a>
            </DialogFooter>
          </DialogContent>
        </DialogPortal>
      </Dialog>

      {/* Revoke confirm (AC-3). */}
      <Dialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setRevokeTarget(null);
            setRevokeError(null);
          }
        }}
      >
        <DialogPortal>
          <DialogContent data-testid="revoke-confirm-dialog">
            <DialogHeader>
              <DialogTitle>Revoke roles</DialogTitle>
              <DialogDescription>
                Remove all of this user&rsquo;s roles in this tenant. The
                membership row is kept (the user can be re-assigned). This
                action is audit-logged server-side.
              </DialogDescription>
            </DialogHeader>
            {revokeTarget ? (
              <div className="space-y-2 text-sm">
                <div>
                  <span className="font-medium">User:</span>{" "}
                  {revokeTarget.display_name || revokeTarget.email}
                </div>
                <div className="font-mono text-xs text-muted-foreground">
                  {revokeTarget.id}
                </div>
                {revokeTarget.tenant_id ? (
                  <div>
                    <span className="font-medium">Tenant:</span>{" "}
                    <code className="font-mono text-xs">
                      {revokeTarget.tenant_id}
                    </code>
                  </div>
                ) : null}
              </div>
            ) : null}
            {revokeError ? (
              <Alert
                variant="destructive"
                id="revoke-user-error"
                aria-live="polite"
              >
                <AlertTitle>Revoke failed</AlertTitle>
                <AlertDescription data-testid="revoke-user-error">
                  {revokeError}
                </AlertDescription>
              </Alert>
            ) : null}
            <DialogFooter>
              <Button
                variant="ghost"
                onClick={() => {
                  setRevokeTarget(null);
                  setRevokeError(null);
                }}
              >
                Cancel
              </Button>
              <Button
                variant="destructive"
                data-testid="revoke-confirm-submit"
                disabled={
                  revokeMutation.isPending ||
                  !revokeTarget ||
                  !resolveRevokeTenant(revokeTarget)
                }
                onClick={() => {
                  if (!revokeTarget) return;
                  const tenant = resolveRevokeTenant(revokeTarget);
                  if (!tenant) {
                    setRevokeError(
                      "Cannot resolve the tenant for this revoke. Reload and retry.",
                    );
                    return;
                  }
                  revokeMutation.mutate({
                    user_id: revokeTarget.id,
                    tenant_id: tenant,
                  });
                }}
              >
                {revokeMutation.isPending ? "Revoking…" : "Revoke roles"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </DialogPortal>
      </Dialog>
    </div>
  );
}
