// Slice 142 — /admin/super-admins management page.
//
// LIST + GRANT + DEMOTE for the slice-142 super_admin management
// surface. The page binds to the BFF at /api/admin/super-admins which
// proxies to the platform's /v1/admin/super-admins.
//
// The list table renders one row per super_admin with:
//   - the name, through <Person> against the list's own names (#2385):
//     the platform names each super admin from their own tenant, so
//     no user ID renders as visible text
//   - granted_at + granted_via (the slice-198 'bootstrap_first_install'
//     value or the slice-142 'manual_grant' value)
//   - per-row actions menu → Demote opens a confirmation dialog;
//     withheld, with the reason, from the last super admin (#2090)
//
// LOAD-BEARING UX: the demote dialog includes a prominent warning that
// the operation is irreversible without DB access. Mirrors the slice-
// 060 self-demotion guard idiom.
//
// 409 RESPONSE HANDLING: when the upstream rejects a demote as "last
// super_admin", we surface the upstream error message in an Alert
// rather than as an opaque red toast. The text comes from the
// upstream's `error` field, set to "Cannot demote the last
// super_admin" by the slice-142 P0-SA-1 safety rail.

"use client";

import { useMemo, useState } from "react";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";
import { EntityPicker } from "@/components/ui/entity-picker";
import type { EntityKind } from "@/lib/entity-picker";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { RowActions } from "@/components/ui/row-actions";
import { lastSuperAdminReason } from "@/components/admin/last-holder";
import { superAdminDirectory } from "@/components/admin/super-admin-directory";
import { Person } from "@/components/ui/person";
import { resolvePerson } from "@/lib/person";
import type { SuperAdminRow } from "@/lib/api/admin";
import { PageShell } from "@/components/shell/page-shell";
import { Term } from "@/components/help/term";
import { Timestamp } from "@/components/ui/timestamp";

type SuperAdminListResponse = { items: SuperAdminRow[]; error?: string };

const GRANT_KINDS: readonly EntityKind[] = ["user"];

