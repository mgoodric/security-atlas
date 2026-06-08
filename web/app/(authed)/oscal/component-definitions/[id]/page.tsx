"use client";

// Slice 589 — /oscal/component-definitions/[id] vendor-claims view.
//
// Lists one imported component-definition's vendor CLAIMS (joined to their
// owning component) and lets a grc_engineer disposition each claim:
// accept / reject / needs-info.
//
// THE LOAD-BEARING BOUNDARY: a vendor claim is an ASSERTION, not
// platform-verified evidence (canvas invariant #2 / P0-512-1). Accepting a
// claim records that the operator credits the vendor's assertion — it does
// NOT auto-satisfy a control. The UI labels every claim as a vendor claim and
// never implies acceptance produces control coverage. The unmapped flag
// surfaces the slice-512 `scf_anchor_id IS NULL` claims that still need an
// SCF-anchor mapping.
//
// Slice 620 adds the operator MAPPING affordance: an unmapped claim
// (scf_anchor_id IS NULL) gets a searchable SCF-anchor picker. Mapping sets
// the human-approved crosswalk (requirement -> SCF anchor only, invariant #7);
// it does NOT fabricate control coverage — the claim stays a claim. On success
// the "Unmapped to SCF" badge clears and the mapped anchor shows.
//
// Data source:
//   GET   /api/oscal/component-definitions/[id]                 (claims)
//   POST  /api/oscal/component-claims/[claimId]/disposition     (disposition)
//   PATCH /api/oscal/component-claims/[claimId]/scf-anchor       (mapping)
//   GET   /api/anchors                                           (picker catalog)

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { use, useMemo, useState } from "react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { bffControlFetch } from "@/lib/api/_shared";
import type { Anchor } from "@/lib/api/anchors";
import type {
  ClaimStatus,
  ComponentDefinitionDetail,
  Disposition,
  DispositionResult,
  MapScfAnchorResult,
} from "@/lib/api/oscal-components";

function statusBadgeVariant(
  status: ClaimStatus,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "accepted":
      return "default";
    case "rejected":
      return "destructive";
    case "needs_info":
      return "outline";
    default:
      return "secondary";
  }
}

// filterAnchors narrows the SCF-anchor catalog by a case-insensitive substring
// match over scf_id + family + name. Exported for unit testing the picker
// search behaviour without standing up the React tree. A blank query caps the
// list at `cap` so the picker never renders the full ~1,400-anchor catalog.
export function filterAnchors(
  anchors: Anchor[],
  query: string,
  cap = 25,
): Anchor[] {
  const q = query.trim().toLowerCase();
  if (q === "") return anchors.slice(0, cap);
  return anchors
    .filter(
      (a) =>
        a.scf_id.toLowerCase().includes(q) ||
        a.family.toLowerCase().includes(q) ||
        a.name.toLowerCase().includes(q),
    )
    .slice(0, cap);
}

function statusLabel(status: ClaimStatus): string {
  switch (status) {
    case "accepted":
      return "Accepted";
    case "rejected":
      return "Rejected";
    case "needs_info":
      return "Needs info";
    default:
      return "Asserted";
  }
}

