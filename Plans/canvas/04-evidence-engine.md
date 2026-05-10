**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 4. Evidence Engine

## 4.1 Evidence SDK (defined first, before any connector)

The SDK contract is the architectural commitment. The ledger has exactly one canonical inbound API: `IngestEvidence(record) → EvidenceReceipt`. The SDK exposes that API through **two complementary profiles**, not a primary and a fallback:

| Profile | Direction | Who initiates | Use when |
|---|---|---|---|
| **Connector** (pull / subscribe) | Platform → Source | security-atlas reaches out and queries / subscribes | Source has a stable API and we have credentials to reach it |
| **Pusher** (push) | Source → Platform | Source initiates and pushes to security-atlas | Source is behind a firewall, ephemeral (CI), event-emitting (webhook), or owns its scheduling |

Many real connectors implement both. The GitHub connector pulls org/repo state on a schedule *and* receives push events from GitHub's webhook subscription — both flow into the same ledger via the same `IngestEvidence` call. CI/CD evidence (SAST, SCA, container scans, deploy events) is push-only by nature. Custom internal tools, aggregating middleware, telemetry-tap configurations (Vector / OTEL collectors), and air-gapped data diodes all become first-class evidence sources via push.

Connector profile methods (gRPC, language-agnostic, runs as a separate process):

| Method | Returns | Notes |
|--------|---------|-------|
| `Describe()` | `ConnectorManifest` | name, version, supported source types, required scopes, rate-limit hints, **profiles_supported** |
| `AuthMethods()` | `[AuthMethod]` | OIDC, API key, IAM role, OAuth flow, SCIM token |
| `HealthCheck(creds)` | `HealthResult` | Can we reach the source? |
| `ListEvidenceKinds()` | `[EvidenceKind]` | Each kind has a registered schema URI. |
| `Pull(kind, since, scope_filter)` | `Stream<EvidenceRecord>` | Snapshot/query mode. |
| `Subscribe(kind, scope_filter)` | `Stream<EvidenceRecord>` | Event-driven streams (when source supports). |
| `VerifyProvenance(record)` | `bool` | Cryptographic re-verification when applicable. |

Pusher profile surface (REST + gRPC + CLI + per-language SDKs):

| Endpoint / surface | Purpose |
|---|---|
| `POST /v1/evidence:push` | Single record or batch (≤100). Idempotency-key required. Schema-validated. |
| `Push(stream<EvidenceRecord>)` (gRPC) | High-throughput streaming push. |
| `security-atlas evidence push` CLI | Universal escape hatch — works from any shell, CI, cron. |
| Go / Python / TypeScript / Java SDKs | Embed in customer code. |

Push auth: short-lived OIDC tokens from CI IdPs (GitHub Actions, GitLab CI, AWS IRSA), platform-issued API keys, or mTLS — each scoped at issue time to (tenant × evidence_kind set × scope predicate × TTL). Idempotency keys, rate limits, schema-registry validation, and provenance metadata are all mandatory. Anonymous push, schemaless push, and scope-less push are explicitly rejected.

**The schema registry** is the contract enforcement point. Every `evidence_kind` has a stable identifier, a JSON Schema, an owner, default SCF anchor mappings, and semver. Tenants can register private kinds for custom internal tools without touching the global namespace — the OpenTelemetry-semantic-conventions analog.

> **Deep dive:** the full SDK contract — both profiles, push security threat model, middleware patterns (aggregating, telemetry-tap, air-gapped one-way bridge, cross-cluster federation), CLI and SDK details, and roadmap — is in [`EVIDENCE_SDK.md`](../EVIDENCE_SDK.md).

This contract was written before listing any connector deliberately, so AWS-shaped assumptions don't leak. The push profile was added because pull-only architectures structurally cannot ingest CI/CD evidence, behind-firewall sources, telemetry-tap deployments, or air-gapped systems — all of which a security-product startup encounters in the first year.

## 4.2 v1 connector roster

