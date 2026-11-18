# openapi-types

Writes `web/lib/api/generated/openapi.d.ts` from `docs/openapi.yaml` (#2394).
From `web/`: `npm run openapi:types` to write it, `npm run openapi:check` to
fail on a stale copy. Both need `npm ci --prefix tools/openapi-types` first.

This directory is its own npm package, outside the web workspace and the
root lockfile, because `openapi-typescript@7.13.0` declares a
`typescript@^5.x` peer while web is on TypeScript 6, and npm can neither
install that nor override a peer range. TypeScript 5 here is used only to
print the generated file. Web still compiles that file with TypeScript 6.

Fold it back into web's devDependencies, and delete this directory, once an
`openapi-typescript` release accepts TypeScript 6.
