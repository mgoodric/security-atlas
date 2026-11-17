// #2312 — repo-wide ratchet: a BFF route under a dynamic `[param]`
// segment refuses a non-UUID path id before it builds the upstream URL.
//
// encodeURIComponent leaves `..` and `.` intact, so a route that
// interpolates `[id]` into its upstream path lets `/api/x/%2E%2E/verb`
// normalise upstream to `/v1/verb`. It needs the caller's own bearer, so
// it grants no privilege, but `rejectNonUUID` (lib/api/bff.ts) closes it
// for the cost of one line.
//
// Every route.ts under a dynamic segment must call `rejectNonUUID`, or be
// listed in KNOWN_UNGUARDED below. The list only shrinks:
//   * a new unguarded route fails ("not guarded");
//   * a listed route that now has the guard, or no longer exists, fails
//     too ("stale entry"), so the fix that guards it also removes it.
//
// Some listed routes take a param that is not a UUID at all (a section
// `[key]`, a `[transition]` verb, a category `[ref]`, an evidence id).
// Those need their own allowlist check rather than rejectNonUUID; they
// stay listed until one is written.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

const API_DIR = dirname(fileURLToPath(import.meta.url));

const KNOWN_UNGUARDED = new Set([
  "action-plans/[id]/controls/[controlId]/route.ts",
  "action-plans/[id]/risks/[riskId]/route.ts",
  "action-plans/[id]/route.ts",
  "action-plans/[id]/verify/route.ts",
  "admin/credentials/[id]/revoke/route.ts",
  "admin/credentials/[id]/rotate/route.ts",
  "admin/features/[key]/route.ts",
  "admin/super-admins/[user_id]/route.ts",
  "admin/users/[id]/module-access/route.ts",
  "anchors/[id]/requirements/route.ts",
  "audit/periods/[id]/controls/[controlId]/evidence-summary/route.ts",
  "audit/populations/[id]/route.ts",
  "audit/samples/[id]/annotations/route.ts",
  "audit/samples/[id]/route.ts",
  "audit/walkthroughs/[id]/attachments/route.ts",
  "audit/walkthroughs/[id]/route.ts",
  "board-packs/[id]/markdown/route.ts",
  "board-packs/[id]/pdf/route.ts",
  "board-packs/[id]/publish/route.ts",
  "board-packs/[id]/route.ts",
  "board-packs/[id]/sections/[key]/approve/route.ts",
  "board-packs/[id]/sections/[key]/route.ts",
  "controls/[id]/action-plans/route.ts",
  "controls/[id]/attest-form/route.ts",
  "controls/[id]/attestations/route.ts",
  "controls/[id]/coverage/route.ts",
  "controls/[id]/effective-scope/route.ts",
  "controls/[id]/effectiveness/route.ts",
  "controls/[id]/evidence-summary/route.ts",
  "controls/[id]/gap-explanation/route.ts",
  "controls/[id]/history/route.ts",
  "controls/[id]/policies/route.ts",
  "controls/[id]/risks/route.ts",
  "controls/[id]/state/route.ts",
  "controls/checklist/[generationId]/export/route.ts",
  "controls/checklist/sections/[id]/approve/route.ts",
  "csf/profiles/[kind]/route.ts",
  "evidence/[id]/route.ts",
  "framework-scopes/[id]/[transition]/route.ts",
  "framework-scopes/[id]/applicability/[requirement_id]/route.ts",
  "framework-scopes/[id]/applicability/bulk/route.ts",
  "framework-scopes/[id]/applicability/diff/route.ts",
  "framework-scopes/[id]/applicability/import/route.ts",
  "framework-scopes/[id]/applicability/route.ts",
  "framework-scopes/[id]/route.ts",
  "me/sessions/[id]/route.ts",
  "metrics/[id]/inputs/route.ts",
  "metrics/[id]/observations/route.ts",
  "metrics/[id]/route.ts",
  "metrics/[id]/target/route.ts",
  "oscal/component-claims/[id]/disposition/route.ts",
  "oscal/component-claims/[id]/scf-anchor/route.ts",
  "oscal/component-definitions/[id]/route.ts",
  "personnel-security/checklist-items/[id]/complete/route.ts",
  "personnel-security/checklists/[id]/route.ts",
  "questionnaires/[id]/answer-runs/[runId]/route.ts",
  "questionnaires/[id]/answer-runs/route.ts",
  "questionnaires/[id]/answers/[qid]/ai-approve/route.ts",
  "questionnaires/[id]/answers/[qid]/ai-reject/route.ts",
  "questionnaires/[id]/answers/[qid]/ai-suggest/route.ts",
  "questionnaires/[id]/answers/[qid]/route.ts",
  "questionnaires/[id]/export-pdf/route.ts",
  "questionnaires/[id]/import-excel/route.ts",
  "questionnaires/[id]/questions/[qid]/scf-mapping/[proposalID]/approve/route.ts",
  "questionnaires/[id]/questions/[qid]/scf-mapping/[proposalID]/reject/route.ts",
  "questionnaires/[id]/questions/[qid]/scf-mapping/ai-suggest/route.ts",
  "questionnaires/[id]/route.ts",
  "questionnaires/[id]/suggestions/route.ts",
  "requirements/[id]/coverage/route.ts",
  "risks/[id]/action-plans/route.ts",
  "risks/[id]/audit-log/route.ts",
  "risks/[id]/children/[childId]/route.ts",
  "risks/[id]/children/route.ts",
  "risks/[id]/parents/route.ts",
  "risks/[id]/route.ts",
  "risks/categories/[ref]/[action]/route.ts",
  "risks/categories/[ref]/route.ts",
  "saved-views/[id]/route.ts",
  "tenants/[id]/route.ts",
  "themes/[ref]/route.ts",
  "vendors/[id]/reviews/route.ts",
  "vendors/[id]/route.ts",
]);

function dynamicRoutes(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      out.push(...dynamicRoutes(p));
    } else if (name === "route.ts") {
      const rel = relative(API_DIR, p);
      if (rel.includes("[")) out.push(rel);
    }
  }
  return out;
}

describe("BFF path-param UUID guard (#2312)", () => {
  const routes = dynamicRoutes(API_DIR);
  const guarded = new Set(
    routes.filter((r) =>
      readFileSync(join(API_DIR, r), "utf8").includes("rejectNonUUID("),
    ),
  );

  test("finds the dynamic BFF routes", () => {
    expect(routes.length).toBeGreaterThan(0);
  });

  test("every dynamic BFF route is guarded or listed", () => {
    const unguarded = routes.filter(
      (r) => !guarded.has(r) && !KNOWN_UNGUARDED.has(r),
    );
    expect(unguarded, "not guarded: call rejectNonUUID").toEqual([]);
  });

  test("the known-unguarded list has no stale entries", () => {
    const present = new Set(routes);
    const stale = [...KNOWN_UNGUARDED].filter(
      (r) => guarded.has(r) || !present.has(r),
    );
    expect(stale, "stale entry: remove it from KNOWN_UNGUARDED").toEqual([]);
  });
});
