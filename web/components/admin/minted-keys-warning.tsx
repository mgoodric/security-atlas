// #1953 — the still-live API keys a user minted, shown on the user revoke
// screen.
//
// Admin-minted keys are machine keys (connectors, CI). They outlive the
// admin who minted them (#1947, ADR 0003): revoking the user's roles or
// disabling the user leaves them working. This panel makes that visible
// so the operator can decide, key by key, which should go too.
//
// Informational only. Nothing here runs as a side effect of the user
// revoke; each key revoke is its own click and confirmation, and "Revoke
// all" asks first.
//
// The credentials API lists the caller's own tenant. When the user being
// revoked sits in another tenant (a super_admin's cross-tenant view), the
// panel says so rather than showing an empty, misleading list.

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { kindsLabel } from "@/app/(authed)/settings/allowed-kinds-display";
import type {
  AdminCredential,
  AdminCredentialListResponse,
} from "@/lib/api/admin";

export function mintedKeysQueryKey(userId: string) {
  return ["admin-creds", "minted-by", userId];
}

async function fetchMintedKeys(userId: string): Promise<AdminCredential[]> {
  const res = await fetch(
    `/api/admin/credentials?minted_by=${encodeURIComponent(userId)}`,
    { cache: "no-store" },
  );
  if (!res.ok) throw new Error(`list minted keys: ${res.status}`);
  const body = (await res.json()) as AdminCredentialListResponse;
  return body.items ?? [];
}

async function revokeKey(id: string): Promise<void> {
  const res = await fetch(
    `/api/admin/credentials/${encodeURIComponent(id)}/revoke`,
    { method: "POST" },
  );
  if (!res.ok) throw new Error(`revoke ${id}: ${res.status}`);
}

function formatTime(ts?: string | null, empty = "never"): string {
  return ts ? new Date(ts).toLocaleString() : empty;
}

export function MintedKeysWarning({
  userId,
  userTenantId,
  sessionTenantId,
}: {
  userId: string;
  userTenantId?: string;
  sessionTenantId?: string;
}) {
  const qc = useQueryClient();
  const otherTenant =
    !!userTenantId && !!sessionTenantId && userTenantId !== sessionTenantId;
  const list = useQuery({
    queryKey: mintedKeysQueryKey(userId),
    queryFn: () => fetchMintedKeys(userId),
    enabled: !otherTenant,
  });
  // The key id awaiting confirmation, or "all".
  const [confirming, setConfirming] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const revoke = useMutation({
    mutationFn: async (ids: string[]) => {
      for (const id of ids) await revokeKey(id);
    },
    onSuccess: () => {
      setConfirming(null);
      setError(null);
    },
    onError: (err: Error) => setError(err.message),
    onSettled: () => qc.invalidateQueries({ queryKey: ["admin-creds"] }),
  });

  if (otherTenant) {
    return (
      <p
        className="text-xs text-muted-foreground"
        data-testid="minted-keys-other-tenant"
      >
        API keys this user minted in tenant{" "}
        <code className="font-mono">{userTenantId}</code> keep working after
        this revoke. Review them on that tenant&rsquo;s API keys page.
      </p>
    );
  }
  if (list.isLoading) {
    return (
      <p className="text-xs text-muted-foreground">
        Checking for API keys this user minted…
      </p>
    );
  }
  if (list.isError) {
    return (
      <p className="text-xs text-destructive" data-testid="minted-keys-error">
        Could not check for API keys this user minted: {list.error.message}
      </p>
    );
  }
  const keys = list.data ?? [];
  if (keys.length === 0) {
    return (
      <p
        className="text-xs text-muted-foreground"
        data-testid="minted-keys-none"
      >
        This user minted no API keys that are still live.
      </p>
    );
  }

  return (
    <Alert data-testid="minted-keys-warning">
      <AlertTitle>
        {keys.length === 1
          ? "1 API key this user minted stays live"
          : `${keys.length} API keys this user minted stay live`}
      </AlertTitle>
      <AlertDescription className="space-y-2">
        <p>
          Keys an admin mints are machine keys and keep working after that admin
          is revoked or disabled. Nothing here is revoked automatically. Revoke
          the ones that should not outlive this user.
        </p>
        <ul className="space-y-2">
          {keys.map((k) => (
            <li
              key={k.id}
              className="rounded border p-2 text-xs"
              data-testid={`minted-key-${k.id}`}
            >
              <div className="flex items-center justify-between gap-2">
                <code className="font-mono">
                  {k.id} (…{k.last4})
                </code>
                {confirming === k.id ? (
                  <span className="flex gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setConfirming(null)}
                    >
                      Cancel
                    </Button>
                    <Button
                      size="sm"
                      variant="destructive"
                      data-testid={`minted-key-revoke-confirm-${k.id}`}
                      disabled={revoke.isPending}
                      onClick={() => revoke.mutate([k.id])}
                    >
                      Confirm revoke
                    </Button>
                  </span>
                ) : (
                  <Button
                    size="sm"
                    variant="outline"
                    data-testid={`minted-key-revoke-${k.id}`}
                    disabled={revoke.isPending}
                    onClick={() => setConfirming(k.id)}
                  >
                    Revoke
                  </Button>
                )}
              </div>
              <div className="mt-1 text-muted-foreground">
                Scope: {k.scope_predicate || "all scopes"} ·{" "}
                {kindsLabel(k.allowed_kinds)}
                {k.is_admin ? " · admin" : ""}
              </div>
              <div className="text-muted-foreground">
                Last used: {formatTime(k.last_used_at)} · Expires:{" "}
                {formatTime(k.expires_at, "never")}
              </div>
            </li>
          ))}
        </ul>
        {error ? (
          <p
            className="text-destructive"
            data-testid="minted-keys-revoke-error"
          >
            {error}
          </p>
        ) : null}
        {confirming === "all" ? (
          <div className="flex items-center gap-2">
            <span>
              Revoke all {keys.length} keys? Anything using them stops working.
            </span>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setConfirming(null)}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              variant="destructive"
              data-testid="minted-keys-revoke-all-confirm"
              disabled={revoke.isPending}
              onClick={() => revoke.mutate(keys.map((k) => k.id))}
            >
              {revoke.isPending ? "Revoking…" : "Revoke all"}
            </Button>
          </div>
        ) : (
          <Button
            size="sm"
            variant="outline"
            data-testid="minted-keys-revoke-all"
            disabled={revoke.isPending}
            onClick={() => setConfirming("all")}
          >
            Revoke all
          </Button>
        )}
      </AlertDescription>
    </Alert>
  );
}
