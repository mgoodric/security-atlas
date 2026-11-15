// Slice 060 — /admin/features (AC-5).
//
// Real bind to slice 059: list + PATCH per key. Flags are grouped by
// category. Each toggle requires explicit confirmation, because slice
// 059 + the slice 060 P0 anti-criterion call out that flag flips are
// always intentional admin clicks. Re-enabling (false → true) surfaces
// a modal with a reason; disabling (true → false) goes through the row
// menu and its inline confirm, which also takes a reason (#2090).
//
// P0 anti-criterion: never silently re-enable a flag. The UI only flips
// state inside a confirm handler — there is no auto-flip elsewhere in
// the codebase.

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
import { RowActions } from "@/components/ui/row-actions";
import { Skeleton } from "@/components/ui/skeleton";
import { FeatureFlag, FeatureFlagListResponse } from "@/lib/api/admin";
import { PageShell } from "@/components/shell/page-shell";

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
    <PageShell
      title="Features"
      subtitle={
        <>
          Per-tenant feature flags. Disabling a gated module hides its nav entry
          and makes its routes unreachable; data is preserved and you can
          re-enable at any time.
        </>
      }
    >
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
                onRequestEnable={() =>
                  setPending({ flag: f, nextEnabled: true })
                }
                onDisable={(reason) =>
                  mut.mutate({ key: f.key, enabled: false, reason })
                }
                submitting={mut.isPending}
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
    </PageShell>
  );
}

function FlagRow({
  flag,
  onRequestEnable,
  onDisable,
  submitting,
}: {
  flag: FeatureFlag;
  onRequestEnable: () => void;
  onDisable: (reason: string) => void;
  submitting: boolean;
}) {
  const [reason, setReason] = useState("");
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
        {flag.enabled ? (
          // #2090: disabling is the destructive direction, so it sits in
          // the row menu and confirms inline, with the same optional
          // reason the enable modal takes.
          <RowActions
            label={`Actions for ${flag.key}`}
            data-testid={`flag-actions-${flag.key}`}
            busy={submitting}
            items={[
              {
                id: "disable",
                label: "Disable",
                destructive: true,
                confirmPrompt: `Disable ${flag.key}? Routes gated by this flag will return 404. Existing data is preserved; re-enable any time.`,
                confirmLabel: "Confirm disable",
                confirmDetail: (
                  <label className="space-y-1 text-xs">
                    <span className="font-medium">Reason (optional)</span>
                    <Input
                      value={reason}
                      onChange={(e) => setReason(e.target.value)}
                      placeholder="e.g. tenant doesn't use vendor module"
                      data-testid={`flag-disable-reason-${flag.key}`}
                    />
                  </label>
                ),
                onSelect: () => onDisable(reason),
                "data-testid": `flag-disable-${flag.key}`,
              },
            ]}
          />
        ) : (
          <Button
            size="sm"
            onClick={onRequestEnable}
            data-testid={`flag-enable-${flag.key}`}
          >
            Enable
          </Button>
        )}
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
