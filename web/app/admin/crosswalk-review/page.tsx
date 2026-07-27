// Slice 536b — the crosswalk review/edit surface.
//
// This is the in-product replacement for hand-editing data/crosswalks/*.yaml
// and re-running the importer. It does three things and deliberately not a
// fourth:
//
//   1. Lists a framework version's requirement -> SCF anchor edges with their
//      STRM type, strength, source_attribution, mapping_tier and rationale.
//   2. Surfaces the slice-536a conflict findings against each requirement.
//   3. Lets a reviewer EDIT a mapping's content (relationship_type / strength /
//      rationale) — every edit writing an append-only audit row upstream — and
//      move its TIER through slice 483's endpoint.
//
// The fourth thing it does NOT do is own an approval workflow. Approve/reject
// is `transitionCrosswalkTier`, which posts to slice 483's
// POST /v1/admin/crosswalk-edges/{id}/tier. Slice 536a's scope reconciliation
// (§1.2) established that 483 supersedes slice 536's original "promote
// source_attribution" design; a second workflow here is the slice's explicit
// anti-criterion. Nothing on this page auto-approves: every tier move is a
// button a human presses, and `verified` is reachable only from `under_review`.
//
// Invariant #7: the edit form has no control for the requirement or the anchor.
// An edge's endpoints are not editable here, in the BFF, in the platform
// handler, or in the atlas_app column grant — a reviewer changes what a mapping
// MEANS, never the shape of the graph.
//
// Admin gate: /admin/* is gated server-side by app/admin/layout.tsx, and every
// route this page calls is admin-gated upstream. The UI replicates no
// authorization of its own — it hides illegal tier buttons for clarity, and the
// server refuses them regardless.

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "next/navigation";
import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  RELATIONSHIP_TYPES,
  conflictsForEdge,
  editCrosswalkEdge,
  fetchEdgeAudit,
  fetchReviewQueue,
  highestSeverity,
  nextTiers,
  sortConflicts,
  transitionCrosswalkTier,
  type ConflictSeverity,
  type CrosswalkConflict,
  type CrosswalkEdge,
  type CrosswalkReviewRequirement,
  type MappingTier,
  type RelationshipType,
} from "@/lib/api/crosswalk-review";

const PAGE_SIZE = 25;

// Severity and tier both map onto the semantic status tokens (slice 753) rather
// than ad-hoc colors. `verified` is `pass`, `rejected` is `critical`,
// `under_review` is `progress`, `draft` is neutral — a draft is not a problem,
// it is simply unreviewed, and coloring it as one would train reviewers to read
// the whole backlog as alarming.
const SEVERITY_VARIANT: Record<
  ConflictSeverity,
  "critical" | "warning" | "info"
> = { high: "critical", medium: "warning", low: "info" };

const TIER_VARIANT: Record<
  MappingTier,
  "pass" | "progress" | "critical" | "outline"
> = {
  verified: "pass",
  under_review: "progress",
  rejected: "critical",
  draft: "outline",
};

const TIER_LABEL: Record<MappingTier, string> = {
  draft: "Draft",
  under_review: "Under review",
  verified: "Verified",
  rejected: "Rejected",
};

// The verb a reviewer reads on the button, rather than the tier name. "Approve"
// is the word the operator thinks in; the payload is still the tier value, so
// the vocabulary difference stays in the UI and never in the wire.
const TIER_ACTION_LABEL: Record<MappingTier, string> = {
  draft: "Return to draft",
  under_review: "Send to review",
  verified: "Approve",
  rejected: "Reject",
};