| Connector | Why v1 | Source pattern |
|-----------|--------|----------------|
| AWS | Universal cloud baseline | Event (CloudTrail → EventBridge) + Query (Steampipe-style) |
| GCP | Second cloud baseline | Event (Audit Logs → Pub/Sub) + Query |
| Azure | Third cloud baseline | Event (Activity Log → Event Hub) + Query |
| Kubernetes | Container reality | Event (audit log) + Query (kube-bench) |
| GitHub | Code provenance + access | Event (audit log API) + Query |
| GitLab | Same | Same |
| Okta | IdP | Event (System Log) + Query (SCIM) |
| Azure AD / Entra | IdP | Event + Query |
| Google Workspace | IdP + endpoint | Event (Reports API) + Query |
| Jamf / Intune | MDM | Query (mostly) |
| osquery / Fleet | Endpoint posture | Query (host-driven) |
| Jira / Linear | Ticket evidence | Query + webhook |
| Slack | Comms-record evidence | Event (audit log) for enterprise |
| 1Password / Bitwarden | Secrets posture | Query |
| Datadog / Grafana / PagerDuty | Ops/IR evidence | Query |
| HRIS (Rippling, BambooHR, Workday) | Personnel lifecycle | Query (SCIM where available) |
| CSV / S3 / SFTP / Manual upload | Universal escape hatch | Query (cron + file watcher) |

Roughly 17 connectors covers ~80% of mid-market evidence demand. Community can extend to Vanta's 300+ over time.

## 4.3 Evidence ingestion vs control evaluation (separated stages)

```
[ Source system ]
       │
       ▼   (event or pull)
[ Connector ]
       │ raw record
       ▼
[ Ingestion stage ]  --- canonicalize, redact, hash, scope-tag, store in ledger
       │
       ▼   (immutable evidence ledger, append-only)
[ Evaluation stage ]  --- read-only consumer; runs queries/policies against records
       │
       ▼
[ Control state ]  --- pass/fail/inconclusive per (control × scope × time)
```

This separation means:
- Evaluation logic can be replayed against historical evidence at will (point-in-time audit replay).
- Bugs in evaluation never corrupt source-of-truth evidence.
- New controls can be evaluated retroactively against existing evidence.

## 4.4 Control-as-code (distinct from policy-as-code and detect-as-code)

A **control** is authored as a small bundle:

- A YAML/JSON manifest declaring metadata (id, framework mappings via SCF, applicability_expr, freshness class, owner).
- One or more **evidence queries** — Rego/SQL/JSON-path/Sigma over the evidence ledger.
- One or more **enforcement hooks** (optional) — OPA/Custodian/Kyverno policies that prevent drift in the source system.
- A **manual-evidence schema** (optional) — when the control requires human attestation, the form schema for that.
- Tests — fixture evidence + expected pass/fail.

| Concept | Substrate | Runtime | Output |
|---------|-----------|---------|--------|
| Control-as-code | OSCAL component-definition + bundle | Evaluation stage | Control state |
| Policy-as-code | OPA Rego, Cloud Custodian, Kyverno | Source-system runtime | Drift prevented |
| Detect-as-code | Sigma, Panther, Snowflake SQL | SIEM / detection runtime | Alerts (out of scope) |

