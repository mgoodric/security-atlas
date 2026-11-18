"use client";

// #2106 — the Linked controls section on the risk detail page
// (`/risks/[id]`).
//
// Lists the controls the risk links, each with an Unlink confirmed
// inline, and a "Link controls…" picker (the shared EntityMultiPicker)
// that links the chosen controls one by one. The platform writes each
// link and unlink to the risk's history, refuses both on a closed risk,
// and refuses to unlink the last control of a mitigate risk with no live
// action plan; its message is shown as is. A closed risk shows the list
// without the controls to change it.
//
// #2138: each row shows the control's live state (worst cell, as on
// /controls) from its own query, so one control whose state fails to load
// says so on its row alone. A caller who may not write risks (canEdit
// false) sees the list without Link or Unlink. A control the list cannot
// name reads "Unknown control", never its id.

import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EntityMultiPicker } from "@/components/ui/entity-picker";
import { ConfirmInline, useTriggerRefs } from "@/components/ui/row-actions";
import { useToast } from "@/components/ui/toast";
import { fetchControlState } from "@/lib/api/control-detail";
import { fetchTenantControlsList } from "@/lib/api/controls-list";
import { linkRiskControl, unlinkRiskControl, type Risk } from "@/lib/api/risks";
import type { EntityKind } from "@/lib/entity-picker";
import { enumLabel } from "@/lib/labels/enum-labels";

import { controlLiveState, resultBadgeVariant } from "./control-state";

const CONTROL_KINDS: readonly EntityKind[] = ["control"];

type Props = {
  risk: Pick<Risk, "id" | "linked_control_ids" | "status">;
  canEdit: boolean;
};

