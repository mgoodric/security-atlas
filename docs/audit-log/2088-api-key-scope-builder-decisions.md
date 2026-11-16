# #2088 — API-key scope builder over pickers, with a raw JSON fallback

The issue form on `/admin/api-keys` asked for the scope predicate as typed
JSON and the allowed kinds as a comma-separated list. It now builds both
with pickers, shows the predicate read-only, and keeps raw JSON as a
fallback. The build-time calls:

detection_tier_actual: manual_review
detection_tier_target: integration

## D1 — Narrowed: no control or framework pickers

The ticket named controls and frameworks "as the scope predicate allows".
The predicate allows neither. A key's push scope is enforced in two places
(`internal/evidence/ingest/ingest.go`): `allowed_kinds`, and
`scope_predicate`, which is matched against the record's scope
dimensions. Nothing on the push path refers to a control or a framework.
A control picker would have shown an admin a key as scoped to a control
when nothing enforced it, which misstates a security boundary.

Agreed with the dispatcher (relaying for Matt): the builder offers
evidence kinds and scope dimensions only. The first acceptance check
becomes "two evidence kinds and one scope dimension". Control and
framework scoping need backend enforcement first.

## D2 — Found: the stored predicate does not match the evaluator

`scope_predicate` is stored as JSONB (default `'{}'`), but ingest
evaluates it as a `key=value[,key=value]` string. In a scratch test (since
deleted), `scopeSatisfiesPredicate(nil, "{}")` returned false, and so did
any JSON object. So a persisted key with a non-empty predicate,
including the default this form has always sent, may reject every push.
That's a backend fix, filed as #2435, and outside this ticket. The
detection tier above records it: a manual read caught what an integration
test that pushes with a UI-issued key should have caught.

## D3 — The predicate uses the `internal/scope` grammar

The builder reuses `ScopePicker` (#2103), so the predicate is in the
grammar `scope.Evaluate` reads (`eq`, `in`, `and`). Keys, exceptions and
framework scopes then share one predicate language, and the picker's
"can't show this" reasons come with it. #2435 was asked to evaluate the
credential predicate with `scope.Evaluate` rather than define a second
schema. The dispatcher agreed.

## D4 — Everything is sent as `{}`

The picker's Everything is `{"op":"true"}`. The form sends `{}` instead,
the column default and the value it always sent. #2435 defines `{}` as
unrestricted. Both spellings already load as Everything in the picker.
The picker's read-only view still prints `{"op":"true"}` for Everything.
That's the same meaning, so the shared picker is left alone.

The raw JSON toggle is disabled while "Choose by dimension" has no value
picked. Opening that unfinished choice as JSON would show Everything and
silently widen the key.

## D5 — Raw JSON stays open on a predicate the builder can't show

Switching back from raw JSON parses the text. Invalid JSON, or a valid
predicate the builder can't show (any-of, negation, a custom dimension), is
explained under the editor, and the editor stays open. The key can
still be issued with it as written. The framework-scope wizard instead
switches back and shows the predicate as "Advanced-only". Here, staying
in the editor keeps the text editable and puts the explanation next to it.
The parse reuses the wizard's `parseAdvanced` and the picker's
`fromPredicate`, with no new rules.

## D6 — Evidence kinds come from the schema registry

The kinds picker lists the tenant's registered kinds (`GET /v1/schemas`,
through the new `/api/evidence-kinds` BFF route, which passes back only
kind, semver and owner). Ingest does not require a kind to be registered,
so a kind can also be added by name. None picked still means any kind, and
the picker says so.

## D7 — The first e2e check issues a real key

The stored-scope check issues through the real platform and reads the
key back from the list, then revokes it. Only the pick lists are mocked,
so the kind and dimension values stay fixed whatever the seed holds. The
raw-JSON check mocks the issue call: it checks what is sent, not what is
stored.