export default function ComponentDefinitionDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const qc = useQueryClient();
  const [note, setNote] = useState<Record<string, string>>({});
  const [actionError, setActionError] = useState<string | null>(null);
  // Per-claim SCF-anchor picker search term (only used for unmapped claims).
  const [anchorQuery, setAnchorQuery] = useState<Record<string, string>>({});

  const detailQ = useQuery({
    queryKey: ["oscal", "component-definition", id],
    queryFn: () =>
      bffControlFetch<ComponentDefinitionDetail>(
        `/api/oscal/component-definitions/${id}`,
      ),
  });

  // The bundled SCF-anchor catalog backing the picker. Fetched once; the
  // picker filters client-side. Only loaded lazily is unnecessary here — the
  // catalog is small and shared across every unmapped claim on the page.
  const anchorsQ = useQuery({
    queryKey: ["anchors", "catalog"],
    queryFn: () =>
      bffControlFetch<{ anchors: Anchor[] }>(`/api/anchors`).then(
        (b) => b.anchors,
      ),
  });

  const dispositionM = useMutation({
    mutationFn: async (args: {
      claimId: string;
      disposition: Disposition;
    }): Promise<DispositionResult> => {
      const res = await fetch(
        `/api/oscal/component-claims/${args.claimId}/disposition`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            disposition: args.disposition,
            note: note[args.claimId] ?? "",
          }),
        },
      );
      if (!res.ok) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const j = (await res.json()) as { error?: string };
          if (j.error) msg = j.error;
        } catch {
          // keep status line
        }
        throw new Error(msg);
      }
      return (await res.json()) as DispositionResult;
    },
    onSuccess: () => {
      setActionError(null);
      void qc.invalidateQueries({
        queryKey: ["oscal", "component-definition", id],
      });
    },
    onError: (err: Error) => setActionError(err.message),
  });

  // Slice 620 — map an unmapped claim to a canonical SCF anchor. On success
  // the detail query is invalidated so the claim re-renders mapped (the
  // "Unmapped to SCF" badge clears + the anchor shows).
  const mapM = useMutation({
    mutationFn: async (args: {
      claimId: string;
      scfAnchorId: string;
    }): Promise<MapScfAnchorResult> => {
      const res = await fetch(
        `/api/oscal/component-claims/${args.claimId}/scf-anchor`,
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ scf_anchor_id: args.scfAnchorId }),
        },
      );
      if (!res.ok) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const j = (await res.json()) as { error?: string };
          if (j.error) msg = j.error;
        } catch {
          // keep status line
        }
        throw new Error(msg);
      }
      return (await res.json()) as MapScfAnchorResult;
    },
    onSuccess: () => {
      setActionError(null);
      void qc.invalidateQueries({
        queryKey: ["oscal", "component-definition", id],
      });
    },
    onError: (err: Error) => setActionError(err.message),
  });

  if (detailQ.isLoading) {
    return (
      <div className="space-y-3" data-testid="component-definition-loading">
        <Skeleton className="h-8 w-1/3" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (detailQ.isError || !detailQ.data) {
    return (
      <Alert variant="destructive" data-testid="component-definition-error">
        Could not load this component-definition.
      </Alert>
    );
  }

  const d = detailQ.data;

  return (
    <div className="space-y-6" data-testid="component-definition-detail">
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold">
          {d.source_label || d.catalog_title || d.id}
        </h1>
        <p className="text-sm text-muted-foreground">
          {d.catalog_title} · OSCAL {d.oscal_version} · imported by{" "}
          {d.imported_by}
        </p>
        <p
          className="text-xs text-muted-foreground"
          data-testid="vendor-claim-disclaimer"
        >
          Every row below is a vendor assertion, not platform-verified evidence.
          Accepting a claim credits the vendor&apos;s statement; it does not
          satisfy a control on its own.
        </p>
      </header>

      {actionError && (
        <Alert variant="destructive" data-testid="disposition-error">
          {actionError}
        </Alert>
      )}

      {d.claims.length === 0 ? (
        <div
          className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground"
          data-testid="claims-empty"
        >
          This component-definition has no vendor claims.
        </div>
      ) : (
        <ul className="space-y-3" data-testid="claims-list">
          {d.claims.map((c) => (
            <li
              key={c.id}
              className="space-y-3 rounded-lg border p-4"
              data-testid="claim-row"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span
                  className="font-mono text-sm"
                  data-testid="claim-control-id"
                >
                  {c.control_id}
                </span>
                <Badge variant="secondary" data-testid="claim-vendor-badge">
                  Vendor claim
                </Badge>
                <Badge
                  variant={statusBadgeVariant(c.claim_status)}
                  data-testid="claim-status-badge"
                >
                  {statusLabel(c.claim_status)}
                </Badge>
                {c.unmapped && (
                  <Badge variant="outline" data-testid="claim-unmapped-badge">
                    Unmapped to SCF
                  </Badge>
                )}
                {c.scf_anchor_id && (
                  <Badge variant="outline" data-testid="claim-scf-anchor">
                    {c.scf_anchor_id}
                  </Badge>
                )}
              </div>

              <div className="text-xs text-muted-foreground">
                {c.component_title} · {c.component_type}
              </div>
              <p className="text-sm" data-testid="claim-statement">
                {c.statement || "(no vendor statement)"}
              </p>

              {c.dispositioned_by && (
                <p
                  className="text-xs text-muted-foreground"
                  data-testid="claim-disposition-meta"
                >
                  {statusLabel(c.claim_status)} by {c.dispositioned_by}
                  {c.disposition_note ? ` — ${c.disposition_note}` : ""}
                </p>
              )}

              <textarea
                className="w-full rounded-md border p-2 text-sm"
                rows={2}
                placeholder="Disposition note (optional)"
                data-testid="claim-note-input"
                value={note[c.id] ?? ""}
                onChange={(e) =>
                  setNote((prev) => ({ ...prev, [c.id]: e.target.value }))
                }
              />

              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  data-testid="claim-accept"
                  disabled={dispositionM.isPending}
                  onClick={() =>
                    dispositionM.mutate({
                      claimId: c.id,
                      disposition: "accept",
                    })
                  }
                >
                  Accept
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  data-testid="claim-reject"
                  disabled={dispositionM.isPending}
                  onClick={() =>
                    dispositionM.mutate({
                      claimId: c.id,
                      disposition: "reject",
                    })
                  }
                >
                  Reject
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  data-testid="claim-needs-info"
                  disabled={dispositionM.isPending}
                  onClick={() =>
                    dispositionM.mutate({
                      claimId: c.id,
                      disposition: "needs-info",
                    })
                  }
                >
                  Needs info
                </Button>
              </div>

              {c.unmapped && (
                <ScfAnchorPicker
                  claimId={c.id}
                  anchors={anchorsQ.data ?? []}
                  loading={anchorsQ.isLoading}
                  query={anchorQuery[c.id] ?? ""}
                  onQueryChange={(v) =>
                    setAnchorQuery((prev) => ({ ...prev, [c.id]: v }))
                  }
                  onMap={(scfAnchorId) =>
                    mapM.mutate({ claimId: c.id, scfAnchorId })
                  }
                  pending={mapM.isPending}
                />
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ScfAnchorPicker is the slice-620 SCF-anchor mapping affordance for ONE
// unmapped claim. It renders a search box + a short filtered list of bundled
// SCF anchors; clicking one PATCHes the mapping. The picker NEVER lets the
// operator type a free-form anchor — every option resolves to a real
// scf_anchors row (invariant #7), and the platform validates again server-side.
function ScfAnchorPicker({
  claimId,
  anchors,
  loading,
  query,
  onQueryChange,
  onMap,
  pending,
}: {
  claimId: string;
  anchors: Anchor[];
  loading: boolean;
  query: string;
  onQueryChange: (v: string) => void;
  onMap: (scfAnchorId: string) => void;
  pending: boolean;
}) {
  const filtered = useMemo(
    () => filterAnchors(anchors, query),
    [anchors, query],
  );

  return (
    <div
      className="space-y-2 rounded-md border border-dashed p-3"
      data-testid="scf-anchor-picker"
    >
      <p className="text-xs font-medium text-muted-foreground">
        Map this claim to a canonical SCF anchor. Setting the crosswalk does not
        satisfy a control on its own.
      </p>
      <input
        type="text"
        className="w-full rounded-md border p-2 text-sm"
        placeholder="Search SCF anchors (code, family, name)…"
        data-testid="scf-anchor-search"
        value={query}
        onChange={(e) => onQueryChange(e.target.value)}
      />
      {loading ? (
        <p className="text-xs text-muted-foreground">Loading anchors…</p>
      ) : filtered.length === 0 ? (
        <p
          className="text-xs text-muted-foreground"
          data-testid="scf-anchor-empty"
        >
          No matching SCF anchors.
        </p>
      ) : (
        <ul
          className="max-h-48 space-y-1 overflow-y-auto"
          data-testid="scf-anchor-options"
        >
          {filtered.map((a) => (
            <li key={a.id}>
              <button
                type="button"
                className="flex w-full flex-col rounded-md border px-2 py-1 text-left text-sm hover:bg-accent disabled:opacity-50"
                data-testid="scf-anchor-option"
                data-scf-id={a.scf_id}
                disabled={pending}
                onClick={() => onMap(a.scf_id)}
              >
                <span className="font-mono text-xs">{a.scf_id}</span>
                <span className="text-xs text-muted-foreground">
                  {a.family} · {a.name}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <input
        type="hidden"
        data-testid="scf-anchor-claim-id"
        value={claimId}
        readOnly
      />
    </div>
  );
}