export function RiskControls({ risk, canEdit }: Props) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [picking, setPicking] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [confirming, setConfirming] = useState<string | null>(null);
  const unlinkTriggers = useTriggerRefs();
  const [error, setError] = useState<string | null>(null);
  const closed = risk.status === "closed";
  const editable = canEdit && !closed;
  const linked = risk.linked_control_ids ?? [];

  const states = useQueries({
    queries: linked.map((cid) => ({
      queryKey: ["control-state", cid],
      queryFn: () => fetchControlState(cid),
      retry: false,
    })),
  });

  // The same query the picker runs, so names cost no extra request.
  const controls = useQuery({
    queryKey: ["entity-picker", "controls"],
    queryFn: fetchTenantControlsList,
    staleTime: 30_000,
  });
  const titles = new Map((controls.data ?? []).map((c) => [c.id, c.title]));

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["risk", risk.id] });
    await qc.invalidateQueries({ queryKey: ["risk-audit-log", risk.id] });
    await qc.invalidateQueries({ queryKey: ["risks"] });
  };

  const link = useMutation({
    mutationFn: async (ids: string[]) => {
      // One request per control: each is its own logged link.
      for (const cid of ids) await linkRiskControl(risk.id, cid);
    },
    onSuccess: async (_d, ids) => {
      setError(null);
      setSelected([]);
      setPicking(false);
      await refresh();
      toast(
        ids.length === 1 ? "Control linked" : `${ids.length} controls linked`,
      );
    },
    onError: async (err) => {
      setError((err as Error).message);
      await refresh();
    },
  });

  const unlink = useMutation({
    mutationFn: (cid: string) => unlinkRiskControl(risk.id, cid),
    onSuccess: async () => {
      setError(null);
      setConfirming(null);
      await refresh();
      toast("Control unlinked");
    },
    onError: (err) => {
      setConfirming(null);
      setError((err as Error).message);
    },
  });

  const toLink = selected.filter((cid) => !linked.includes(cid));

  return (
    <Card data-testid="risk-controls">
      <CardHeader className="flex flex-row items-center justify-between border-b">
        <CardTitle>Linked controls</CardTitle>
        {editable && !picking ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              setError(null);
              setPicking(true);
            }}
            data-testid="risk-controls-link-open"
          >
            Link controls…
          </Button>
        ) : null}
      </CardHeader>
      <CardContent className="space-y-3">
        {error ? (
          <Alert variant="destructive" data-testid="risk-controls-error">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        {linked.length === 0 ? (
          <p
            className="text-sm text-muted-foreground"
            data-testid="risk-controls-empty"
          >
            No controls are linked to this risk.
          </p>
        ) : (
          <ul className="space-y-1" data-testid="risk-controls-list">
            {linked.map((cid, i) => (
              <li
                key={cid}
                className="flex flex-wrap items-center justify-between gap-2 text-sm"
                data-testid={`risk-controls-row-${cid}`}
              >
                <span className="flex flex-wrap items-center gap-2">
                  <Link
                    href={`/controls/${encodeURIComponent(cid)}`}
                    className="text-primary hover:underline"
                  >
                    {titles.get(cid) ??
                      (controls.isLoading ? "Loading…" : "Unknown control")}
                  </Link>
                  <ControlStateBadge q={states[i]} />
                </span>
                {!editable ? null : confirming === cid ? (
                  <ConfirmInline
                    prompt="Unlink this control from the risk?"
                    confirmLabel="Unlink"
                    ariaLabel="Confirm: unlink control"
                    busy={unlink.isPending}
                    onCancel={() => setConfirming(null)}
                    returnFocus={() => unlinkTriggers.get(cid)}
                    onConfirm={() => unlink.mutate(cid)}
                    data-testid={`risk-controls-unlink-confirm-${cid}`}
                  />
                ) : (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setError(null);
                      setConfirming(cid);
                    }}
                    ref={unlinkTriggers.ref(cid)}
                    data-testid={`risk-controls-unlink-${cid}`}
                  >
                    Unlink
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}

        {picking && editable ? (
          <div className="space-y-2" data-testid="risk-controls-picker-panel">
            <EntityMultiPicker
              kinds={CONTROL_KINDS}
              legend="Controls to link"
              searchPlaceholder="Search controls…"
              testId="risk-controls-picker"
              value={selected}
              onChange={setSelected}
              emptyHint="No controls in this tenant yet — upload a control bundle first."
            />
            <div className="flex gap-2">
              <Button
                type="button"
                size="sm"
                disabled={toLink.length === 0 || link.isPending}
                onClick={() => link.mutate(toLink)}
                data-testid="risk-controls-link-submit"
              >
                {link.isPending
                  ? "Linking…"
                  : toLink.length > 1
                    ? `Link ${toLink.length} controls`
                    : "Link control"}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  setPicking(false);
                  setSelected([]);
                }}
                data-testid="risk-controls-link-cancel"
              >
                Cancel
              </Button>
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

// One control's live state: its worst result and, beside it, its worst
// freshness. Shown as labels, never the enum keys.
function ControlStateBadge({
  q,
}: {
  q: {
    isLoading: boolean;
    isError: boolean;
    data?: { states?: { result: string; freshness_status: string }[] };
  };
}) {
  if (q.isLoading) {
    return <span className="text-xs text-muted-foreground">…</span>;
  }
  if (q.isError) {
    return (
      <span
        className="text-xs text-rose-600"
        data-testid="risk-controls-state-error"
      >
        State unavailable
      </span>
    );
  }
  const live = controlLiveState(q.data?.states ?? []);
  if (!live) {
    return (
      <span
        className="text-xs text-muted-foreground"
        data-testid="risk-controls-state"
        data-result=""
      >
        Not evaluated
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-2">
      <Badge
        variant={resultBadgeVariant(live.result)}
        data-testid="risk-controls-state"
        data-result={live.result}
      >
        {enumLabel("EvaluationState", live.result)}
      </Badge>
      {live.freshness ? (
        <span
          className="text-xs text-muted-foreground"
          data-testid="risk-controls-freshness"
        >
          {enumLabel("FreshnessState", live.freshness)}
        </span>
      ) : null}
    </span>
  );
}
