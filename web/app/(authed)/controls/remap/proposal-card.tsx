"use client";

// #1778 — one remap proposal on the review page: the legacy anchor beside
// the proposed anchor or anchors, the affected control, the affected
// crosswalk edges (super-admins only), and the reviewer's decision.
//
// SCF titles are verbatim and descriptions are our own words; both render
// as React text, never as HTML.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Timestamp } from "@/components/ui/timestamp";
import {
  decideRemap,
  lookupAnchors,
  searchAnchors,
  type EdgeRemapProposal,
  type RemapAction,
  type RemapProposal,
  type SCFAnchor,
} from "@/lib/api/remap";
import { REMAP_CLASS_LABELS, needsExplicitAnchor } from "@/lib/remap-review";

// A lookup entry is undefined while loading and null when the release has
// no anchor with that code; the two must not read the same.
function anchorName(a: SCFAnchor | null | undefined): string {
  if (a === undefined) return "…";
  if (a === null) return "(not in this release)";
  return a.name;
}

function AnchorText({
  code,
  release,
  anchor,
  testId,
}: {
  code: string;
  release: string;
  anchor: SCFAnchor | null | undefined;
  testId: string;
}) {
  return (
    <div data-testid={testId}>
      <span className="font-mono text-xs font-semibold">
        {code}@{release}
      </span>
      {anchor === undefined ? null : anchor === null ? (
        <span className="block text-xs text-muted-foreground">
          Not in this release.
        </span>
      ) : (
        <>
          <span className="block text-sm" data-testid={`${testId}-title`}>
            {anchor.name}
          </span>
          {anchor.description ? (
            <span className="block text-xs text-muted-foreground">
              {anchor.description}
            </span>
          ) : null}
        </>
      )}
    </div>
  );
}

