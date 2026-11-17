# #2286 — overlay edge author names: decisions

- detection_tier_actual: none
- detection_tier_target: none

Builds on #1800 (overlay list, coverage wires, export) and #2085 (the
people resolver).

### D1 — resolve on the server, with one lookup

The web already has `<Person>` (#2085), which resolves an actor through
`/api/people` in the browser. It is not used for overlay authors:

- the ticket asks for the names in the export, which the browser never
  touches, so a server-side resolver is needed regardless;
- `<Person>` looks a key up by the raw reference, but a key credential id
  is `key_<uuid>` while `/v1/people` lists the bare uuid, and its key text
  is "API key (owner: …)", not the "API key …<last4>" the ticket asks for.

So the names are resolved once, on the server, and carried on the wire
(`created_by_name`, `retired_by_name`, `overlay.created_by_name`). The
server does not add a second user lookup: `people.Directory` is built from
the same `list` read that serves `GET /v1/people` (refactored out of the
handler), on the app pool under the caller's RLS tenant with an explicit
`tenant_id` predicate as well. Another tenant's user or key is never in
the directory and reads "Unknown".

### D2 — a session records the person, not the token

`actorFrom` recorded `cred.ID`. For a signed-in user that is `jwt:<jti>`,
one id per token, which names no one and cannot be resolved. A JWT caller
whose subject names a user (`credstore.CallerUserID`) is now recorded as
the bare `users(id)`, the shape #2479 records for `triggered_by`. Every
other caller keeps its credential id, so a key stays `key_<uuid>` even when
a user owns it (`credstore.PrincipalOf` would turn a user-owned key into
its owner and lose "API key"). Edges a session wrote before this change
keep their `jwt:` id and read "Unknown": there is no record mapping a token
id back to a user, and the update guard forbids rewriting `created_by`.

### D3 — formats

| Actor recorded                        | Name                       |
| ------------------------------------- | -------------------------- |
| a users(id) of this tenant (active)   | display name               |
| a users(id) of this tenant (disabled) | "Former user" (as #2085)   |
| `key_<uuid>` of this tenant           | "API key …" + last 4 of id |
| `system`                              | "System"                   |
| anything else                         | "Unknown"                  |

The last four are of the key's id, never its token: `api_keys.last4` is
the token's and is not read. Uuids must be canonical (`identity.UserID`,
#2478).

### D4 — export columns

`created_by` and `retired_by` become the names; `created_by_id` and
`retired_by_id`, placed beside them, keep the recorded ids. Every cell
already goes through `internal/export`'s formula neutralisation, so a name
starting with `=`, `+`, `-` or `@` is prefixed with `'` (tested).

### D5 — a failed name read

The list and export fail closed (500) when the people read fails. A
create, retire or import has already committed, so its response logs the
failure and names the authors "Unknown" instead of failing the request.
