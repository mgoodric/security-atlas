"use client";

// #1748 — Parents and Children sections on the risk detail page
// (`/risks/[id]`).
//
// Parents lists the risks this one rolls up to; Children lists the risks
// that roll up to it, each with a Detach control, and an Attach picker that
// offers only risks at a lower level (team < org < company) that are not
// already children. The platform re-checks every rule on attach (level,
// cycle, tenant) and its refusal message is shown as is, so a cycle the
// picker cannot see still surfaces honestly.
//
// Attaching or detaching never changes this risk's stored score; the page
// re-reads the risk afterwards so the roll-up beside the assessed score
// follows the new set of children.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useId, useState, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import {
  attachChildRisks,
  canBeChildOf,
  detachChildRisk,
  fetchRiskChildren,
  fetchRiskParents,
  fetchRisksList,
  type LinkedRisk,
  type Risk,
} from "@/lib/api/risks";

type Props = {
  risk: Pick<Risk, "id" | "level">;
};

export function RiskHierarchyLinks({ risk }: Props) {
  const queryClient = useQueryClient();
  const pickerId = useId();
  const [selected, setSelected] = useState("");
  const [actionError, setActionError] = useState<string | null>(null);

  const children = useQuery({
    queryKey: ["risk-children", risk.id],
    queryFn: () => fetchRiskChildren(risk.id),
  });
  const parents = useQuery({
    queryKey: ["risk-parents", risk.id],
    queryFn: () => fetchRiskParents(risk.id),
  });
  // The picker's candidates. Only a company or org risk can take children,
  // so a team risk never fetches the register.
  const canHaveChildren = risk.level === "org" || risk.level === "company";
  const register = useQuery({
    queryKey: ["risks", "list"],
    queryFn: fetchRisksList,
    enabled: canHaveChildren,
  });

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["risk", risk.id] }),
      queryClient.invalidateQueries({ queryKey: ["risk-children", risk.id] }),
      queryClient.invalidateQueries({ queryKey: ["risk-parents"] }),
    ]);
  };

  const attach = useMutation({
    mutationFn: (childID: string) => attachChildRisks(risk.id, [childID]),
    onSuccess: async () => {
      setSelected("");
      setActionError(null);
      await refresh();
    },
    onError: (err: Error) => setActionError(err.message),
  });
  const detach = useMutation({
    mutationFn: (childID: string) => detachChildRisk(risk.id, childID),
    onSuccess: async () => {
      setActionError(null);
      await refresh();
    },
    onError: (err: Error) => setActionError(err.message),
  });

  const childRows = children.data?.children ?? [];
  const childIDs = new Set(childRows.map((c) => c.id));
  const candidates = (register.data?.risks ?? []).filter(
    (r) => r.id !== risk.id && !childIDs.has(r.id) && canBeChildOf(r, risk),
  );

  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Card data-testid="risk-detail-parents">
        <CardHeader className="border-b">
          <CardTitle>Parents</CardTitle>
        </CardHeader>
        <CardContent>
          {parents.isLoading ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : parents.isError ? (
            <p
              className="text-sm text-rose-600"
              data-testid="risk-detail-parents-error"
            >
              Could not load parent risks.
            </p>
          ) : (parents.data?.parents ?? []).length === 0 ? (
            <p
              className="text-sm text-muted-foreground"
              data-testid="risk-detail-parents-empty"
            >
              This risk does not roll up to another risk.
            </p>
          ) : (
            <ul className="space-y-1.5" data-testid="risk-detail-parents-list">
              {(parents.data?.parents ?? []).map((p) => (
                <LinkedRiskRow
                  key={p.id}
                  link={p}
                  testid="risk-detail-parent"
                />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card data-testid="risk-detail-children">
        <CardHeader className="border-b">
          <CardTitle>Children</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {children.isLoading ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : children.isError ? (
            <p
              className="text-sm text-rose-600"
              data-testid="risk-detail-children-error"
            >
              Could not load child risks.
            </p>
          ) : childRows.length === 0 ? (
            <p
              className="text-sm text-muted-foreground"
              data-testid="risk-detail-children-empty"
            >
              No risks roll up to this one.
            </p>
          ) : (
            <ul className="space-y-1.5" data-testid="risk-detail-children-list">
              {childRows.map((c) => (
                <LinkedRiskRow key={c.id} link={c} testid="risk-detail-child">
                  <Button
                    variant="destructive"
                    size="xs"
                    disabled={detach.isPending}
                    onClick={() => detach.mutate(c.id)}
                    data-testid={`risk-detail-child-detach-${c.id}`}
                    aria-label={`Detach ${c.title}`}
                  >
                    Detach
                  </Button>
                </LinkedRiskRow>
              ))}
            </ul>
          )}

          {canHaveChildren ? (
            <form
              className="flex flex-wrap items-end gap-2"
              data-testid="risk-detail-attach"
              onSubmit={(e) => {
                e.preventDefault();
                if (selected) attach.mutate(selected);
              }}
            >
              <div className="min-w-0 flex-1 space-y-1">
                <label
                  htmlFor={pickerId}
                  className="text-xs text-muted-foreground"
                >
                  Attach a lower-level risk
                </label>
                <Select
                  id={pickerId}
                  value={selected}
                  onChange={(e) => setSelected(e.target.value)}
                  data-testid="risk-detail-attach-select"
                  disabled={register.isLoading || candidates.length === 0}
                >
                  <option value="">
                    {register.isLoading
                      ? "Loading risks…"
                      : candidates.length === 0
                        ? "No eligible risks"
                        : "Choose a risk"}
                  </option>
                  {candidates.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.title} ({r.level})
                    </option>
                  ))}
                </Select>
              </div>
              <Button
                type="submit"
                size="sm"
                disabled={!selected || attach.isPending}
                data-testid="risk-detail-attach-submit"
              >
                Attach
              </Button>
            </form>
          ) : (
            <p
              className="text-xs text-muted-foreground"
              data-testid="risk-detail-attach-unavailable"
            >
              A team risk cannot have children: children sit at a lower level
              than their parent.
            </p>
          )}

          {actionError ? (
            <p
              className="text-sm text-rose-600"
              data-testid="risk-detail-hierarchy-error"
            >
              {actionError}
            </p>
          ) : null}
        </CardContent>
      </Card>
    </div>
  );
}

function LinkedRiskRow({
  link,
  testid,
  children,
}: {
  link: LinkedRisk;
  testid: string;
  children?: ReactNode;
}) {
  return (
    <li
      className="flex flex-wrap items-center gap-2 text-sm"
      data-testid={testid}
      data-risk-id={link.id}
    >
      <Link
        href={`/risks/${encodeURIComponent(link.id)}`}
        className="text-primary hover:underline"
      >
        {link.title}
      </Link>
      <span className="text-xs capitalize text-muted-foreground">
        {link.level}
      </span>
      <span className="font-mono text-xs text-muted-foreground">
        severity {link.severity}
      </span>
      {link.link_source === "rule" ? (
        <span
          className="rounded-md bg-slate-100 px-1.5 py-0.5 text-[11px] text-slate-700 dark:bg-slate-800 dark:text-slate-300"
          title="Linked by an aggregation rule"
        >
          rule
        </span>
      ) : null}
      {children ? <span className="ml-auto">{children}</span> : null}
    </li>
  );
}
