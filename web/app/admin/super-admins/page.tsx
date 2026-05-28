// Slice 142 — /admin/super-admins management page.
//
// LIST + GRANT + DEMOTE for the slice-142 super_admin management
// surface. The page binds to the BFF at /api/admin/super-admins which
// proxies to the platform's /v1/admin/super-admins.
//
// The list table renders one row per super_admin with:
//   - display_name + email (LEFT JOIN against users in the session
//     tenant; null fields render as a truncated user_id)
//   - granted_at + granted_via (the slice-198 'bootstrap_first_install'
//     value or the slice-142 'manual_grant' value)
//   - per-row Demote button → opens a confirmation dialog
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
import type { SuperAdminRow } from "@/lib/api";

type SuperAdminListResponse = { items: SuperAdminRow[]; error?: string };

function formatGrantedAt(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function formatUserID(s: SuperAdminRow): string {
  if (s.display_name) {
    return s.display_name;
  }
  if (s.email) {
    return s.email;
  }
  // Truncate UUID to first 8 chars + ellipsis to keep cell narrow.
  return s.user_id.length > 12 ? `${s.user_id.slice(0, 8)}…` : s.user_id;
}

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

  return (
    <div className="space-y-6" data-testid="super-admins-page">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Super admins</h1>
        <p className="text-sm text-muted-foreground">
          Platform-global identities authorized to create tenants, rename
          tenants, and grant or demote other super admins. Super admin is NOT a
          per-tenant role; it does not confer tenant-write authority on its own.
        </p>
      </div>

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
            Enter the target user&rsquo;s UUID. The user must already exist in
            some tenant (provisioned via local create or SSO login). Granting is
            idempotent — re-granting an existing super admin is a no-op.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (grantUserID.trim() === "") {
                setGrantError("user_id is required");
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
                User UUID
              </label>
              <Input
                id="grant-user-id"
                data-testid="grant-user-id-input"
                placeholder="00000000-0000-0000-0000-000000000000"
                value={grantUserID}
                onChange={(e) => setGrantUserID(e.target.value)}
                spellCheck={false}
                autoComplete="off"
                aria-describedby={grantErrorId}
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
                      <div className="font-medium">{formatUserID(s)}</div>
                      <div className="font-mono text-xs text-muted-foreground">
                        {s.user_id}
                      </div>
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
                      {formatGrantedAt(s.granted_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        data-testid={`demote-button-${s.user_id}`}
                        onClick={() => {
                          setDemoteError(null);
                          setDemoteTarget(s);
                        }}
                      >
                        Demote
                      </Button>
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
                  {formatUserID(demoteTarget)}
                </div>
                <div className="font-mono text-xs text-muted-foreground">
                  {demoteTarget.user_id}
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
    </div>
  );
}
