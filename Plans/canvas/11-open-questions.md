**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 11. Open Questions Deferred

These are decisions the canvas does **not** resolve. Each is a real choice with tradeoffs that deserve a separate conversation.

1. **SCF licensing fine print.** The standard license is free, but distribution within an OSS product needs a careful read of redistribution terms. Treat as a legal review checkpoint before shipping a packaged catalog.
2. **OpenGRC pattern reuse.** OpenGRC's CC BY-NC-SA license blocks code reuse, but which *concepts* (data model patterns, framework seeders, UI affordances) are worth lifting? An explicit "borrow / leave" inventory is worth a half-day.
3. **License choice for security-atlas itself.** Apache 2.0 vs AGPL is the live debate. Apache supports broadest commercial embedding (we want this); AGPL prevents proprietary forks (some OSS GRC users prefer this). This is governance-shaping.
4. **Risk methodology default — confirm NIST 800-30 + 5x5 + dollar-banded over FAIR.** Practitioner research strongly supports the qualitative+banded default with FAIR for top-N risks. Lock in.
5. **Hosted offering or pure OSS?** A community OSS product can fund itself via a hosted SaaS by the project owners, an enterprise edition, or pure community. Each shapes governance and the Year-2-cliff value-prop.
6. **Audit firm partnerships.** Is there a "audited by security-atlas-fluent firms" registry? This is the auditor adoption flywheel — worth a real strategy.
7. **Privacy as a separate module or first-class?** GDPR/CCPA add data subjects, ROPAs, DPIAs — entities that don't map onto the security control model cleanly. Likely a sibling module sharing the platform spine.
8. **AI-assistance boundary.** What can LLMs do unsupervised? Practitioner research is unambiguous: nothing in audit-binding artifacts. Codify the explicit policy in the contributor docs before any AI feature lands.
9. **Schema-of-evidence governance.** As community connectors land, who owns canonical evidence schemas? An OpenTelemetry-semantic-conventions–style registry is probably the answer.
10. **Disclosure / breach-notification workflow scope.** HIPAA breach rule and GDPR Art. 33 are workflow-heavy. v1 punts; phase 3 lands them.
11. **CCM and FedRAMP elevation timing.** CCM can be opt-in import any time. FedRAMP needs RFC-0024 OSCAL conformance — could be a strong v1.5 differentiator if a user demands.
12. **Governance of the control catalog itself.** Who reviews community-contributed controls? "Verified" tier? Build this from the start to avoid a later quality crisis.
13. **Solo-operator vs multi-tenant tension.** The primary persona deploys single-tenant; OSS distribution wants multi-tenant. Postgres RLS handles the data model, but the UX for "I'm the only user" should not feel multi-tenant. Design for hidden multi-tenancy in single-user mode.
14. **The board narrative LLM boundary.** The auto-drafted narrative is the most valuable feature for solo operators *and* the highest-risk feature if it hallucinates. Spend time on the prompt engineering, the human-approval UX, and the audit trail of every generated word.
15. **CSA / Shared Assessments licensing posture.** Bundling CAIQ or SIG templates inside the OSS distribution requires commercial licenses we do not currently hold. v1 ships the *machinery* (ingest, AI-assist, export) and the user provides the file. v3 may revisit if customer demand justifies CSA membership. Document this clearly in the project README so contributors don't accidentally PR bundled templates.
16. **AI inference backend default.** Local Ollama is the v2 default (no data leaves deployment, fits the security-product-startup trust story). Cloud LLM (Anthropic / OpenAI / Bedrock) is opt-in per-tenant. Decide which 1–2 local models to ship-test against to set quality expectations.
17. **Schema-registry governance.** As community-contributed `evidence_kind` schemas land, who reviews them? "Verified" tier? Semver enforcement (additive minor versions, breaking-change deprecation windows)? This needs to be defined before community connectors and pushers proliferate and lock in inconsistent shapes.
18. **Push credential issuance UX.** Short-lived OIDC tokens from CI IdPs are the right default for service accounts, but the UX for issuing platform API keys (rotation, scoping, revocation, audit) needs design — this is the credential type users will reach for first, and getting the scoping wrong is the path to "the CI key can push anything for any tenant."
19. **FrameworkScope ownership.** Who owns the predicates? Engineering knows where systems live; the auditor approves what counts. The `approved_by` + `approval_evidence` fields exist, but the workflow (drafting → review → auditor approval → activation) needs UX design. Particularly load-bearing for PCI where scope reduction is the dominant lever.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 10. Roadmap](./10-roadmap.md) · [Sources →](./sources.md)
