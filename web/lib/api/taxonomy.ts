// #1818 — browser-side client for the Taxonomy settings page: the
// organisation's risk categories and themes, through the BFF.
//
// Every write goes to the platform, which is the gate: a caller without
// risk-taxonomy rights (admin or grc_engineer) gets a 403, and deleting a
// category or theme still in use is a 409 with the counts. The helpers at
// the bottom only decide what the page offers; they never replace either
// refusal.

import { APIError } from "./base";
import type { RiskCategory } from "./risks";

export type { RiskCategory } from "./risks";

// One entry in GET /v1/themes: a built-in (`default`) or one of the
// organisation's own (`tenant`), with its usage.
export type TaxonomyTheme = {
  name: string;
  description: string;
  source: "default" | "tenant";
  risk_count: number;
  rule_count: number;
};

async function call<T>(
  url: string,
  init?: { method?: string; body?: unknown },
): Promise<T> {
  const res = await fetch(url, {
    method: init?.method ?? "GET",
    headers:
      init?.body === undefined
        ? undefined
        : { "Content-Type": "application/json" },
    body: init?.body === undefined ? undefined : JSON.stringify(init.body),
    cache: "no-store",
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

const categoryPath = (ref: string) =>
  `/api/risks/categories/${encodeURIComponent(ref)}`;
const themePath = (ref: string) => `/api/themes/${encodeURIComponent(ref)}`;

/** All the organisation's categories, archived ones included. */
export async function listTaxonomyCategories(): Promise<RiskCategory[]> {
  const body = await call<{ categories?: RiskCategory[] }>(
    "/api/risks/categories?include_archived=true",
  );
  return body.categories ?? [];
}

export function createCategory(input: {
  slug: string;
  name: string;
  description?: string;
}): Promise<unknown> {
  return call("/api/risks/categories", { method: "POST", body: input });
}

export function renameCategory(slug: string, name: string): Promise<unknown> {
  return call(categoryPath(slug), { method: "PATCH", body: { name } });
}

export function transitionCategory(
  slug: string,
  action: "archive" | "unarchive" | "set-default",
): Promise<unknown> {
  return call(`${categoryPath(slug)}/${action}`, { method: "POST" });
}

export function deleteCategory(slug: string): Promise<unknown> {
  return call(categoryPath(slug), { method: "DELETE" });
}

export async function listTaxonomyThemes(): Promise<TaxonomyTheme[]> {
  const body = await call<{ themes?: TaxonomyTheme[] }>("/api/themes");
  return body.themes ?? [];
}

export function createTheme(input: {
  name: string;
  description?: string;
}): Promise<unknown> {
  return call("/api/themes", { method: "POST", body: input });
}

export function renameTheme(name: string, next: string): Promise<unknown> {
  return call(themePath(name), { method: "PATCH", body: { name: next } });
}

export function deleteTheme(name: string): Promise<unknown> {
  return call(themePath(name), { method: "DELETE" });
}

// ----- what the page offers -----

/** The roles the platform lets manage the taxonomy (requireTaxonomyAdmin). */
const TAXONOMY_ROLES = ["admin", "grc_engineer"];

/**
 * canManageTaxonomy mirrors the platform's risk-taxonomy guard, so the
 * page shows edit controls only to a caller whose writes would succeed.
 * Anything unknown is view-only.
 */
export function canManageTaxonomy(
  me: { is_admin?: boolean; roles?: readonly string[] } | undefined,
): boolean {
  if (!me) return false;
  if (me.is_admin === true) return true;
  return (me.roles ?? []).some((r) => TAXONOMY_ROLES.includes(r));
}

/** usageLabel names a category's or theme's usage, e.g. "3 risks, 1 rule". */
export function usageLabel(u: { risk_count: number; rule_count: number }) {
  const risks = `${u.risk_count} ${u.risk_count === 1 ? "risk" : "risks"}`;
  const rules = `${u.rule_count} ${u.rule_count === 1 ? "rule" : "rules"}`;
  return `${risks}, ${rules}`;
}

/**
 * deleteBlockedReason says why delete is unavailable, or null when it is
 * allowed: the platform refuses deleting anything a risk or aggregation
 * rule uses, and the default category can't be deleted either.
 */
export function deleteBlockedReason(u: {
  risk_count: number;
  rule_count: number;
  is_default?: boolean;
}): string | null {
  if (u.risk_count > 0 || u.rule_count > 0) {
    return `In use by ${usageLabel(u)}`;
  }
  if (u.is_default) return "The default category can't be deleted";
  return null;
}
