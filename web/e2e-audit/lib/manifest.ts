// Slice 178 — manifest loader + validator (AC-10, AC-10a, P0-178-11).
//
// Two roles:
//
//   1. At test time, `loadManifest()` parses + validates the
//      `mockup-spec.json` data file and returns the typed entries.
//
//   2. At CI time, `validateManifest()` performs the schema check + the
//      mockup-file-existence check; the `Frontend · UI honesty
//      (advisory)` job runs it before invoking Playwright so a broken
//      manifest fails fast.
//
// The validator is a small handwritten check rather than a runtime AJV
// dependency — the manifest's shape is small enough (~10 fields per
// entry) that adding `ajv` to the dev tree is more cost than the
// duplication. The shipped JSON Schema (`mockup-spec.schema.json`) is
// the canonical declarative version for editor / IDE assist and stays
// in sync with this checker by structure.

import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import type { ManifestEntry } from "./mockup-diff";

const REPO_ROOT_FROM_WEB = resolve(__dirname, "..", "..", "..");

export function manifestPath(): string {
  return resolve(__dirname, "..", "mockup-spec.json");
}

export function mockupsDir(): string {
  // Mockups archived out of the active tree by slice 437; `web/` is the
  // canonical frontend and per-page mockup-vs-`web/` divergence is no
  // longer fileable drift. The harness still resolves the (now archived)
  // source-of-truth files for its SHIP-GAP / MOCKUP-STALE heuristics.
  return resolve(REPO_ROOT_FROM_WEB, "Plans", "_archive", "mockups");
}

export type ManifestValidationError = {
  index: number;
  route: string | null;
  message: string;
};

export function validateManifest(raw: unknown): {
  ok: boolean;
  entries: ManifestEntry[];
  errors: ManifestValidationError[];
} {
  const errors: ManifestValidationError[] = [];
  const entries: ManifestEntry[] = [];

  if (!Array.isArray(raw)) {
    return {
      ok: false,
      entries: [],
      errors: [
        { index: -1, route: null, message: "manifest root must be an array" },
      ],
    };
  }

  for (let i = 0; i < raw.length; i++) {
    const obj = raw[i];
    const where = (msg: string) => ({
      index: i,
      route: typeof obj?.route === "string" ? obj.route : null,
      message: msg,
    });
    if (!obj || typeof obj !== "object") {
      errors.push(where("entry must be an object"));
      continue;
    }
    if (typeof obj.route !== "string" || !obj.route.startsWith("/")) {
      errors.push(where(`route must be a string starting with /`));
    }
    if (obj.mockupPath !== null && typeof obj.mockupPath !== "string") {
      errors.push(where("mockupPath must be a string or null"));
    } else if (typeof obj.mockupPath === "string") {
      if (!/^[a-z0-9][a-z0-9./_-]*\.html$/.test(obj.mockupPath)) {
        errors.push(
          where(
            `mockupPath ${JSON.stringify(
              obj.mockupPath,
            )} must match ^[a-z0-9][a-z0-9./_-]*\\.html$`,
          ),
        );
      }
      // P0-178-11 — the file must exist on disk.
      const filePath = resolve(mockupsDir(), obj.mockupPath);
      if (!existsSync(filePath)) {
        errors.push(
          where(
            `mockupPath ${JSON.stringify(
              obj.mockupPath,
            )} does not exist at ${filePath} (P0-178-11)`,
          ),
        );
      }
    }
    if (!Array.isArray(obj.expectedTestIds)) {
      errors.push(where("expectedTestIds must be an array of strings"));
    } else if (
      !obj.expectedTestIds.every(
        (t: unknown) => typeof t === "string" && t.length > 0,
      )
    ) {
      errors.push(where("expectedTestIds must contain only non-empty strings"));
    } else if (
      new Set(obj.expectedTestIds).size !== obj.expectedTestIds.length
    ) {
      errors.push(where("expectedTestIds must be unique"));
    }
    if (!Array.isArray(obj.allowedExtraTestIds)) {
      errors.push(where("allowedExtraTestIds must be an array of strings"));
    } else if (
      !obj.allowedExtraTestIds.every(
        (t: unknown) => typeof t === "string" && t.length > 0,
      )
    ) {
      errors.push(
        where("allowedExtraTestIds must contain only non-empty strings"),
      );
    } else if (
      new Set(obj.allowedExtraTestIds).size !== obj.allowedExtraTestIds.length
    ) {
      errors.push(where("allowedExtraTestIds must be unique"));
    }
    if (obj.staleMockupTestIds !== undefined) {
      if (!Array.isArray(obj.staleMockupTestIds)) {
        errors.push(where("staleMockupTestIds must be an array if present"));
      }
    }
    if (obj.notes !== undefined && typeof obj.notes !== "string") {
      errors.push(where("notes must be a string if present"));
    }
    if (errors.filter((e) => e.index === i).length === 0) {
      entries.push({
        route: obj.route as string,
        mockupPath: obj.mockupPath as string | null,
        expectedTestIds: obj.expectedTestIds as string[],
        allowedExtraTestIds: obj.allowedExtraTestIds as string[],
        staleMockupTestIds: obj.staleMockupTestIds as string[] | undefined,
        notes: obj.notes as string | undefined,
      });
    }
  }

  // Route uniqueness across entries.
  const seen = new Set<string>();
  for (let i = 0; i < entries.length; i++) {
    const r = entries[i].route;
    if (seen.has(r)) {
      errors.push({
        index: i,
        route: r,
        message: `duplicate route ${r}`,
      });
    }
    seen.add(r);
  }

  return { ok: errors.length === 0, entries, errors };
}

/**
 * Load + validate the manifest. Throws on failure with a multi-line
 * message that names the offending entry — readable in CI logs.
 */
export function loadManifest(): ManifestEntry[] {
  const path = manifestPath();
  const text = readFileSync(path, "utf-8");
  const raw = JSON.parse(text);
  const result = validateManifest(raw);
  if (!result.ok) {
    const lines = [
      `manifest at ${path} failed validation:`,
      ...result.errors.map(
        (e) =>
          `  - [${e.index}] ${e.route ? `\`${e.route}\` ` : ""}${e.message}`,
      ),
    ];
    throw new Error(lines.join("\n"));
  }
  return result.entries;
}
