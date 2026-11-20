# 2715 — tenant switch: guard every reset path; stop stale mutation writes — decisions log

- detection_tier_actual: manual_review
- detection_tier_target: unit

The mutation leak was found by reading code during the follow-up to the
#2712 review, not by a test. It could have been caught at unit tier:
`lib/tenant-cache.test.ts` now runs a mutation across a reset on the app's
real QueryClient.

## D1. "Reset the mutation cache too" does not work

The ticket proposed resetting the mutation cache if any `onSuccess` could
write after a switch. Some do: `app/(authed)/settings/_tabs/tenant.tsx`
writes `["risks", "review-cadence"]` and `["vendors",
"initial-review-window"]`, and `app/(authed)/catalog/audited-under` writes
its report key. None of these keys carries a tenant or an id.

A probe built a mutation, called `getMutationCache().clear()` and
`resetQueries()`, then resolved the mutation. The cache held the tenant-A
value afterwards. `Mutation.execute` in `@tanstack/query-core` 5.100.10
calls `this.options.onSuccess` whether or not the mutation is still in the
cache, so clearing it does nothing to the write.

## D2. Drop the stale onSuccess via the MutationCache, don't throw

`tenantGuardedMutationCache()` (`web/lib/tenant-cache.ts`) is the browser
QueryClient's MutationCache. Its `onMutate` stamps each mutation with the
current tenant epoch, and `forgetTenantCaches()` bumps that epoch. Its
`onSuccess` runs before the mutation's own `onSuccess`. For a stale stamp, it
replaces the options with `onSuccess: undefined` (public `setOptions`).

This relies on one detail of query-core 5.100.10: `execute` awaits
`config.onSuccess` and only then reads `this.options.onSuccess`. The
late-write test runs the real QueryClient through a `MutationObserver`, so it
fails if that ordering changes. Red-checked: with the epoch bump removed it
fails (`expected 'tenant A: 90 days' to be undefined`).

**Rejected: throwing `TenantSwitchedError` from `config.onSuccess`.** That
also skips the write, but it sends the mutation to `error`. No shared
mutation-error path exists to filter the error out: components render
`mut.error.message` themselves, in at least 15 files. Each one still mounted
would show a false "save failed".

**What the stale mutation does instead:** it ends in `success` with its
data, and its `onSettled` still runs, so UI resets (`setEvalProgress(null)`
and the like) and invalidations still happen. Invalidations refetch under the
new cookie, which is safe. The server write under tenant A did succeed. Only
the client-side cache write is dropped.

A mutation with no stamp counts as stale (fail closed). That only happens to
one restored from a persister, and this app has none.

## D3. Residual gap: callbacks passed to mutate()

Callbacks given as `mutate(vars, { onSuccess })` live on the
`MutationObserver`, which query-core keeps private. The guard cannot reach
them. Code that runs after `await mutateAsync()` is outside its reach for
the same reason. None writes to the query cache today.
`lib/tenant-switch-guard.test.ts` flags a `setQueryData` inside a
`mutate()`/`mutateAsync()` call's own arguments. It does not flag a write
made after an awaited `mutateAsync()`, because telling that apart from an
unrelated later write needs control-flow analysis. That gap is documented,
not guarded.

## D4. Guarding the switch paths: a source scan, not a lint rule

`lib/tenant-switch-guard.test.ts` follows `lib/no-origin-self-fetch.test.ts`
(vitest, comments stripped). It holds four rules:

- Only `lib/auth/switch-tenant.ts` calls the switch BFF route.
- Every `switchTenant()`/`onTenantSwitched()` call is matched by a
  `forgetTenantCaches()` call. The two listeners that need no reset of their
  own are exempt by name, each with its reason.
- The server writers of the `atlas_jwt` cookie are a known list, each naming
  the reset that covers it.
- The `mutate()`-callback write from D3 is flagged.

A custom ESLint rule (like `web/eslint-rules/no-dev-notes-in-ui.mjs`) was not
taken. ESLint rules see one file at a time, and rules 1 and 3 are facts about
the whole tree: only one file calls the route, and the set of cookie writers is
exactly this list. A vitest sentinel can state both, and it already runs in the
`Frontend · vitest` gate.
