# Issue #2502 — MCP create_risk: scores through the REST rule

Found while building #2490 (#2493): the MCP `create_risk` applier inserted
with raw SQL and skipped `ValidateInherentScore` and `ValidateScoreNumbers`.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## D1 — One rule: `risk.PrepareCreateScores`

`Store.Create` held the create-time score rule inline: methodology schema,
FAIR normalisation (with an empty FAIR residual taking the inherent), then
the #2490 numeric check. That block is now `risk.PrepareCreateScores`, and
both `Store.Create` and the MCP applier call it. The applier does not route
through `Store.Create` itself: the store owns its transaction and the
applier runs inside the proposal's confirm transaction (the reason the
applier's header gives for hand-written SQL). Calling the shared validator
before the insert is the option the ticket allows.

## D2 — Refuse when the proposal is filed, and again on confirm

`writeproposals.Store.Create` runs `validateToolInput` before its
transaction, so the MCP tool call itself gets the refusal rather than the
person confirming it later. The applier checks again on confirm, which
covers a proposal filed before this change. A tool_input that does not
decode is left to the applier, as before.

Statuses mirror `POST /v1/risks`: `ErrScoreNotNumeric` is 422,
`ErrInherentScoreInvalid` and `ErrInvalidMethodology` are 400. Both the
file and confirm surfaces return `{"error", "code": "score_validation_failed"}`;
the error text is the same string REST returns (pinned by
`TestHTTP_CreateRisk_ScoreRuleMatchesREST` and
`TestCreateRiskScores_SameRuleAsREST`).

## D3 — A refused field names its JSON type, not its value

`checkScoreFields` echoed the raw value (`got "20"`), so a huge string came
back whole. It now names the type (`got a string`, `an object`, ...). This
changes REST's message too; there is one message, not two.

## D4 — An omitted inherent_score still files an unscored risk

REST requires `inherent_score`; `create_risk` has always made it optional
and stored `{}`. Requiring it would change the tool's published input
schema and every caller that proposes a risk it cannot yet score. That is
a product call outside this ticket, so it is kept and named as the one
difference in `createRiskScores`' doc comment. The methodology is still
checked when the score is omitted, where before an unknown methodology
reached the enum cast in the INSERT.

## D5 — FAIR now stored as REST stores it

Before, an MCP FAIR risk kept its raw score (legacy `lef`/`lm` aliases
included) with residual `{}`. It now gets REST's canonical shape and the
residual takes the inherent. FAIR validation itself is unchanged
(`FairScoreFromJSON`; the numeric check still skips Monetary
methodologies).

## D6 — Audit of the other MCP write appliers

The MCP server's only platform writes are filing and confirming proposals
(`internal/mcp/tools/writes.go`), so the appliers registered in
`RegisterDefaultAppliers` are the whole MCP write surface.

| Tool                    | Applier                        | Write path                                                                                                                                                                          | Bypasses REST validation? |
| ----------------------- | ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| `create_risk`           | `ApplyCreateRisk`              | Hand-written INSERT                                                                                                                                                                 | Yes: fixed here           |
| `update_control_state`  | `NewUpdateControlStateApplier` | `ingest.Service.Process` (#1751), the stage REST and gRPC push use                                                                                                                  | No                        |
| `push_evidence`         | `NewPushEvidenceApplier`       | `ingest.Service.Process`                                                                                                                                                            | No                        |
| `update_risk_treatment` | `ApplyUpdateRiskTreatment`     | `risk.ChangeTreatmentTx` / `ProposeAcceptanceTx` / `ApproveAcceptanceTx`, the transition functions the REST routes use; `SetTreatmentOwnerTx`, through `applyRecordFields` as PATCH | No; writes no score       |
| `link_child_risks`      | `ApplyLinkChildRisks`          | `risk.LinkChildrenTx`, as `POST /v1/risks/{id}/children`                                                                                                                            | No                        |
| `unlink_child_risk`     | `ApplyUnlinkChildRisk`         | `risk.UnlinkChildTx`, as `DELETE /v1/risks/{id}/children/{child_id}`                                                                                                                | No                        |

There is no MCP write tool for controls, policies, vendors, action plans
or exceptions, and no MCP risk update that touches a score, so nothing
else needed fixing or filing.