export function ProposalCard({
  proposal: p,
  edges,
}: {
  proposal: RemapProposal;
  // null: the caller is not a super-admin, so edges are not shown at all.
  edges: EdgeRemapProposal[] | null;
}) {
  const qc = useQueryClient();
  const explicit = needsExplicitAnchor(p.class);
  const [chosen, setChosen] = useState<string>("");
  const [q, setQ] = useState("");
  const [submittedQ, setSubmittedQ] = useState("");

  const legacyQ = useQuery({
    queryKey: ["scf-anchors", p.legacy_release, [p.legacy_scf_id]],
    queryFn: () => lookupAnchors(p.legacy_release, [p.legacy_scf_id]),
    staleTime: Infinity,
  });
  // The successors, plus (for a reused ID) what the code means now.
  const targetCodes = [
    ...new Set([
      ...p.successor_scf_ids,
      ...(p.class === "reused_id" ? [p.legacy_scf_id] : []),
    ]),
  ].slice(0, 10);
  const targetQ = useQuery({
    queryKey: ["scf-anchors", p.target_release, targetCodes],
    queryFn: () => lookupAnchors(p.target_release, targetCodes),
    enabled: targetCodes.length > 0,
    staleTime: Infinity,
  });
  const searchQ = useQuery({
    queryKey: ["scf-anchor-search", p.target_release, submittedQ],
    queryFn: () => searchAnchors(p.target_release, submittedQ),
    enabled: explicit && submittedQ.length >= 2,
  });

  const decide = useMutation({
    mutationFn: (action: RemapAction) =>
      decideRemap(
        p.id,
        action,
        action === "approve" && explicit ? { scf_id: chosen } : {},
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["remap-proposals"] }),
  });

  const legacy = legacyQ.data?.anchors[p.legacy_scf_id];
  const target = targetQ.data?.anchors ?? {};
  const reusedNow = target[p.legacy_scf_id];

  return (
    <Card data-testid="remap-proposal" data-proposal-id={p.id}>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2 text-base">
          <Link
            href={`/controls/${encodeURIComponent(p.control_id)}`}
            className="underline underline-offset-3"
            data-testid="remap-proposal-control"
          >
            {p.title}
          </Link>
          <span className="font-mono text-xs text-muted-foreground">
            {p.bundle_id}
          </span>
          {p.class === "reused_id" ? (
            <Badge variant="warning" data-testid="remap-reused-flag">
              Reused ID
            </Badge>
          ) : (
            <Badge variant="outline">{REMAP_CLASS_LABELS[p.class]}</Badge>
          )}
          {p.status === "rejected" ? (
            <Badge variant="secondary">Rejected earlier</Badge>
          ) : null}
        </CardTitle>
        <p className="text-xs text-muted-foreground">
          Proposed <Timestamp value={p.created_at} />
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
          <div>
            <p className="mb-1 text-xs font-medium uppercase text-muted-foreground">
              Current anchor
            </p>
            <AnchorText
              code={p.legacy_scf_id}
              release={p.legacy_release}
              anchor={legacy}
              testId="remap-legacy-anchor"
            />
          </div>
          <div>
            <p className="mb-1 text-xs font-medium uppercase text-muted-foreground">
              Proposed in {p.target_release}
            </p>
            {p.successor_scf_ids.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No successor. Search the release below, or keep the legacy
                anchor.
              </p>
            ) : (
              <ul className="space-y-2" data-testid="remap-successors">
                {p.successor_scf_ids.map((code) => (
                  <li key={code} className="flex items-start gap-2">
                    {explicit ? (
                      <input
                        type="radio"
                        name={`remap-${p.id}`}
                        className="mt-1"
                        aria-label={`Choose ${code}`}
                        data-testid="remap-successor-choice"
                        checked={chosen === code}
                        onChange={() => setChosen(code)}
                      />
                    ) : null}
                    <AnchorText
                      code={code}
                      release={p.target_release}
                      anchor={target[code]}
                      testId="remap-successor"
                    />
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>

        {p.class === "reused_id" ? (
          <Alert data-testid="remap-reused-meanings">
            <AlertDescription className="space-y-1">
              <p>
                {p.legacy_scf_id} is a reused ID. In {p.legacy_release} it meant{" "}
                <strong data-testid="remap-reused-old">
                  {anchorName(legacy)}
                </strong>
                .
              </p>
              <p>
                In {p.target_release} it means{" "}
                <strong data-testid="remap-reused-new">
                  {anchorName(reusedNow)}
                </strong>
                {reusedNow?.legacy_scf_id
                  ? `, which continues ${reusedNow.legacy_scf_id}`
                  : ""}
                . Choose the anchor that carries this control&apos;s meaning; it
                is never approved by default.
              </p>
            </AlertDescription>
          </Alert>
        ) : null}

        {explicit ? (
          <div className="space-y-2">
            <form
              className="flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                setSubmittedQ(q.trim());
              }}
            >
              <Input
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder={`Search ${p.target_release} by code or title`}
                aria-label="Search anchors"
                data-testid="remap-anchor-search"
              />
              <Button type="submit" variant="outline" size="sm">
                Search
              </Button>
            </form>
            {searchQ.isError ? (
              <p className="text-xs text-destructive">
                {(searchQ.error as Error).message}
              </p>
            ) : null}
            {searchQ.data ? (
              <ul className="space-y-1" data-testid="remap-anchor-results">
                {searchQ.data.anchors.length === 0 ? (
                  <li className="text-xs text-muted-foreground">No matches.</li>
                ) : (
                  searchQ.data.anchors.map((a) => (
                    <li key={a.scf_id}>
                      <button
                        type="button"
                        className="text-left text-sm hover:underline"
                        data-testid="remap-anchor-result"
                        onClick={() => setChosen(a.scf_id)}
                      >
                        <span className="font-mono text-xs font-semibold">
                          {a.scf_id}
                        </span>{" "}
                        {a.name}
                      </button>
                    </li>
                  ))
                )}
              </ul>
            ) : null}
            <p className="text-sm" data-testid="remap-chosen">
              {chosen ? (
                <>
                  Re-anchor on{" "}
                  <span className="font-mono font-semibold">{chosen}</span>
                </>
              ) : (
                <span className="text-muted-foreground">No anchor chosen.</span>
              )}
            </p>
          </div>
        ) : null}

        {edges !== null ? (
          <div data-testid="remap-edges">
            <p className="mb-1 text-xs font-medium uppercase text-muted-foreground">
              Crosswalk edges on this anchor ({edges.length})
            </p>
            {edges.length === 0 ? (
              <p className="text-xs text-muted-foreground">None open.</p>
            ) : (
              <ul className="space-y-1 text-xs">
                {edges.map((e) => (
                  <li key={e.id} data-testid="remap-edge">
                    <span className="font-mono">
                      {e.framework_slug}:{e.framework_version}{" "}
                      {e.requirement_code}
                    </span>{" "}
                    <span className="text-muted-foreground">
                      {e.relationship_type}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : null}

        {decide.isError ? (
          <Alert variant="destructive" data-testid="remap-decide-error">
            <AlertDescription>
              {(decide.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : null}

        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            data-testid="remap-approve"
            disabled={decide.isPending || (explicit && !chosen)}
            onClick={() => decide.mutate("approve")}
          >
            Approve
          </Button>
          {p.status === "open" ? (
            <Button
              size="sm"
              variant="outline"
              data-testid="remap-reject"
              disabled={decide.isPending}
              onClick={() => decide.mutate("reject")}
            >
              Reject
            </Button>
          ) : null}
          <Button
            size="sm"
            variant="ghost"
            data-testid="remap-keep-legacy"
            disabled={decide.isPending}
            onClick={() => decide.mutate("keep-legacy")}
          >
            Keep legacy anchor
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
