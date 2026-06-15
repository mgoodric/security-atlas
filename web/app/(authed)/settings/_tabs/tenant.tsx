"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";

import {
  DEFAULT_GATE_MODE,
  GATE_MODE_OPTIONS,
  GateMode,
  describeGateMode,
  parseGateMode,
} from "../gate-mode";

// --- Section 1.5: Tenant (admin/super_admin only) -------------------------
//
// Slice 144: rename-tenant flow. Admins see a tenant-name input field
// + Save button. Renders only when the caller holds the admin role on
// the CURRENT tenant (per slice 097 D3 pattern: client-side via
// getSessionMe + an upstream-enforced 403 from the platform). The
// platform's authority gate is the canonical guard; the
// hide-when-not-admin is UX-only and not load-bearing.
//
// The section reads the current tenant via `GET /v1/me/tenants` (the
// slice 192 BFF route already shipped) and PATCHes via the slice 144
// BFF route `/api/tenants/[id]`. Errors map 1:1 to the wire response:
// 409 (duplicate name) renders an inline conflict notice; 400 renders
// the upstream error message.

type MeTenantRow = {
  id: string;
  name: string;
  current: boolean;
};

type MeTenantsResponse = {
  tenants: MeTenantRow[];
};

async function fetchMyTenants(): Promise<MeTenantsResponse> {
  const res = await fetch(`/api/me/tenants`, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`list my tenants: ${res.status}`);
  }
  return (await res.json()) as MeTenantsResponse;
}

async function patchTenantName(
  id: string,
  name: string,
): Promise<{ tenant: { name: string } }> {
  const res = await fetch(`/api/tenants/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const body = await res.text();
    let parsed: { error?: string } = {};
    try {
      parsed = JSON.parse(body) as { error?: string };
    } catch {
      // body might be plaintext; fall through
    }
    const err = new Error(
      parsed.error ?? `rename tenant: ${res.status}`,
    ) as Error & {
      status?: number;
    };
    err.status = res.status;
    throw err;
  }
  return (await res.json()) as { tenant: { name: string } };
}

// Slice 613: PATCH the per-tenant control-bundle gate policy via the same
// slice-144 BFF route `patchTenantName` uses. No backend change — slice 608
// taught `PATCH /v1/tenants/{id}` to accept `bundle_gate_mode`. The response
// carries the persisted tenant row including the authoritative
// `bundle_gate_mode`; we parse it fail-safe-toward-strict.
async function patchTenantGateMode(
  id: string,
  bundle_gate_mode: GateMode,
): Promise<{ tenant: { bundle_gate_mode: GateMode } }> {
  const res = await fetch(`/api/tenants/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ bundle_gate_mode }),
  });
  if (!res.ok) {
    const body = await res.text();
    let parsed: { error?: string } = {};
    try {
      parsed = JSON.parse(body) as { error?: string };
    } catch {
      // body might be plaintext; fall through
    }
    const err = new Error(
      parsed.error ?? `set gate policy: ${res.status}`,
    ) as Error & { status?: number };
    err.status = res.status;
    throw err;
  }
  const json = (await res.json()) as {
    tenant?: { bundle_gate_mode?: unknown };
  };
  return {
    tenant: { bundle_gate_mode: parseGateMode(json.tenant?.bundle_gate_mode) },
  };
}

