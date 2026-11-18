# #2384: API key labels (decisions log)

**Ticket:** [#2384](https://github.com/mgoodric/security-atlas/issues/2384), the follow-up to #2085.
**PR:** #2671

detection_tier_actual: manual_review
detection_tier_target: unit

Review of the first head found three gaps, and all three are now fixed. Bidi and zero-width labels were accepted, and so were quote characters. The audit rows did not carry the label. Each was a validation or payload branch that a unit test should have caught before review.

## Decisions

### D1: The admin API keys page keeps its "Last 4" column

The acceptance criterion says "No response or page shows a token, prefix or last4 for the key". This covers how a key appears in place of a person: `<Person>`, `resolvePerson`, the credential picker and `GET /v1/people`. None of these takes last4 as an input, and `apiKeyText` is built from the label and the owner's name only.

The key-management page at `/admin/api-keys` is exempt. An admin uses last4 there to tell which bearer a connector holds before rotating or revoking it. Matt confirmed this exemption during review.

**Confidence:** high (confirmed).

### D2: The label is display text, bounded and plain

- **Storage.** `api_keys.label` is nullable. The CHECK is 1 to 100 characters, so "no label" is always `NULL` and never `''`. The handler trims the label, and an empty result means no label.
- **Characters refused.** The handler refuses control characters (`unicode.IsControl`) and format characters (`unicode.Cf`), which include U+202E, U+200B and U+2066–2069. Bidi overrides and zero-width characters would make a label render as different text.
- **Quotes refused.** The handler also refuses `'`. A key reads "API key '<label>' (owner: <user>)". With a quote in the label, the label could close its own quotes and add a fake "(owner: …)".
- **Visibility.** Every signed-in member can read the label, because `GET /v1/people` is open to every role. The issue form says so under the field.

**Confidence:** medium. The 100-character limit is a judgement call, and it can be widened by changing the CHECK.

### D3: A rotate successor inherits the label

A rotation replaces the bearer but not the job the key does. If the label were dropped, the key would read as a different, unnamed actor from that point on. Both the DB store (`apikeystore.Rotate`) and the in-memory credstore/persister path carry the label forward.

**Confidence:** high.

### D4: Audit rows name the key

`keyAudit` gains `label` (`omitempty`). This means the `api_key_issue`, `api_key_rotate` and `api_key_revoke` rows name the key as well as giving its last4. An unlabelled key's row keeps its existing shape.

**Confidence:** high.

### D5: The server-side `people.Directory.Name` is unchanged

Server-side names, such as exports and #2286 responses, still read "API key …<last four of the key id>". The ticket scopes the label to `resolvePerson`. Moving the server-side name onto the label is left for a follow-up if one is wanted.

**Confidence:** medium.