Conflation of these is the most common error in the GRC engineering discourse. We pick clear boundaries and stick to them. See [grc.engineering](https://grc.engineering/) for the broader manifesto context.

## 4.5 Manual evidence as first-class

Manual controls render the same UI surface as automated ones: a control card with its mappings, freshness clock, current state. The difference is the evaluation source — for `manual_periodic`, an authorized owner uploads evidence (a screenshot, a signed PDF, a meeting log) on the schedule; for `manual_attested`, a roleholder asserts the state with a digital acknowledgment that becomes an evidence record. Both feed the same evidence ledger.

If the org has 30% manual controls, that 30% looks no less rigorous in the dashboard than the automated 70%. This is intentional — pretending manual controls don't exist is the most common path to "the tool says we're green but we're not."

## 4.6 Security questionnaires (CAIQ, SIG, HECVAT, customer)

Security questionnaires are the dominant inbound demand on a security-product startup's GRC program — every prospect of meaningful size sends a CAIQ, a SIG Lite, or a bespoke Word doc before contract. Practitioner research consistently shows: **the same answers get rewritten for every customer, in spreadsheets, by hand**, even when the org owns Vanta or Drata. This is the highest-ROI workflow we ship.

### 4.6.1 The questionnaire is a graph node

Every question is a first-class node in the same UCF graph that holds framework requirements (see [`UCF_GRAPH_MODEL.md`](../UCF_GRAPH_MODEL.md)). Questions are mapped to **SCF anchors** with STRM-typed edges, exactly like framework requirements:

```
QuestionnaireQuestion[CAIQ-IAM-02] --equal/1.0--> SCF:IAC-06 (MFA)
QuestionnaireQuestion[SIG-G.1.1]   --equal/0.9--> SCF:IAC-06
QuestionnaireQuestion[HECVAT-AAAI-04] --subset_of/1.0--> SCF:IAC-06
```

Once mapped, **answering one question generates a candidate answer for every equivalent question across questionnaires**. A CAIQ answer about MFA pre-fills the SIG question and the HECVAT question. The UCF graph's payoff extends from "one control satisfies six frameworks" to "one answer pre-populates six questionnaires."

### 4.6.2 Entities

| Entity | Purpose | Key fields |
|---|---|---|
| `Questionnaire` | A template (CAIQ v4.1, SIG Lite 2026, custom-customer-X) | `id`, `name`, `version`, `source` (csa/shared_assessments/educause/custom), `domain_taxonomy[]`, `license_class` (free/restricted/proprietary), `import_policy` |
| `QuestionnaireQuestion` | One question in a template | `id`, `questionnaire_id`, `code` (e.g., `IAM-02`), `domain`, `text`, `answer_type` (yes_no_na / scaled / freeform), `linked_scf_anchors[]` (with strength) |
| `QuestionnaireResponse` | An instance — "our answers to CAIQ for customer X, on date Y" | `id`, `questionnaire_id`, `for_customer` (or `for_org` for self-published), `period_id` (frozen evidence window), `status` (draft / under_review / approved / sent), `pdf_export_uri` |
| `QuestionnaireAnswer` | One answer within a response | `id`, `response_id`, `question_id`, `answer_value`, `narrative`, `cited_evidence_ids[]`, `cited_policy_ids[]`, `cited_control_ids[]`, `authored_by`, `ai_assisted` (bool), `ai_model` (if assisted), `human_approved` (bool), `human_approver` (if approved) |
| `AnswerLibrary` | Reusable canonical answers — the "we always say this for MFA" pattern | `id`, `scf_anchor_id`, `canonical_text`, `last_reviewed_at`, `review_owner` |

### 4.6.3 License posture (the part that matters)

[Research, May 2026.](./sources.md)

| Questionnaire | Current version | Ships in security-atlas? | Mechanism |
|---|---|---|---|
| **CAIQ v4.1** (CSA) | 283 questions, 17 domains, Dec 2025 | **No (template not bundled)** — but ingest + answer flow ships v1 | User downloads from CSA, imports the file. Avoids CSA commercial-embed license. |
| **CAIQ-Lite v4.1** (CSA) | 138 questions | Same as above | Same. |
| **SIG 2026 Lite** (Shared Assessments) | ~128 questions | **No** — Shared Assessments membership is members-only (~$7,200/yr). Ingest customer-supplied responses only. | Import from customer-provided file. |
| **SIG 2026 Core** (Shared Assessments) | ~855 questions | Same. | Same. |
| **HECVAT 4.1.5** (EDUCAUSE/REN-ISAC) | 321 questions, free | **Yes — bundled** | Ships in `questionnaire_templates/` with default SCF mappings. |
| **VSAQ** (Google, Apache-2.0, unmaintained) | n/a | Yes — reference schema only | Bundled as a schema example, not a live questionnaire. |
| **Custom customer questionnaires** | Word/Excel/PDF | Yes — universal import | Parser extracts questions; user maps each to SCF (AI-suggested) once; future receipts auto-map. |
| **NIST CSF / CIS CSAT self-assessments** | Free | v2 | Bundled as orthogonal "internal assessment" templates. |

**Concrete OSS stance:** ship the SCF crosswalk and HECVAT bundled (both permissive). For CAIQ and SIG, we ship the *machinery* (ingest, mapping, AI-assist, export), not the *content*. The user provides the file; we provide the workflow.

### 4.6.4 Workflows

**Inbound (customer sent us a questionnaire):**

```
Customer sends CAIQ.xlsx
    │
    ▼
Import — parse to QuestionnaireQuestion rows
    │
    ▼
SCF mapping — match each question to SCF anchors
  - Cached for canonical questionnaires (CAIQ, SIG, HECVAT)
  - AI-suggested for bespoke; human approves the mapping once,
    then it's permanent for that template
    │
    ▼
Answer drafting per question:
  1. Check AnswerLibrary for SCF anchor → use canonical answer if present
  2. Else: RAG over evidence ledger + policies, scoped to mapped SCF anchor
  3. Generate candidate answer with required citations to evidence_id / policy_id
  4. Show DRAFT to human reviewer with "approve / edit / reject"
    │
    ▼
Reviewer approves answer-by-answer (no bulk-approve all)
    │
    ▼
Export — PDF + Excel (CAIQ format) / + customer's original format
    │
    ▼
QuestionnaireResponse pinned to evidence period (audit-period freezing)
```

**Outbound (publish our own CAIQ/HECVAT):**

The org maintains its own CAIQ-formatted self-attestation as a *living artifact* derived from current evidence. Whenever evidence drifts past freshness or a control fails, the published CAIQ flags out-of-date answers. A staleness banner gates re-publish.

### 4.6.5 AI-assist boundary (explicit)

This is the highest-risk feature in the entire platform. Practitioner research is unambiguous: **auditors and prospects roll eyes at AI-generated security questionnaire responses** that hallucinate control claims. Our boundary is hard:

| Allowed | Not allowed |
|---|---|
| AI suggests a draft answer with **mandatory citations** to specific evidence IDs and/or policy IDs. | AI publishes any answer without one-click human approval. |
| AI explains gaps ("evidence covers SCF:IAC-06 but freshness is 95 days, consider re-running before answering"). | AI fabricates control coverage that has no evidence backing. |
| AI suggests SCF mapping for an unmapped question; human approves once, mapping is canonical thereafter. | AI auto-approves its own mappings. |
| AI summarizes prior responses for similarity matching. | AI uses Tenant A's confidential prior answer to seed Tenant B's draft. |

Provenance is enforced at the schema level: `QuestionnaireAnswer.ai_assisted=true` answers cannot have `human_approved=true` without `human_approver` set, and the audit log shows model name + version + timestamp + diff between AI draft and final.

**Inference backend is pluggable:**

- **Default: local Ollama** with a small instruction-tuned model (`llama3.1:8b`, `qwen2.5:14b`, or similar). Ships in the docker-compose. No data leaves the deployment.
- **Optional: cloud** — Anthropic, OpenAI, or Bedrock via API key. Off by default. When enabled, the deployment owner explicitly opts in per-tenant; a banner indicates "AI assist routes to {provider}" wherever drafts appear.
- **The grounding stack is the same** for either backend: pgvector or Qdrant for embeddings of (a) prior approved answers, (b) policy chunks, (c) recent evidence summaries. Citations are required at retrieval, not generation — the model can only cite documents that the retriever returned.

### 4.6.6 Roadmap placement

| Phase | What we ship |
|---|---|
| **v1** | Universal questionnaire import (Excel/CSV/JSON/Word). HECVAT bundled. CAIQ/SIG ingest of customer-provided files. Manual answer authoring with cited evidence. AnswerLibrary for canonical SCF-anchored answers. PDF export. **No AI-assist yet.** |
| **v2** | Local-Ollama AI-assisted drafting with mandatory citations. Cloud-LLM optional. Inbound questionnaire batch processing. Per-customer answer libraries with diff/review. |
| **v3** | Native CAIQ + HECVAT publish to STAR Registry / trust center. Outbound self-published questionnaires that auto-stale on evidence drift. Optional CSA membership integration to bundle CAIQ template. Vendor-questionnaire portability across portfolio orgs. |

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 3. UCF](./03-ucf.md) · **Next:** [5. Scopes →](./05-scopes.md)