export function TenantSection() {
  const qc = useQueryClient();
  const tenantsQuery = useQuery({
    queryKey: ["settings-my-tenants"],
    queryFn: fetchMyTenants,
  });
  const currentTenant = tenantsQuery.data?.tenants.find((t) => t.current);
  const [draft, setDraft] = useState<string>("");
  const [conflict, setConflict] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  // Seed the draft from the loaded current tenant exactly once after
  // the query resolves. The draft is intentionally allowed to diverge
  // from `currentTenant.name` afterwards so the user can edit without
  // a re-fetch resetting their input. Same post-mount-sync pattern as
  // AppearanceSelector (slice 170 D1) — syncing from a non-React
  // state source (TanStack Query cache) into local component state
  // is the canonical case for the disabled rule.
  useEffect(() => {
    if (currentTenant && draft === "") {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDraft(currentTenant.name);
    }
  }, [currentTenant, draft]);

  const renameMut = useMutation({
    mutationFn: (next: string) =>
      patchTenantName(currentTenant?.id ?? "", next),
    onSuccess: (resp) => {
      setConflict(null);
      setSuccess(`Renamed to "${resp.tenant.name}".`);
      qc.invalidateQueries({ queryKey: ["settings-my-tenants"] });
      // Also invalidate the slice 192 switcher cache.
      qc.invalidateQueries({ queryKey: ["tenant-switcher"] });
    },
    onError: (err: Error & { status?: number }) => {
      setSuccess(null);
      if (err.status === 409) {
        setConflict(
          "Another tenant already uses that name. Pick a different one.",
        );
      } else if (err.status === 403) {
        setConflict("You do not have permission to rename this tenant.");
      } else {
        setConflict(err.message);
      }
    },
  });

  const disabled =
    !currentTenant ||
    renameMut.isPending ||
    draft.trim() === "" ||
    draft.trim() === currentTenant?.name;

  return (
    <Card id="tenant" data-testid="settings-section-tenant">
      <CardHeader>
        <CardTitle>Tenant</CardTitle>
        <CardDescription>
          Rename your current tenant. The new name shows up immediately in the
          tenant switcher for everyone on this tenant.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {tenantsQuery.isLoading ? (
          <Skeleton className="h-10 w-full" />
        ) : tenantsQuery.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load tenant</AlertTitle>
            <AlertDescription>
              {(tenantsQuery.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : !currentTenant ? (
          <Alert>
            <AlertTitle>No current tenant</AlertTitle>
            <AlertDescription>
              You are not currently scoped to a tenant. Sign back in or pick a
              tenant from the switcher first.
            </AlertDescription>
          </Alert>
        ) : (
          <>
            <dl className="grid grid-cols-3 gap-x-4 gap-y-3 text-sm">
              <dt className="text-muted-foreground">Tenant ID</dt>
              <dd
                className="col-span-2 font-mono text-xs text-foreground"
                data-testid="settings-tenant-id"
              >
                {currentTenant.id}
              </dd>
              <dt className="text-muted-foreground">Name</dt>
              <dd className="col-span-2">
                <Input
                  value={draft}
                  onChange={(e) => {
                    setDraft(e.target.value);
                    setConflict(null);
                    setSuccess(null);
                  }}
                  maxLength={64}
                  aria-label="Tenant name"
                  data-testid="settings-tenant-name-input"
                />
              </dd>
            </dl>
            <div className="flex items-center gap-3">
              <Button
                type="button"
                onClick={() => renameMut.mutate(draft.trim())}
                disabled={disabled}
                data-testid="settings-tenant-save-btn"
              >
                {renameMut.isPending ? "Saving…" : "Save name"}
              </Button>
              <p className="text-xs text-muted-foreground">
                Up to 64 bytes. Names are case-insensitive unique across the
                deployment.
              </p>
            </div>
            {conflict ? (
              <Alert variant="destructive">
                <AlertDescription data-testid="settings-tenant-error">
                  {conflict}
                </AlertDescription>
              </Alert>
            ) : null}
            {success ? (
              <Alert>
                <AlertDescription data-testid="settings-tenant-success">
                  {success}
                </AlertDescription>
              </Alert>
            ) : null}
            <GatePolicyControl tenantId={currentTenant.id} />
          </>
        )}
      </CardContent>
    </Card>
  );
}

// Slice 613: per-tenant control-bundle gate-policy control. Lives inside the
// admin-gated TenantSection (the same admin/super_admin authority that gates
// the upstream PATCH). Drives the existing `PATCH /v1/tenants/{id}` surface
// (slice 608) — no new backend.
//
// READ-PATH NOTE (JUDGMENT — slice 613 decisions D1): `main` exposes no GET
// that returns a single tenant's `bundle_gate_mode` to the web layer
// (`/v1/me/tenants` is a JWT-claim directory of id/name/current only; the
// admin tenants LIST omits the column). Adding one would be a backend change,
// out of this web-only slice's scope. So the control pre-selects the
// documented default (`strict` — slice 608 D2, which is exactly what the gate
// enforces for an unchanged tenant) and, after a successful PATCH, reflects
// the authoritative value the PATCH response returns. The pending-vs-persisted
// distinction is surfaced honestly to the operator.
function GatePolicyControl({ tenantId }: { tenantId: string }) {
  // The committed value is what the server last confirmed (PATCH response) or
  // the documented default before any change this session. `selected` is the
  // operator's in-flight choice; it can diverge from `committed` until Save.
  const [committed, setCommitted] = useState<GateMode>(DEFAULT_GATE_MODE);
  const [selected, setSelected] = useState<GateMode>(DEFAULT_GATE_MODE);
  const [saved, setSaved] = useState(false);

  const mut = useMutation({
    mutationFn: (next: GateMode) => patchTenantGateMode(tenantId, next),
    onSuccess: (resp) => {
      const mode = resp.tenant.bundle_gate_mode;
      setCommitted(mode);
      setSelected(mode);
      setSaved(true);
    },
  });

  const dirty = selected !== committed;

  return (
    <div
      className="space-y-3 border-t border-border pt-4"
      data-testid="settings-tenant-gate-policy"
    >
      <div>
        <div className="text-sm font-medium text-foreground">
          Control-bundle upload gate
        </div>
        <p className="text-xs text-muted-foreground">
          Decides how the platform handles a control bundle whose tests fail or
          that ships no tests. Applies to every control-bundle upload on this
          tenant. Until you change it, this tenant uses the default (
          <code>strict</code>).
        </p>
      </div>
      <select
        className="rounded-md border border-border bg-background px-2 py-1 text-sm focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50"
        value={selected}
        disabled={mut.isPending}
        onChange={(e) => {
          setSelected(parseGateMode(e.target.value));
          setSaved(false);
        }}
        data-testid="settings-tenant-gate-policy-select"
        aria-label="Control-bundle upload gate policy"
      >
        {GATE_MODE_OPTIONS.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
      <p
        className="text-xs text-muted-foreground"
        data-testid="settings-tenant-gate-policy-description"
      >
        {describeGateMode(selected)}
      </p>
      <div className="flex items-center gap-3">
        <Button
          type="button"
          size="sm"
          onClick={() => {
            setSaved(false);
            mut.mutate(selected);
          }}
          disabled={!dirty || mut.isPending}
          data-testid="settings-tenant-gate-policy-save"
        >
          {mut.isPending ? "Saving…" : "Save gate policy"}
        </Button>
        {saved && !dirty ? (
          <span
            className="text-xs text-muted-foreground"
            data-testid="settings-tenant-gate-policy-saved"
          >
            Gate policy set to <code>{committed}</code>.
          </span>
        ) : null}
      </div>
      {mut.error ? (
        <Alert variant="destructive">
          <AlertDescription data-testid="settings-tenant-gate-policy-error">
            {(mut.error as Error).message}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
