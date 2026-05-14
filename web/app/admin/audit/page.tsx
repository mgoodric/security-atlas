// Slice 060 — /admin/audit (AC-6).
//
// BACKEND GAP: as of slice 060 there is NO unified `/v1/admin/audit-log`
// HTTP endpoint. The seven underlying log tables ship across slices
// 013, 018, 021, 022, 035, 036, and 059 — each with their own (mostly
// internal) write surface. Some tables have entity-scoped read APIs
// (e.g. `/v1/exceptions/{id}/audit-log`); none of them expose a unified
// paginated tenant-wide read.
//
// What this page DOES today:
//   - Surfaces the gap and lists the seven log tables that need
//     unioning, with the slice that owns each.
//   - Provides a static schema description of the unified row shape
//     so the backend slice has a contract to bind to.
//   - Shows the filter UI scaffold so the layout doesn't move when the
//     backend lands.

"use client";

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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

const SOURCE_TABLES: Array<{
  table: string;
  slice: string;
  what: string;
}> = [
  {
    table: "decision_audit_log",
    slice: "035",
    what: "OPA authorization decisions (allow/deny by role).",
  },
  {
    table: "evidence_audit_log",
    slice: "013",
    what: "Evidence ingestion events (push, dedupe, schema check).",
  },
  {
    table: "exception_audit_log",
    slice: "021",
    what: "Exception / waiver workflow state transitions.",
  },
  {
    table: "feature_flag_audit_log",
    slice: "059",
    what: "Feature-flag flips (actor, reason, before → after).",
  },
  {
    table: "policy_audit_log",
    slice: "022",
    what: "Policy library transitions (submit, approve, publish).",
  },
  {
    table: "framework_scope_workflow_log",
    slice: "018",
    what: "FrameworkScope state transitions (draft → activated).",
  },
  {
    table: "artifact_access_log",
    slice: "036",
    what: "Signed-URL grants and artifact reads.",
  },
];

export default function AuditLogPage() {
  const [actorFilter, setActorFilter] = useState("");
  const [eventFilter, setEventFilter] = useState("");

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <p className="text-sm text-muted-foreground">
          A single paginated view across every audit-log surface. Filter by
          actor or event type; each row links to the relevant entity.
        </p>
      </div>

      <Alert>
        <AlertTitle>Unified read endpoint not yet shipped</AlertTitle>
        <AlertDescription>
          The seven log tables below each have their own write path, but no
          `/v1/admin/audit-log` endpoint exists on main as of slice 060. The
          backend slice (tentatively 060.5) unions them with a single paginated
          read. This UI scaffold ships first so the backend has the wire-shape
          contract to bind to.
        </AlertDescription>
      </Alert>

      <Card>
        <CardHeader>
          <CardTitle>Filters (scaffold)</CardTitle>
          <CardDescription>
            Will bind to the unified read endpoint. Free-text actor + event kind
            + time-range cover the typical investigation flows.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 sm:grid-cols-3">
            <Input
              value={actorFilter}
              onChange={(e) => setActorFilter(e.target.value)}
              placeholder="actor (user id or credential)"
              disabled
            />
            <Input
              value={eventFilter}
              onChange={(e) => setEventFilter(e.target.value)}
              placeholder='event kind (e.g. "policy.publish")'
              disabled
            />
            <Button variant="outline" disabled>
              Apply (backend pending)
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Source tables</CardTitle>
          <CardDescription>
            Seven tables that the unified read endpoint will union. Each row in
            the future UI carries <code>source_table</code> so an investigator
            can trace back.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Table</TableHead>
                <TableHead className="w-20">Slice</TableHead>
                <TableHead>What it captures</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {SOURCE_TABLES.map((t) => (
                <TableRow key={t.table}>
                  <TableCell>
                    <code className="font-mono text-xs">{t.table}</code>
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary">#{t.slice}</Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {t.what}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Wire-shape contract for slice 060.5</CardTitle>
          <CardDescription>
            The unified read should return rows shaped like the JSON below.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <pre className="overflow-x-auto rounded bg-foreground/5 p-3 text-xs">
            {`{
  "items": [
    {
      "id": "uuid",
      "source_table": "evidence_audit_log",
      "tenant_id": "uuid",
      "actor": "user:uuid | credential:key_<hex>",
      "event": "evidence.push",
      "subject_kind": "evidence_record",
      "subject_id": "uuid",
      "occurred_at": "RFC3339",
      "payload": { "...source-table-specific...": true }
    }
  ],
  "next_page_token": "opaque cursor or empty"
}`}
          </pre>
        </CardContent>
      </Card>
    </div>
  );
}
