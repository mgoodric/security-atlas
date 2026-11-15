# Taxonomy settings page — decisions log (#1818)

One page manages the organisation's risk categories and themes (spec
#1745). The category and theme write APIs already existed (#1810, #1811).
This change adds the page, the BFF routes for those writes, and usage
counts on the theme list, which the page needs to disable delete.

detection_tier_actual: none
detection_tier_target: none

## Decisions

**D1: The page is `/risks/taxonomy`, not `/settings` or `/admin`.**
A non-admin must be able to view it, which rules out `/admin` (its layout
shows non-admins a forbidden panel). `/settings` is user-facing only: slice 103's
P0-A1 keeps tenant-wide settings off it. The taxonomy belongs to the risk
register, so the page sits beside `/risks/hierarchy`, gets its own
`PAGE_TITLES` entry ("Risk taxonomy") and a sidebar entry in the Risks
group. `lib/nav.test.ts` requires every titled route to be in the sidebar.

**D2: `GET /v1/themes` carries `risk_count` and `rule_count`.**
The ticket asks for themes listed with usage, and for delete disabled
while a theme is in use. The categories list already returned usage
(#1810), but the theme list didn't. A new `ListVisibleThemesWithUsage`
query computes the same counts `ThemeUsage` uses for the delete refusal,
so what the list shows and what a delete refuses can't disagree. Both
fields are additive. No client reads the old shape strictly.

**D3: Edit controls show for admin or grc_engineer, read from `/v1/me`.**
`canManageTaxonomy` mirrors `hasTaxonomyAdmin`: `is_admin`, or a role of
`admin` or `grc_engineer`. Every other caller gets the lists with no add
form and no buttons. Hiding them is presentation only. The platform
refuses each write from those callers with a 403
(`category_admin_integration_test.go`, `theme_admin_integration_test.go`).

**D4: Delete stays visible but disabled, with the reason as text.**
A category or theme still in use shows "Can't delete: In use by N risks,
M rules" under a disabled Delete button, linked by `aria-describedby`.
A screen reader reads the reason, not only a hover tooltip. The default
category is blocked the same way, because the platform refuses deleting
it (`ErrCategoryIsDefault`). For the same reason, the default offers no
Archive and an archived category offers no Set default. Deleting confirms
inline with `ConfirmInline`.

**D5: The category transition BFF route allowlists its action.**
`/api/risks/categories/{ref}/{action}` forwards only `archive`,
`unarchive` and `set-default`; anything else is a 404 there. The BFF never
relays an arbitrary path onto `/v1/risks/categories`.