export default function CrosswalkReviewPage() {
  const params = useSearchParams();
  // No framework picker: there is no framework-version LIST endpoint on main
  // (only the promote/revert/migration admin routes), and adding one is backend
  // scope this slice does not own. The id comes from the query string, which is
  // also what makes a review session linkable.
  const versionFromURL = params.get("framework_version_id") ?? "";
  const [versionInput, setVersionInput] = useState(versionFromURL);
  const [versionId, setVersionId] = useState(versionFromURL);
  const [tier, setTier] = useState<MappingTier | "">("");
  const [conflictsOnly, setConflictsOnly] = useState(false);
  const [offset, setOffset] = useState(0);

  const queue = useQuery({
    queryKey: ["crosswalk-review", versionId, tier, conflictsOnly, offset],
    queryFn: () =>
      fetchReviewQueue({
        frameworkVersionId: versionId,
        tier: tier === "" ? undefined : tier,
        conflictsOnly,
        limit: PAGE_SIZE,
        offset,
      }),
    enabled: versionId !== "",
  });

  const total = queue.data?.total ?? 0;
  const shown = queue.data?.requirements.length ?? 0;

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">Crosswalk review</h1>
        <p className="text-sm text-muted-foreground">
          Review, edit and approve the STRM mappings that connect a
          framework&rsquo;s requirements to SCF anchors. Every edit is recorded
          in an append-only audit trail, and no mapping reaches{" "}
          <em>verified</em> without a human approving it here.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Framework version</CardTitle>
          <CardDescription>
            Paste the framework version id to load its review queue.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              setOffset(0);
              setVersionId(versionInput.trim());
            }}
          >
            <div className="min-w-[22rem] flex-1 space-y-1">
              <label
                htmlFor="cw-version"
                className="text-xs font-medium text-muted-foreground"
              >
                Framework version id
              </label>
              <Input
                id="cw-version"
                value={versionInput}
                onChange={(e) => setVersionInput(e.target.value)}
                placeholder="00000000-0000-0000-0000-000000000000"
                data-testid="crosswalk-version-input"
              />
            </div>
            <div className="space-y-1">
              <label
                htmlFor="cw-tier"
                className="text-xs font-medium text-muted-foreground"
              >
                Tier
              </label>
              <Select
                id="cw-tier"
                value={tier}
                onChange={(e) => {
                  setOffset(0);
                  setTier(e.target.value as MappingTier | "");
                }}
                data-testid="crosswalk-tier-filter"
              >
                <option value="">All tiers</option>
                <option value="draft">Draft</option>
                <option value="under_review">Under review</option>
                <option value="verified">Verified</option>
                <option value="rejected">Rejected</option>
              </Select>
            </div>
            <label className="flex items-center gap-2 pb-1 text-sm">
              <input
                type="checkbox"
                checked={conflictsOnly}
                onChange={(e) => {
                  setOffset(0);
                  setConflictsOnly(e.target.checked);
                }}
                data-testid="crosswalk-conflicts-only"
              />
              Conflicts only
            </label>
            <Button type="submit" data-testid="crosswalk-load">
              Load queue
            </Button>
          </form>
        </CardContent>
      </Card>

      {versionId === "" ? (
        <Alert>
          <AlertTitle>No framework version selected</AlertTitle>
          <AlertDescription>
            Enter a framework version id above to start reviewing its crosswalk
            mappings.
          </AlertDescription>
        </Alert>
      ) : queue.isLoading ? (
        <div className="space-y-2" data-testid="crosswalk-loading">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : queue.error ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load the review queue</AlertTitle>
          <AlertDescription data-testid="crosswalk-queue-error">
            {(queue.error as Error).message}
          </AlertDescription>
        </Alert>
      ) : (
        <>
          <div
            className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground"
            data-testid="crosswalk-queue-summary"
          >
            <span>
              {shown} of {total} requirements
            </span>
            <span>·</span>
            <span>
              {queue.data?.conflict_count ?? 0} conflicts on this page
            </span>
          </div>

          {shown === 0 ? (
            <Alert>
              <AlertTitle>Nothing to review here</AlertTitle>
              <AlertDescription>
                No requirement on this page matches the current filters.
              </AlertDescription>
            </Alert>
          ) : (
            queue.data?.requirements.map((req) => (
              <RequirementCard key={req.id} requirement={req} />
            ))
          )}

          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              data-testid="crosswalk-prev-page"
            >
              Previous
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={offset + PAGE_SIZE >= total}
              onClick={() => setOffset(offset + PAGE_SIZE)}
              data-testid="crosswalk-next-page"
            >
              Next
            </Button>
          </div>
        </>
      )}
    </div>
  );
}

