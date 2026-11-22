# Jira REST fixtures

`search_issues.json` is a `GET /rest/api/3/search` response built from
Jira Cloud's REST v3 OpenAPI description
(`https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json`,
read 2026-11-21): the `SearchResults` envelope, the user's `accountId` and
`displayName`, the `priority` object (`name`, `id`, `iconUrl`), and the
`created` timestamp format `2022-10-06T07:32:47.000+0000`. Issue `fields`
is an untyped map in the spec. `labels` (an array of strings),
`resolutiondate` (same format as `created`) and a select-list custom
field's option object (`{self, value, id}`) are Jira's documented system
and custom field shapes, but the spec has no example of them; they were not
checked against a live site.

The issues cover: a resolved training ticket with a non-UTC offset, an
open vulnerability ticket with a custom severity option, a resolved ticket
with an unrecognised priority name and a null custom field, and a ticket
with an unparseable `created` and a plain-string custom field.
