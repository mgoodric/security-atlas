- **fix(admin-demo):** slice 322 — `/admin/demo` "Reseed demo dataset"
  button now surfaces a visible DOM change within 1s of click.
  Maintainer reported "clicked the load demo data button, but nothing
  seemed to happen" on atlas-edge (`6b3c9d6f`). Root-cause diagnosis is
  hybrid candidate D + E (`docs/audit-log/322-admin-demo-button-no-feedback-decisions.md`):
  post-action `<Alert>` rendered below the fold with no aria-live and
  no scroll-into-view, and the button did not change state between
  click and dialog mount, so users perceived a silent click. Fix in
  `web/app/admin/demo/demo-controls.tsx`: brief "Opening confirmation…"
  button label after click (80ms), `aria-live="polite"` on every
  dynamic `<Alert>`, `scrollIntoView({ behavior: "smooth", block:
"center" })` on success/error Alert mount, and dev-mode
  `console.warn` for non-200 BFF responses. New e2e assertion in
  `web/e2e/admin-demo.spec.ts` covers the click-feedback contract
  (visible DOM change within 1s — guards the CLASS of bug, not just
  the instance) plus two assertions verifying `aria-live="polite"` on
  the post-action Alerts. Backend handler unchanged; env-var gate
  (`ATLAS_ENABLE_DEMO_SEED == "true"`) and slice 278 OPA admin-gate
  preserved verbatim (P0-322-1/2/5 honored). No new dependencies
  (P0-322-3); no workaround that hides config issues (P0-322-4).
