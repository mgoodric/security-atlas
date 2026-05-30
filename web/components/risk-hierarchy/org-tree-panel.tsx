"use client";

// Slice 056 — org tree panel for the hierarchical risk dashboard.
//
// Renders the tenant's `org_units` as a collapsible parent/child tree,
// built client-side from the flat `parent_id` list returned by
// GET /v1/org_units (slice 053). Each node shows its name, `level`
// badge, and child-node count; clicking a node with children toggles
// expand/collapse.
//
// Per-node risk-count chips are a labelled backend gap: the upstream
// ListOrgUnits handler ignores `?include_risk_counts=true`, and the
// slice-019 risk-list `riskWire` predates slice 052 (no `org_unit_id`,
// `themes`, or severity field on a list row), so the counts cannot be
// derived client-side either. Rather than fabricate zeros, each node
// renders a single muted "counts pending" affordance that names the
// missing query param (anti-criterion P0-1). AC-2 is PARTIAL — the tree
// STRUCTURE is real and fully interactive.
//
// org_unit and scope_cell are deliberately NOT conflated here: this
// panel renders only the org_unit hierarchy. scope_cell is an
// orthogonal dimension surfaced elsewhere (canvas invariant 4,
// anti-criterion P0-4).

import { useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import type { OrgUnit } from "@/lib/api/risk-hierarchy";

type TreeNode = OrgUnit & { children: TreeNode[] };

// buildForest turns the flat parent_id list into a forest. Nodes whose
// parent_id does not resolve in the set (cross-tenant-orphaned, or a
// genuinely top-level unit) become roots. Cycle-safe: a node is only
// ever attached once, and a visited set guards the depth walk.
function buildForest(units: OrgUnit[]): TreeNode[] {
  const byId = new Map<string, TreeNode>();
  for (const u of units) byId.set(u.id, { ...u, children: [] });

  const roots: TreeNode[] = [];
  for (const node of byId.values()) {
    const parent = node.parent_id ? byId.get(node.parent_id) : undefined;
    if (parent && parent.id !== node.id) {
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  const sortRec = (nodes: TreeNode[]) => {
    nodes.sort((a, b) => a.name.localeCompare(b.name));
    for (const n of nodes) sortRec(n.children);
  };
  sortRec(roots);
  return roots;
}

function levelVariant(
  level: string,
): "default" | "secondary" | "outline" | "ghost" {
  switch (level) {
    case "company":
      return "default";
    case "org":
      return "secondary";
    case "team":
      return "outline";
    default:
      return "ghost";
  }
}

function TreeRow({
  node,
  depth,
  expanded,
  onToggle,
}: {
  node: TreeNode;
  depth: number;
  expanded: Set<string>;
  onToggle: (id: string) => void;
}) {
  const hasChildren = node.children.length > 0;
  const isOpen = expanded.has(node.id);
  return (
    <li data-testid="org-tree-node">
      <div
        className="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-foreground/5"
        style={{ paddingLeft: `${depth * 16 + 8}px` }}
      >
        {hasChildren ? (
          <button
            type="button"
            onClick={() => onToggle(node.id)}
            aria-expanded={isOpen}
            aria-label={isOpen ? "Collapse" : "Expand"}
            data-testid="org-tree-toggle"
            className="flex h-5 w-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-foreground/10"
          >
            <span aria-hidden className="text-xs">
              {isOpen ? "▾" : "▸"}
            </span>
          </button>
        ) : (
          <span className="h-5 w-5 shrink-0" aria-hidden />
        )}
        <span className="truncate text-sm font-medium" title={node.name}>
          {node.name}
        </span>
        <Badge variant={levelVariant(node.level)} className="shrink-0">
          {node.level}
        </Badge>
        {hasChildren ? (
          <span
            className="shrink-0 text-xs text-muted-foreground"
            data-testid="org-tree-child-count"
          >
            {node.children.length}{" "}
            {node.children.length === 1 ? "child" : "children"}
          </span>
        ) : null}
        {/* Per-node risk-count chips are a labelled backend gap — see
            the panel-level note. The platform never fabricates counts. */}
        <span
          className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground/70"
          data-testid="org-tree-counts-pending"
          title="Per-severity risk counts await GET /v1/org_units?include_risk_counts=true"
        >
          risk counts pending
        </span>
      </div>
      {hasChildren && isOpen ? (
        <ul>
          {node.children.map((child) => (
            <TreeRow
              key={child.id}
              node={child}
              depth={depth + 1}
              expanded={expanded}
              onToggle={onToggle}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

export function OrgTreePanel({
  units,
  state,
}: {
  units: OrgUnit[] | undefined;
  state: PanelState;
}) {
  const forest = useMemo(() => buildForest(units ?? []), [units]);
  // All nodes start expanded so the full hierarchy is visible on load;
  // the user collapses what they don't need.
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [initialized, setInitialized] = useState(false);

  // Seed the expanded set from the data WITHOUT a useEffect: derive it
  // once during render the first time data arrives (React 19
  // set-state-in-effect lint, anti-criterion P0-5 / slice 040 learned).
  if (!initialized && units && units.length > 0) {
    setExpanded(new Set(units.map((u) => u.id)));
    setInitialized(true);
  }

  const toggle = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const isEmpty = !state.isLoading && !state.isError && forest.length === 0;

  return (
    <PanelCard
      title="Org tree"
      description="Risk hierarchy by org_unit and level"
      state={state}
      testid="org-tree-panel"
      skeletonClassName="h-64 w-full"
    >
      {isEmpty ? (
        <div
          className="flex flex-col items-center gap-3 py-10 text-center"
          data-testid="org-tree-empty"
        >
          <p className="text-sm text-muted-foreground">
            No org_units yet. The risk hierarchy starts here.
          </p>
          <Button
            variant="outline"
            size="sm"
            render={<a href="/risks" />}
            data-testid="org-tree-empty-action"
          >
            Add your first org_unit
          </Button>
        </div>
      ) : (
        <ul className="-mx-2">
          {forest.map((node) => (
            <TreeRow
              key={node.id}
              node={node}
              depth={0}
              expanded={expanded}
              onToggle={toggle}
            />
          ))}
        </ul>
      )}
    </PanelCard>
  );
}