function RequirementCard({
  requirement,
}: {
  requirement: CrosswalkReviewRequirement;
}) {
  const worst = highestSeverity(requirement.conflicts);
  return (
    <Card data-testid="crosswalk-requirement">
      <CardHeader>
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-base">
            <span data-testid="crosswalk-requirement-code">
              {requirement.code}
            </span>{" "}
            <span className="font-normal text-muted-foreground">
              {requirement.title}
            </span>
          </CardTitle>
          {worst ? (
            <Badge
              variant={SEVERITY_VARIANT[worst]}
              data-testid="crosswalk-requirement-severity"
            >
              {requirement.conflicts.length} conflict
              {requirement.conflicts.length === 1 ? "" : "s"}
            </Badge>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <ConflictList conflicts={requirement.conflicts} />
        {requirement.edges.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            This requirement has no mappings. It can never be covered by
            evidence until one is added.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Anchor</TableHead>
                <TableHead>Relationship</TableHead>
                <TableHead>Strength</TableHead>
                <TableHead>Provenance</TableHead>
                <TableHead>Tier</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {requirement.edges.map((edge) => (
                <EdgeRow
                  key={edge.id}
                  edge={edge}
                  conflicts={conflictsForEdge(requirement.conflicts, edge.id)}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

// ConflictList renders the requirement's findings above its mappings. They sit
// at requirement level because several 536a heuristics are statements about a
// SET of edges — and the orphaned-requirement family is a statement about the
// ABSENCE of one, which no per-row view could show at all.
function ConflictList({ conflicts }: { conflicts: CrosswalkConflict[] }) {
  if (conflicts.length === 0) return null;
  return (
    <ul className="space-y-1" data-testid="crosswalk-conflicts">
      {sortConflicts(conflicts).map((c, i) => (
        <li
          key={`${c.reason}-${i}`}
          className="flex flex-wrap items-center gap-2 text-sm"
        >
          <Badge variant={SEVERITY_VARIANT[c.severity]}>{c.severity}</Badge>
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            {c.reason}
          </code>
          <span className="text-muted-foreground">{c.detail}</span>
        </li>
      ))}
    </ul>
  );
}

function EdgeRow({
  edge,
  conflicts,
}: {
  edge: CrosswalkEdge;
  conflicts: CrosswalkConflict[];
}) {
  const [panel, setPanel] = useState<"none" | "edit" | "audit">("none");
  const tier = edge.mapping_tier;

  return (
    <>
      <TableRow data-testid="crosswalk-edge-row" data-edge-id={edge.id}>
        <TableCell>
          <div className="font-mono text-xs">{edge.anchor_scf_id}</div>
          <div className="text-xs text-muted-foreground">
            {edge.anchor_family} · {edge.anchor_title}
          </div>
          {conflicts.length > 0 ? (
            <div className="mt-1 flex flex-wrap gap-1">
              {conflicts.map((c, i) => (
                <Badge
                  key={`${c.reason}-${i}`}
                  variant={SEVERITY_VARIANT[c.severity]}
                  data-testid="crosswalk-edge-conflict"
                >
                  {c.reason}
                </Badge>
              ))}
            </div>
          ) : null}
        </TableCell>
        <TableCell className="text-sm">{edge.relationship_type}</TableCell>
        <TableCell className="text-sm tabular-nums">
          {edge.strength.toFixed(2)}
        </TableCell>
        {/* Provenance is displayed, never edited: source_attribution records
            where a mapping came from (ADR 0018 / 483 P0-483-3) and promoting it
            would falsify history. */}
        <TableCell className="text-xs text-muted-foreground">
          {edge.source_attribution}
        </TableCell>
        <TableCell>
          <Badge variant={TIER_VARIANT[tier]} data-testid="crosswalk-edge-tier">
            {TIER_LABEL[tier]}
          </Badge>
        </TableCell>
        <TableCell className="text-right">
          <div className="flex justify-end gap-1">
            <Button
              size="xs"
              variant="outline"
              onClick={() => setPanel(panel === "edit" ? "none" : "edit")}
              data-testid="crosswalk-edit-toggle"
            >
              Edit
            </Button>
            <Button
              size="xs"
              variant="ghost"
              onClick={() => setPanel(panel === "audit" ? "none" : "audit")}
              data-testid="crosswalk-audit-toggle"
            >
              History
            </Button>
          </div>
        </TableCell>
      </TableRow>
      {panel !== "none" ? (
        <TableRow>
          <TableCell colSpan={6} className="bg-muted/30">
            {panel === "edit" ? (
              <div className="space-y-4">
                <EditForm edge={edge} onDone={() => setPanel("none")} />
                <TierActions edge={edge} />
              </div>
            ) : (
              <AuditTrail edgeId={edge.id} />
            )}
          </TableCell>
        </TableRow>
      ) : null}
    </>
  );
}

function EditForm({
  edge,
  onDone,
}: {
  edge: CrosswalkEdge;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [relationshipType, setRelationshipType] = useState<RelationshipType>(
    edge.relationship_type,
  );
  const [strength, setStrength] = useState(String(edge.strength));
  const [rationale, setRationale] = useState(edge.rationale);
  const [note, setNote] = useState("");

  const mutation = useMutation({
    mutationFn: () =>
      editCrosswalkEdge(edge.id, {
        relationship_type: relationshipType,
        strength: Number(strength),
        rationale,
        note,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["crosswalk-review"] });
      void qc.invalidateQueries({ queryKey: ["crosswalk-audit", edge.id] });
    },
  });

  const rejected = edge.mapping_tier === "rejected";

  return (
    <form
      className="space-y-3"
      data-testid="crosswalk-edit-form"
      onSubmit={(e) => {
        e.preventDefault();
        mutation.mutate();
      }}
    >
      {/* D-536b-1 made an edit to a verified mapping demote it back to
          under_review. Warning BEFORE the edit rather than only reporting it
          after is the honest ordering: the reviewer chooses knowing the cost. */}
      {edge.mapping_tier === "verified" ? (
        <Alert variant="destructive" data-testid="crosswalk-demotion-warning">
          <AlertTitle>Editing will send this mapping back to review</AlertTitle>
          <AlertDescription>
            This mapping is verified. Changing its content means it is no longer
            the mapping that was verified, so saving will move it to{" "}
            <em>under review</em> and record the demotion in the audit trail. It
            will need approving again.
          </AlertDescription>
        </Alert>
      ) : null}
      {rejected ? (
        <Alert data-testid="crosswalk-rejected-notice">
          <AlertTitle>This mapping was rejected</AlertTitle>
          <AlertDescription>
            A rejected mapping is terminal and cannot be edited.
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label
            htmlFor={`rel-${edge.id}`}
            className="text-xs font-medium text-muted-foreground"
          >
            Relationship type
          </label>
          <Select
            id={`rel-${edge.id}`}
            value={relationshipType}
            disabled={rejected}
            onChange={(e) =>
              setRelationshipType(e.target.value as RelationshipType)
            }
            data-testid="crosswalk-edit-relationship"
          >
            {RELATIONSHIP_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </Select>
        </div>
        <div className="space-y-1">
          <label
            htmlFor={`str-${edge.id}`}
            className="text-xs font-medium text-muted-foreground"
          >
            Strength (0&ndash;1)
          </label>
          <Input
            id={`str-${edge.id}`}
            type="number"
            min={0}
            max={1}
            step={0.05}
            value={strength}
            disabled={rejected}
            onChange={(e) => setStrength(e.target.value)}
            data-testid="crosswalk-edit-strength"
          />
        </div>
      </div>

      <div className="space-y-1">
        <label
          htmlFor={`rat-${edge.id}`}
          className="text-xs font-medium text-muted-foreground"
        >
          Rationale — why this mapping holds
        </label>
        <textarea
          id={`rat-${edge.id}`}
          rows={3}
          maxLength={4096}
          value={rationale}
          disabled={rejected}
          onChange={(e) => setRationale(e.target.value)}
          className="w-full rounded-md border bg-background px-2 py-1 text-sm"
          data-testid="crosswalk-edit-rationale"
        />
      </div>

      <div className="space-y-1">
        <label
          htmlFor={`note-${edge.id}`}
          className="text-xs font-medium text-muted-foreground"
        >
          Change note — why you are changing it (recorded in the audit trail)
        </label>
        <textarea
          id={`note-${edge.id}`}
          rows={2}
          maxLength={4096}
          value={note}
          disabled={rejected}
          onChange={(e) => setNote(e.target.value)}
          className="w-full rounded-md border bg-background px-2 py-1 text-sm"
          data-testid="crosswalk-edit-note"
        />
      </div>

      {mutation.error ? (
        <Alert variant="destructive">
          <AlertDescription data-testid="crosswalk-edit-error">
            {(mutation.error as Error).message}
          </AlertDescription>
        </Alert>
      ) : null}
      {mutation.data ? (
        <Alert data-testid="crosswalk-edit-success">
          <AlertTitle>Edit recorded</AlertTitle>
          <AlertDescription>
            Audit row <code className="text-xs">{mutation.data.edit_id}</code>{" "}
            written.
            {mutation.data.tier_demoted_to
              ? ` This mapping moved from ${mutation.data.tier_demoted_from} to ${mutation.data.tier_demoted_to}.`
              : ""}
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="flex gap-2">
        <Button
          type="submit"
          size="sm"
          disabled={rejected || mutation.isPending}
          data-testid="crosswalk-edit-save"
        >
          {mutation.isPending ? "Saving…" : "Save edit"}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onDone}>
          Close
        </Button>
      </div>
    </form>
  );
}

// TierActions is the approve/reject control. It renders one button per
// transition slice 483's machine permits from the edge's CURRENT tier —
// `nextTiers` mirrors internal/crosswalktier.legalTransitions. The mirror is a
// clarity affordance only: the server refuses an illegal move 422 regardless of
// what this renders, and there is no bulk action, because approving in bulk
// would let a reviewer verify mappings they never read.
function TierActions({ edge }: { edge: CrosswalkEdge }) {
  const qc = useQueryClient();
  const [note, setNote] = useState("");
  const [pending, setPending] = useState<MappingTier | null>(null);

  const mutation = useMutation({
    mutationFn: (tier: MappingTier) =>
      transitionCrosswalkTier(edge.id, tier, note),
    onSuccess: () => {
      setPending(null);
      setNote("");
      void qc.invalidateQueries({ queryKey: ["crosswalk-review"] });
      void qc.invalidateQueries({ queryKey: ["crosswalk-audit", edge.id] });
    },
  });

  const options = nextTiers(edge.mapping_tier);
  if (options.length === 0) {
    return (
      <p
        className="text-sm text-muted-foreground"
        data-testid="crosswalk-tier-terminal"
      >
        This mapping is in a terminal tier — no further review action is
        available.
      </p>
    );
  }

  return (
    <div
      className="space-y-2 border-t pt-3"
      data-testid="crosswalk-tier-actions"
    >
      <p className="text-xs font-medium text-muted-foreground">
        Review decision (slice 483 tier transition — recorded with your identity
        and this note)
      </p>
      {pending === null ? (
        <div className="flex flex-wrap gap-2">
          {options.map((t) => (
            <Button
              key={t}
              size="sm"
              variant={t === "rejected" ? "destructive" : "outline"}
              onClick={() => setPending(t)}
              data-testid={`crosswalk-tier-${t}`}
            >
              {TIER_ACTION_LABEL[t]}
            </Button>
          ))}
        </div>
      ) : (
        // A confirm step rather than a one-click fire. A tier move is
        // audit-binding — the note is the reviewer's justification and belongs
        // to the decision, not to a follow-up edit.
        <div className="space-y-2" data-testid="crosswalk-tier-confirm">
          <p className="text-sm">
            Move this mapping to <strong>{TIER_LABEL[pending]}</strong>?
          </p>
          <textarea
            rows={2}
            maxLength={4096}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="Why (optional, recorded in the audit trail)"
            className="w-full rounded-md border bg-background px-2 py-1 text-sm"
            data-testid="crosswalk-tier-note"
          />
          <div className="flex gap-2">
            <Button
              size="sm"
              disabled={mutation.isPending}
              onClick={() => mutation.mutate(pending)}
              data-testid="crosswalk-tier-confirm-submit"
            >
              {mutation.isPending ? "Recording…" : "Confirm"}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setPending(null)}
              data-testid="crosswalk-tier-cancel"
            >
              Cancel
            </Button>
          </div>
        </div>
      )}
      {mutation.error ? (
        <Alert variant="destructive">
          <AlertDescription data-testid="crosswalk-tier-error">
            {(mutation.error as Error).message}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}

// AuditTrail is the in-product proof that nothing went unrecorded: both the
// content edits (536b) and the tier transitions (483) for one edge.
function AuditTrail({ edgeId }: { edgeId: string }) {
  const audit = useQuery({
    queryKey: ["crosswalk-audit", edgeId],
    queryFn: () => fetchEdgeAudit(edgeId),
  });

  if (audit.isLoading) return <Skeleton className="h-16 w-full" />;
  if (audit.error) {
    return (
      <Alert variant="destructive">
        <AlertDescription data-testid="crosswalk-audit-error">
          {(audit.error as Error).message}
        </AlertDescription>
      </Alert>
    );
  }

  const data = audit.data;
  const empty =
    (data?.content_edits.length ?? 0) === 0 &&
    (data?.tier_transitions.length ?? 0) === 0;

  return (
    <div className="space-y-3 text-sm" data-testid="crosswalk-audit-trail">
      {empty ? (
        <p className="text-muted-foreground">
          No edits or review decisions recorded for this mapping yet.
        </p>
      ) : null}
      {(data?.content_edits.length ?? 0) > 0 ? (
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">
            Content edits ({data?.content_edit_count})
          </p>
          <ul className="space-y-1">
            {data?.content_edits.map((e) => (
              <li key={e.id} data-testid="crosswalk-audit-edit">
                <span className="text-muted-foreground">{e.created_at}</span>{" "}
                {e.from.relationship_type} @ {e.from.strength.toFixed(2)} &rarr;{" "}
                {e.to.relationship_type} @ {e.to.strength.toFixed(2)}
                {e.note ? ` — ${e.note}` : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {(data?.tier_transitions.length ?? 0) > 0 ? (
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">
            Review decisions
          </p>
          <ul className="space-y-1">
            {data?.tier_transitions.map((t, i) => (
              <li
                key={`${t.created_at}-${i}`}
                data-testid="crosswalk-audit-tier"
              >
                <span className="text-muted-foreground">{t.created_at}</span>{" "}
                {TIER_LABEL[t.from_tier]} &rarr; {TIER_LABEL[t.to_tier]}
                {t.note ? ` — ${t.note}` : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