export default function SuperAdminsPage() {
  const queryClient = useQueryClient();
  const [grantUserID, setGrantUserID] = useState("");
  const [grantError, setGrantError] = useState<string | null>(null);
  const [demoteTarget, setDemoteTarget] = useState<SuperAdminRow | null>(null);
  const [demoteError, setDemoteError] = useState<string | null>(null);

  const { data, isLoading, isError } = useQuery({
    queryKey: ["admin", "super-admins"],
    queryFn: async () => {
      const res = await fetch("/api/admin/super-admins", {
        cache: "no-store",
      });
      const body = (await res.json()) as SuperAdminListResponse;
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body.items ?? [];
    },
  });
  // #2385: names come from the list (cross-tenant), not /v1/people.
  const directory = useMemo(() => superAdminDirectory(data ?? []), [data]);
  const nameOf = (s: SuperAdminRow) => resolvePerson(s.user_id, directory).text;

  const grantMutation = useMutation({
    mutationFn: async (userID: string) => {
      const res = await fetch("/api/admin/super-admins", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ user_id: userID }),
      });
      const body = (await res.json()) as { error?: string } & SuperAdminRow;
      if (!res.ok) {
        throw new Error(body.error ?? `${res.status} ${res.statusText}`);
      }
      return body;
    },
    onSuccess: async () => {
      setGrantUserID("");
      setGrantError(null);
      await queryClient.invalidateQueries({
        queryKey: ["admin", "super-admins"],
      });
    },
    onError: (err: Error) => {
      setGrantError(err.message);
    },
  });

  const demoteMutation = useMutation({
    mutationFn: async (userID: string) => {
      const res = await fetch(
        `/api/admin/super-admins/${encodeURIComponent(userID)}`,
        { method: "DELETE" },
      );
      if (!res.ok && res.status !== 204) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const body = (await res.json()) as { error?: string };
          if (body?.error) msg = body.error;
        } catch {
          // ignore
        }
        throw new Error(msg);
      }
    },
    onSuccess: async () => {
      setDemoteTarget(null);
      setDemoteError(null);
      await queryClient.invalidateQueries({
        queryKey: ["admin", "super-admins"],
      });
    },
    onError: (err: Error) => {
      setDemoteError(err.message);
    },
  });

  // Slice 363 — form-error association. The Alert mounts conditionally
  // on submit failure; inputs point aria-describedby at the Alert's
  // stable id WHEN it's mounted (undefined otherwise — React strips the
  // attribute). See `web/components/ui/checkbox.tsx` for the convention.
  // The demote dialog Alert (`id="demote-error"`) also carries the
  // stable id + aria-live="polite" so the live region announces the
  // error; the dialog has no input to point aria-describedby at, so
  // the id exists for the live-region linkage only.
  const grantErrorId = grantError ? "grant-super-admin-error" : undefined;
  const demoteReason = lastSuperAdminReason(data ?? []);

  return (
    <PageShell
      title="Super admins"
      data-testid="super-admins-page"
      subtitle={
        <>
          Platform-global identities authorized to create tenants, rename
          tenants, and grant or demote other super admins.{" "}
          <Term id="super-admin">Super admin</Term> is NOT a per-tenant{" "}
          <Term id="role">role</Term>; it does not confer tenant-write authority
          on its own.
        </>
      }
    >
      <Alert>
        <AlertTitle>Last super admin safeguard</AlertTitle>
        <AlertDescription>
          Demoting the last remaining super admin would leave the platform with
          no path to grant new super admins. The backend rejects that operation
          with a 409 Conflict. To rotate the last super admin, first grant a
          successor, then demote the previous holder.
        </AlertDescription>
      </Alert>

      <Card>
        <CardHeader>
          <CardTitle>Grant super admin</CardTitle>
          <CardDescription>
            Pick a person in this tenant, or paste the user ID of someone in
            another tenant. The user must already exist (provisioned via local
            create or SSO login). Granting is idempotent — re-granting an
            existing super admin is a no-op.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (grantUserID.trim() === "") {
                setGrantError("Pick a user to grant super admin to.");
                return;
              }
              setGrantError(null);
              grantMutation.mutate(grantUserID.trim());
            }}
          >
            <div className="flex-1 min-w-[24rem] space-y-1">
              <label
                htmlFor="grant-user-id"
                className="text-xs font-medium text-muted-foreground"
              >
                User
              </label>
              <EntityPicker
                kinds={GRANT_KINDS}
                id="grant-user-id"
                testId="grant-user-id-input"
                placeholder="Search people, or paste a user ID"
                value={grantUserID}
                onChange={(id) => {
                  setGrantUserID(id);
                  setGrantError(null);
                }}
                describedBy={grantErrorId}
              />
            </div>
            <Button
              type="submit"
              data-testid="grant-super-admin-submit"
              disabled={grantMutation.isPending}
            >
              {grantMutation.isPending ? "Granting…" : "Grant super admin"}
            </Button>
          </form>
          {grantError ? (
            <Alert
              variant="destructive"
              className="mt-4"
              id="grant-super-admin-error"
              aria-live="polite"
            >
              <AlertTitle>Grant failed</AlertTitle>
              <AlertDescription data-testid="grant-error">
                {grantError}
              </AlertDescription>
            </Alert>
          ) : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Current super admins</CardTitle>
          <CardDescription>
            Identities with platform-global authority. The bootstrap-tenant
            installer holds a non-revokable provenance value (
            <code>bootstrap_first_install</code>); manually-granted super admins
            carry <code>manual_grant</code>.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="text-sm text-muted-foreground">Loading…</div>
          ) : isError ? (
            <Alert variant="destructive">
              <AlertTitle>Could not load super admins</AlertTitle>
              <AlertDescription>
                The list endpoint returned an error. Refresh to retry.
              </AlertDescription>
            </Alert>
          ) : (data ?? []).length === 0 ? (
            <div className="rounded-lg border border-dashed p-12 text-center text-sm text-muted-foreground">
              No super admins. This should be impossible after first install —
              bootstrap always provisions one. Check the migration ran.
            </div>
          ) : (
            <Table data-testid="super-admins-table">
              <TableHeader>
                <TableRow>
                  <TableHead>Identity</TableHead>
                  <TableHead>Provenance</TableHead>
                  <TableHead>Granted</TableHead>
                  <TableHead className="w-32 text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data ?? []).map((s) => (
                  <TableRow
                    key={s.user_id}
                    data-testid={`super-admin-row-${s.user_id}`}
                  >
                    <TableCell>
                      <Person
                        id={s.user_id}
                        directory={directory}
                        className="font-medium"
                        testId="super-admin-name"
                      />
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          s.granted_via === "bootstrap_first_install"
                            ? "secondary"
                            : "default"
                        }
                      >
                        {s.granted_via}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      <Timestamp value={s.granted_at} style="datetime" />
                    </TableCell>
                    <TableCell className="text-right">
                      {/* #2090: Demote opens the confirm dialog below.
                          It is withheld, with the reason, from the last
                          super admin; the backend's 409 still stands. */}
                      <RowActions
                        label={`Actions for ${nameOf(s)}`}
                        data-testid={`super-admin-actions-${s.user_id}`}
                        items={[
                          {
                            id: "demote",
                            label: "Demote",
                            disabledReason: demoteReason,
                            onSelect: () => {
                              setDemoteError(null);
                              setDemoteTarget(s);
                            },
                            "data-testid": `demote-button-${s.user_id}`,
                          },
                        ]}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog
        open={demoteTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setDemoteTarget(null);
            setDemoteError(null);
          }
        }}
      >
        <DialogPortal>
          <DialogContent data-testid="demote-dialog">
            <DialogHeader>
              <DialogTitle>Demote super admin</DialogTitle>
              <DialogDescription>
                Remove platform-global super admin authority from this identity.
                The action is logged to the audit trail. If this is the last
                super admin, the request will be rejected.
              </DialogDescription>
            </DialogHeader>
            {demoteTarget ? (
              <div className="space-y-2 text-sm">
                <div>
                  <span className="font-medium">Identity:</span>{" "}
                  <Person id={demoteTarget.user_id} directory={directory} />
                </div>
              </div>
            ) : null}
            {demoteError ? (
              <Alert variant="destructive" id="demote-error" aria-live="polite">
                <AlertTitle>Demote failed</AlertTitle>
                <AlertDescription data-testid="demote-error">
                  {demoteError}
                </AlertDescription>
              </Alert>
            ) : null}
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  setDemoteTarget(null);
                  setDemoteError(null);
                }}
              >
                Cancel
              </Button>
              <Button
                variant="destructive"
                data-testid="demote-confirm"
                disabled={demoteMutation.isPending}
                onClick={() => {
                  if (demoteTarget) {
                    demoteMutation.mutate(demoteTarget.user_id);
                  }
                }}
              >
                {demoteMutation.isPending ? "Demoting…" : "Demote"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </DialogPortal>
      </Dialog>
    </PageShell>
  );
}
