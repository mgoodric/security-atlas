// Slice 060 — /admin/features (AC-5).
//
// Real bind to slice 059: list + PATCH per key. Flags are grouped by
// category. Each toggle requires explicit confirmation; re-enabling
// (false → true) and disabling (true → false) both surface a modal,
// because slice 059 + the slice 060 P0 anti-criterion call out that
// flag flips are always intentional admin clicks.
//
// P0 anti-criterion: never silently re-enable a flag. The UI only flips
// state inside the modal's onConfirm handler — there is no auto-flip
// elsewhere in the codebase.

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

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
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { FeatureFlag, FeatureFlagListResponse } from "@/lib/api";

async function fetchFlags(): Promise<FeatureFlag[]> {
  const res = await fetch(`/api/admin/features`);
  if (!res.ok) throw new Error(`list: ${res.status}`);
  const body = (await res.json()) as FeatureFlagListResponse;
  return body.items ?? [];
}

async function patchFlag({
  key,
  enabled,
  reason,
}: {
  key: string;
  enabled: boolean;
  reason: string;
}): Promise<void> {
  const res = await fetch(`/api/admin/features/${encodeURIComponent(key)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled, reason }),
  });
  if (!res.ok) throw new Error(`patch: ${res.status}`);
}

export default function FeaturesPage() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["admin-features"], queryFn: fetchFlags });
  const [pending, setPending] = useState<{
    flag: FeatureFlag;
    nextEnabled: boolean;
  } | null>(null);

  const mut = useMutation({
    mutationFn: patchFlag,
    onSuccess: () => {
      setPending(null);
      qc.invalidateQueries({ queryKey: ["admin-features"] });
      qc.invalidateQueries({ queryKey: ["admin-features-overview"] });
    },
  });

  const grouped = groupByCategory(list.data ?? []);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Features</h1>
        <p className="text-sm text-muted-foreground">
          Per-tenant feature flags. Disabling a module hides its routes; data is
          preserved and re-enable any time.
        </p>
      </div>

      {list.isLoading ? <ListSkeleton /> : null}
      {list.error ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load feature flags</AlertTitle>
          <AlertDescription>{(list.error as Error).message}</AlertDescription>
        </Alert>
      ) : null}

      {Object.entries(grouped).map(([category, flags]) => (
        <Card key={category}>
          <CardHeader>
            <CardTitle className="capitalize">{category}</CardTitle>
            <CardDescription>
              {flags.length} flag{flags.length === 1 ? "" : "s"} in this
              category.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {flags.map((f) => (
              <FlagRow
                key={f.key}
                flag={f}
                onRequestToggle={(nextEnabled) =>
                  setPending({ flag: f, nextEnabled })
                }
              />
            ))}
          </CardContent>
        </Card>
      ))}

      {pending ? (
        <ConfirmFlipModal
          flag={pending.flag}
          nextEnabled={pending.nextEnabled}
          onCancel={() => setPending(null)}
          onConfirm={(reason) =>
            mut.mutate({
              key: pending.flag.key,
              enabled: pending.nextEnabled,
              reason,
            })
          }
          submitting={mut.isPending}
        />
      ) : null}
    </div>
  );
}

function FlagRow({
  flag,
  onRequestToggle,
}: {
  flag: FeatureFlag;
  onRequestToggle: (nextEnabled: boolean) => void;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border p-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <code className="font-mono text-sm">{flag.key}</code>
          {flag.enabled ? (
            <Badge variant="secondary">on</Badge>
          ) : (
            <Badge variant="outline">off</Badge>
          )}
          {flag.has_override ? <Badge variant="outline">override</Badge> : null}
        </div>
        <p className="text-xs text-muted-foreground">{flag.description}</p>
        {flag.last_changed_at ? (
          <p className="text-xs text-muted-foreground">
            Last changed {flag.last_changed_at}
            {flag.last_changed_by ? ` by ${flag.last_changed_by}` : null}
          </p>
        ) : null}
      </div>
      <div className="flex gap-2 sm:shrink-0">
        <Button
          size="sm"
          variant={flag.enabled ? "destructive" : "default"}
          onClick={() => onRequestToggle(!flag.enabled)}
        >
          {flag.enabled ? "Disable" : "Enable"}
        </Button>
      </div>
    </div>
  );
}

function ConfirmFlipModal({
  flag,
  nextEnabled,
  onCancel,
  onConfirm,
  submitting,
}: {
  flag: FeatureFlag;
  nextEnabled: boolean;
  onCancel: () => void;
  onConfirm: (reason: string) => void;
  submitting: boolean;
}) {
  const [reason, setReason] = useState("");
  const direction = nextEnabled ? "Enable" : "Disable";
  const consequence = nextEnabled
    ? "Routes gated by this flag will return live data again. Re-evaluation may take a few seconds for cached queries to drop."
    : "Routes gated by this flag will return 404. Existing data is preserved; re-enable any time.";

  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
    >
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>
            {direction} <code className="font-mono">{flag.key}</code>?
          </CardTitle>
          <CardDescription>{flag.description}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p>{consequence}</p>
          <label className="space-y-1 text-xs">
            <span className="font-medium">Reason (optional)</span>
            <Input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="e.g. tenant doesn't use vendor module"
            />
          </label>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onCancel} disabled={submitting}>
              Cancel
            </Button>
            <Button
              variant={nextEnabled ? "default" : "destructive"}
              onClick={() => onConfirm(reason)}
              disabled={submitting}
            >
              {submitting
                ? `${direction.slice(0, -1)}ing…`
                : `Confirm ${direction.toLowerCase()}`}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-12 w-full" />
      ))}
    </div>
  );
}

function groupByCategory(flags: FeatureFlag[]): Record<string, FeatureFlag[]> {
  const out: Record<string, FeatureFlag[]> = {};
  for (const f of flags) {
    const k = f.category || "uncategorized";
    if (!out[k]) out[k] = [];
    out[k].push(f);
  }
  for (const k of Object.keys(out)) {
    out[k].sort((a, b) => a.key.localeCompare(b.key));
  }
  return out;
}
