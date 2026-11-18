// #2143 — in-app module help, read at build time.
//
// `web/help/modules/<id>.md` is the single source for a module's help:
// the "How this works" panel (`components/help/help-panel.tsx`) renders
// it here, and the docs site publishes the same file as a module page
// (`docs-site/hooks/help_pages.py`). Each file opens with front matter:
//
//   ---
//   id: controls                  # equals the file name
//   title: Controls
//   routes: [/controls]           # the pages this help covers
//   terms: [anchor, crosswalk]    # ids from web/help/terms.json
//   diagrams:                     # optional; files in public/help/diagrams/
//     - src: control-states.svg
//       alt: What the diagram shows, for a screen reader
//   ---
//
// The front matter is a deliberately small YAML subset (scalars, flow
// lists, which Prettier may wrap onto the next line, and a block list of
// scalar maps for `diagrams`), parsed in `module-parse.ts`
// without a YAML dependency. The docs hook reads the same subset with
// PyYAML. `modules.test.ts` fails the build on a malformed file, an
// unknown term, a route claimed twice, or a file not registered below.
//
// A lifecycle diagram is not listed under `diagrams`: it is a ```mermaid
// block in the body (#2148, `lifecycle-diagram.ts`), pre-rendered to a
// light and a dark SVG, checked against the backend's exported lifecycle
// by `diagrams.test.ts`, and lifted out of the body into `lifecycles`.
//
// The `.md` imports resolve to the file's text: Turbopack's `raw` rule in
// `next.config.ts`, and a matching plugin in `vitest.config.ts`.

import actionPlansSource from "@/help/modules/action-plans.md";
import activitySource from "@/help/modules/activity.md";
import adminSource from "@/help/modules/admin.md";
import adminApiKeysSource from "@/help/modules/admin-api-keys.md";
import adminDemoSource from "@/help/modules/admin-demo.md";
import adminFeaturesSource from "@/help/modules/admin-features.md";
import adminSsoSource from "@/help/modules/admin-sso.md";
import adminSuperAdminsSource from "@/help/modules/admin-super-admins.md";
import adminTenantsSource from "@/help/modules/admin-tenants.md";
import adminUsersSource from "@/help/modules/admin-users.md";
import approvalsSource from "@/help/modules/approvals.md";
import auditsSource from "@/help/modules/audits.md";
import boardPacksSource from "@/help/modules/board-packs.md";
import calendarSource from "@/help/modules/calendar.md";
import controlsSource from "@/help/modules/controls.md";
import controlsChecklistSource from "@/help/modules/controls-checklist.md";
import csfSource from "@/help/modules/csf.md";
import dashboardSource from "@/help/modules/dashboard.md";
import evidenceSource from "@/help/modules/evidence.md";
import exceptionsSource from "@/help/modules/exceptions.md";
import frameworkScopesSource from "@/help/modules/framework-scopes.md";
import metricsSource from "@/help/modules/metrics.md";
import personnelSecuritySource from "@/help/modules/personnel-security.md";
import policiesSource from "@/help/modules/policies.md";
import questionnairesSource from "@/help/modules/questionnaires.md";
import risksSource from "@/help/modules/risks.md";
import risksHierarchySource from "@/help/modules/risks-hierarchy.md";
import scfCatalogSource from "@/help/modules/scf-catalog.md";
import settingsSource from "@/help/modules/settings.md";
import vendorClaimsSource from "@/help/modules/vendor-claims.md";
import vendorsSource from "@/help/modules/vendors.md";
import {
  helpModuleForPath as lookupHelpModule,
  parseHelpModule,
  type HelpModule,
} from "./module-parse";

export {
  parseHelpModule,
  type HelpDiagram,
  type HelpLifecycle,
  type HelpModule,
} from "./module-parse";

/** Every module help file, by file name. A new file is added here.
 *  A new module also gets a first-visit hint (#2145), so add its id to
 *  `userprefs.HintModules` (internal/auth/userprefs/hints.go) and widen the
 *  `user_onboarding_hint_dismissals` module CHECK in a new migration;
 *  until then the hint shows but its dismissal is refused with a 400. */
export const HELP_SOURCES: Readonly<Record<string, string>> = {
  "controls.md": controlsSource,
  // #2150: Controls.
  "evidence.md": evidenceSource,
  "exceptions.md": exceptionsSource,
  "controls-checklist.md": controlsChecklistSource,
  // #2151: Risks.
  "risks.md": risksSource,
  "risks-hierarchy.md": risksHierarchySource,
  "action-plans.md": actionPlansSource,
  // #2149: Program.
  "dashboard.md": dashboardSource,
  "approvals.md": approvalsSource,
  "calendar.md": calendarSource,
  "metrics.md": metricsSource,
  "activity.md": activitySource,
  // #2155: Settings & admin.
  "settings.md": settingsSource,
  "admin.md": adminSource,
  "admin-users.md": adminUsersSource,
  "admin-api-keys.md": adminApiKeysSource,
  "admin-sso.md": adminSsoSource,
  "admin-features.md": adminFeaturesSource,
  "admin-tenants.md": adminTenantsSource,
  "admin-super-admins.md": adminSuperAdminsSource,
  "admin-demo.md": adminDemoSource,
  // #2152: Audit.
  "audits.md": auditsSource,
  "framework-scopes.md": frameworkScopesSource,
  "board-packs.md": boardPacksSource,
  "csf.md": csfSource,
  "vendor-claims.md": vendorClaimsSource,
  // #2154: Library.
  "policies.md": policiesSource,
  "vendors.md": vendorsSource,
  "personnel-security.md": personnelSecuritySource,
  "questionnaires.md": questionnairesSource,
  "scf-catalog.md": scfCatalogSource,
};

export const HELP_DOCS_BASE_URL =
  "https://mgoodric.github.io/security-atlas/help/" as const;

/** The module's page on the published docs site. Absolute, because the
 *  shared Markdown renderer drops relative links and the web app serves no
 *  `/docs` path. */
export function helpDocsHref(id: string): string {
  return `${HELP_DOCS_BASE_URL}${id}/`;
}

/** The URL the app serves a diagram file from (`web/public`). */
export function helpDiagramSrc(file: string): string {
  return `/help/diagrams/${file}`;
}

export const HELP_MODULES: readonly HelpModule[] =
  Object.values(HELP_SOURCES).map(parseHelpModule);

/**
 * The help module for the page at `pathname`: the module with the longest
 * route that is the page itself or a parent of it, so `/controls` covers
 * `/controls/{id}` until a module claims the deeper route. Undefined when
 * no module covers the page, and the page shows no help button.
 */
export function helpModuleForPath(
  pathname: string,
  modules: readonly HelpModule[] = HELP_MODULES,
): HelpModule | undefined {
  return lookupHelpModule(pathname, modules);
}
