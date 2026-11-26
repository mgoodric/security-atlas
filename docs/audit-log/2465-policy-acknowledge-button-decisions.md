# #2465 — Acknowledge button and pending acknowledgments in the web app

detection_tier_actual: none
detection_tier_target: none

## D1 — the authz decision the ticket asked for was already made by #2524

The ticket opens with "Decide first" on who may acknowledge. #2524 (PR #2580)
landed that decision before this work started, in the shape the ticket
proposed: any signed-in member with a live role may `POST
/v1/policies/{uuid}/acknowledge` (a narrow path rule in `defaults.rego`), the
handler refuses a caller whose role is not in the policy's audience (403), and
the ack is always recorded for the caller. Its Go role matrix
(`internal/api/policyacks/role_matrix_2524_integration_test.go`) is the per-role
test. This change adds no authz and no backend; it builds the web half on that
contract.

## D2 — the page shows the card only for policies on the caller's pending list

The web app does not decide who may acknowledge. It reads `GET
/v1/me/acknowledgments`, which the platform already scopes to the caller, and
shows the card when this policy is on it. A policy outside the caller's
audience, or one they have acknowledged for this version within the year, is
not on the list, so the page offers nothing. The page cannot tell those two
apart from the pending list alone, so after a reload an acknowledged policy
shows no card rather than an "acknowledged" line; the line shows only right
after the click, from the acknowledge response. Showing it persistently would
need a per-policy "my latest ack" read the API does not have.

## D3 — one click, no inline confirmation

The detail page confirms submit and approve inline. Acknowledging records only
the caller's own attestation, the card states what the click attests ("confirms
you have read version X"), and a repeat is deduplicated by the platform, so a
second confirmation step adds friction without protecting anything.
`ConfirmInline` is also styled as destructive, which this is not.

## D4 — the "my acknowledgments" indicator lives on the policy list

A line above the policy table names each policy waiting for the caller and
links to it. It renders nothing when the list is empty or cannot be read, so
the list itself never depends on it.
