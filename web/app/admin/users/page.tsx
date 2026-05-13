// Slice 060 — /admin/users (AC-3).
//
// BACKEND GAP: as of slice 060 there is no `/v1/admin/users` HTTP endpoint
// listing users with their RBAC roles. Slice 034 ships the `users` table
// (local + OIDC-provisioned identities); slice 035 ships the role enum
// (admin, grc_engineer, control_owner, auditor, viewer) and the OPA Rego
// gate. The admin REST surface for user/role read+write ships in the
// follow-up slice (060.5).
//
// What this page DOES today:
//   - Renders the role-permission matrix (HITL-reviewable; this is the
//     authoritative source for "what each role can do," and is part of
//     the slice 060 anti-criterion P0 review surface).
//   - Renders a user-list table scaffold so the page lands consistently.
//   - Documents the self-demotion modal flow that the row-action will
//     trigger when wired.

"use client";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { RoleBadge, ROLE_DESCRIPTIONS, ROLES } from "@/components/admin/roles";

export default function UsersPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Users</h1>
        <p className="text-sm text-muted-foreground">
          User accounts with RBAC role assignments. Roles are the coarse
          authorization signal; ABAC predicates layer over them per scope.
        </p>
      </div>

      <Alert>
        <AlertTitle>User list endpoint not yet shipped</AlertTitle>
        <AlertDescription>
          As of slice 060, <code>/v1/admin/users</code> is not on main. Slice
          034 ships the users table; slice 035 ships the role enum + OPA gate.
          The admin user-list and <code>PATCH .../users/{`{id}`}/roles</code>{" "}
          endpoints ship in the follow-up slice. The matrix below is the
          role-permission authority for that backend work &mdash; the HITL
          reviewer signs off on this matrix on the slice 060 PR.
        </AlertDescription>
      </Alert>

      <Card>
        <CardHeader>
          <CardTitle>Role-permission matrix</CardTitle>
          <CardDescription>
            Five canonical RBAC roles per{" "}
            <code>Plans/canvas/09-tech-stack.md</code> §9.5. Permissions noted
            at the coarse level &mdash; fine cuts (e.g. &ldquo;auditor X can
            only see scope cells in audit_period Y&rdquo;) layer on top via ABAC
            predicates.
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

      <Card>
        <CardHeader>
          <CardTitle>Users (placeholder)</CardTitle>
          <CardDescription>
            Will bind to <code>GET /v1/admin/users</code> when shipped. Each row
            will support per-user role assignment via a dropdown. Operators
            changing their OWN role hit an explicit confirmation modal
            (anti-criterion P0).
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="rounded-lg border border-dashed p-12 text-center text-sm text-muted-foreground">
            User list lands with backend slice 060.5.
            <br />
            Today, provision users via <code>
              atlas-cli users create-local
            </code>{" "}
            or the SSO callback.
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Self-demotion safeguard</CardTitle>
          <CardDescription>
            An admin removing their OWN admin role triggers an explicit
            confirmation modal — re-granting admin without DB access is
            impossible in a single-admin deployment.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <p>
            <Badge variant="destructive">P0 anti-criterion</Badge> The UI never
            permits role assignment without explicit confirmation when the
            operator is changing their own role.
          </p>
          <p className="text-muted-foreground">
            Modal copy:{" "}
            <em>
              &ldquo;Removing &lsquo;admin&rsquo; from your own user is
              irreversible without DB access &mdash; confirm?&rdquo;
            </em>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
