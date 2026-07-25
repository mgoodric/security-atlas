// Package schemaregistry is the contract-enforcement point for every
// evidence_kind. Each kind has a stable identifier, a JSON Schema (draft
// 2020-12), an owner, default SCF anchor mappings, and a semver. Tenants
// can register private kinds for custom internal tools without touching
// the global namespace — the OpenTelemetry-semantic-conventions analog
// (canvas §4.1; EVIDENCE_SDK §4.5).
//
// Surface:
//   - Registry interface: the lookup + validate contract every caller
//     (today: evidence ingest service, schema HTTP handler, future:
//     slice 013 push validator) depends on.
//   - InMemory: thread-safe in-memory store. Used by the gRPC evidence
//     service and as the inner cache for the DB-backed Service.
//   - Service: DB-backed registry. Reads/writes evidence_kind_schemas,
//     loads the bundled platform schemas at boot, validates JSON
//     payloads against registered schemas using draft 2020-12.
//
// The interface stays narrow on purpose: services that only need
// IsRegistered (slice 003 wire-format check) don't pull in the
// validator. ValidatePayload is the slice 013 hook.
package schemaregistry

import (
	"sync"
)

// Registry is the runtime surface every caller depends on.
type Registry interface {
	// IsRegistered returns true if (kind, semver) is known. Slice 003 calls
	// this before accepting a push.
	IsRegistered(kind, version string) bool
}

// PayloadValidator is the slice 013 hook: validate the JSON-encoded
// payload against the registered JSON Schema for (kind, semver). Returns
// nil if the payload conforms; an error describing the first failure
// otherwise.
type PayloadValidator interface {
	ValidatePayload(kind, version string, payload []byte) error
}

// KindVersion is one (kind, semver) pair. Kept for backwards compatibility
// with the slice-003 evidence service which seeds the in-memory registry
// from a slice.
type KindVersion struct {
	Kind    string
	Version string
}

// InMemory is a thread-safe in-memory registry. The zero value is unusable;
// use New to seed.
type InMemory struct {
	mu    sync.RWMutex
	kinds map[string]map[string]struct{} // kind -> version -> {}
}

// New returns a registry seeded with the supplied kinds. Tests can pass
// nil to start empty.
func New(seed []KindVersion) *InMemory {
	r := &InMemory{kinds: map[string]map[string]struct{}{}}
	for _, kv := range seed {
		r.register(kv.Kind, kv.Version)
	}
	return r
}

// DefaultSeed returns the starter set of evidence kinds the platform knows
// about at boot when no DB-backed Service is available. Slice 014 ships
// the same kinds plus the slice-044 GitHub kinds via embedded JSON
// Schemas; this slim fallback exists for unit tests that don't want to
// spin up the file loader.
//
// Canonical evidence_kind identifier convention (Plans/EVIDENCE_SDK.md
// §4.5): the Kind string is `.v<major>`-suffixed (`osquery.host_posture.v1`)
// and the schema version is a SEPARATE semver (`1.0.0`). The `.v<major>`
// suffix is part of the stable identifier; the semver tracks additive
// minor / patch evolution within that major. Every on-the-wire consumer
// honors this: the 9 first-party connectors, the per-language SDKs, the
// push CLI, the bundled JSON Schemas' `x-evidence-kind`, and the SOC 2
// control bundles' `evidence_kind` references. Do NOT reintroduce
// bare-name kinds here — slice 068 fixed exactly that drift (the SOC 2
// control bundles had drifted to bare names, breaking fresh-deploy
// control-bundle upload). Keep this set aligned with the `schemas/*/`
// directory's `x-evidence-kind` values; `internal/control` ships a
// drift-guard test that fails the build if they diverge.
func DefaultSeed() []KindVersion {
	return []KindVersion{
		{Kind: "sast.scan_result.v1", Version: "1.0.0"},
		{Kind: "access_review.completion.v1", Version: "1.0.0"},
		{Kind: "manual.attestation.v1", Version: "1.0.0"},
		{Kind: "aws.s3.bucket_encryption_state.v1", Version: "1.0.0"},
		{Kind: "github.repo_protection.v1", Version: "1.0.0"},
		{Kind: "github.audit_event.v1", Version: "1.0.0"},
		{Kind: "github.scim_user.v1", Version: "1.0.0"},
		{Kind: "okta.mfa_policy.v1", Version: "1.0.0"},
		{Kind: "okta.app_assignment.v1", Version: "1.0.0"},
		{Kind: "okta.user_lifecycle.v1", Version: "1.0.0"},
		{Kind: "1password.org_policy.v1", Version: "1.0.0"},
		{Kind: "osquery.host_posture.v1", Version: "1.0.0"},
		{Kind: "jira.ticket_evidence.v1", Version: "1.0.0"},
		{Kind: "manual.upload.v1", Version: "1.0.0"},
		// Slice 486: Azure connector (Entra ID + Storage).
		{Kind: "azure.entra_role_assignment.v1", Version: "1.0.0"},
		{Kind: "azure.storage_account_config.v1", Version: "1.0.0"},
		// Slice 519: Azure connector AKS managed-cluster hardening posture
		// (cloud-config / network controls: SCF CFG-02 / NET-04). ARM Reader
		// only — never admin kubeconfig, secrets, or workload manifests.
		{Kind: "azure.aks_cluster_config.v1", Version: "1.0.0"},
		// Slice 520: Azure connector NSG / firewall security-rule posture
		// (network-segmentation controls: SCF NET-04 / NET-01). ARM Reader only
		// — RULE configuration only, never flow logs, packet captures, or
		// traffic contents; read-only (never mutates a network resource).
		{Kind: "azure.nsg_rules.v1", Version: "1.0.0"},
		// Slice 521: Azure connector Key-Vault access-policy / RBAC posture
		// (secrets-management / least-privilege controls: SCF IAC-21 / CRY-09).
		// ARM Reader only — management-plane CONFIGURATION + access-policy /
		// role-assignment METADATA only, NEVER a secret/key/certificate VALUE
		// (the connector never touches the Key-Vault data plane and is never
		// granted any data-plane permission).
		{Kind: "azure.keyvault_access_config.v1", Version: "1.0.0"},
		// Slice 614: Azure connector Azure-Firewall rule-collection posture
		// (network-segmentation / boundary-protection controls: SCF NET-04 /
		// NET-01 — the SAME anchors as the NSG kind). ARM Reader only — RULE
		// configuration only (network + application rule collections + the
		// rule-collection-group priority ordering), never flow logs, packet
		// captures, traffic contents, NAT-rule secrets, threat-intel feeds, or
		// route tables; read-only (never mutates a network resource).
		{Kind: "azure.firewall_rules.v1", Version: "1.0.0"},
		// Slice 487: Kubernetes connector (RBAC + workload security-context).
		{Kind: "k8s.rbac_binding.v1", Version: "1.0.0"},
		{Kind: "k8s.workload_security_context.v1", Version: "1.0.0"},
		// Slice 523: Kubernetes connector NetworkPolicy coverage posture
		// (network-segmentation controls: SCF NET-04 / NET-01). Read-only
		// (get/list on networking.k8s.io/v1 networkpolicies + core namespaces) —
		// NetworkPolicy SPEC metadata only, NEVER pod contents, container env,
		// secrets, the peer/CIDR/port contents of a rule block, or traffic.
		// Slice 622 extends the same 1.0.0 kind additively: when a CNI policy CRD
		// is present (Cilium cilium.io / Calico crd.projectcalico.org), its policy
		// SPEC metadata folds into the per-namespace default-deny assessment and an
		// optional per-policy `source` + namespace `sources` set distinguishes the
		// enforcement plane. Both fields are optional — no version bump.
		{Kind: "k8s.networkpolicy_coverage.v1", Version: "1.0.0"},
		// Slice 524: Kubernetes connector Pod-Security-Standards admission config
		// (configuration-baseline control: SCF CFG-02). Read-only (get/list on
		// core namespaces — NO new ClusterRole rule; reuses the base grant). PSS
		// LABEL configuration only (the pod-security.kubernetes.io/enforce|audit|
		// warn levels + optional pinned versions), NEVER pod specs, container env,
		// secrets, logs, or any other namespace label/annotation. The ENFORCED side
		// of workload hardening, complementing k8s.workload_security_context.v1.
		{Kind: "k8s.pod_security_admission.v1", Version: "1.0.0"},
		// Slice 525: Kubernetes connector Secret-inventory (metadata-only). The
		// OPT-IN mode that adds the ONE `secrets` get/list ClusterRole grant the
		// base connector intentionally withholds. Read-only; Secret METADATA ONLY
		// — type / namespace / name / age / key-NAMES (the .data MAP KEYS), NEVER
		// a Secret VALUE (.data / .stringData, raw or base64). The collector
		// struct physically cannot hold a value; a reflection guard + a
		// fixture-with-real-data drop test fail the build if one is added.
		// Anchors CRY-01 (cryptographic controls) + CRY-09 (key management); the
		// spec's candidate IAC-22 is absent from the bundled SCF catalog, so
		// CRY-09 — the closest real anchor for secret-material lifecycle — is used
		// (decisions-log D2).
		{Kind: "k8s.secret_inventory.v1", Version: "1.0.0"},
		// Slice 652: Kubernetes connector admission-webhook + policy-engine evidence
		// (the #524 PSS follow-on). The deliberate, FLAGGED ClusterRole expansion:
		// the base role gains the admissionregistration.k8s.io
		// validatingwebhookconfigurations + mutatingwebhookconfigurations get/list
		// rule (gating k8s.admission_webhook.v1), plus OPTIONAL policy-engine get/list
		// rules (templates.gatekeeper.sh + kyverno.io) detected by API-discovery probe
		// (gating k8s.admission_policy.v1). CONFIG metadata only — NEVER the webhook
		// caBundle/TLS key, the policy Rego/CEL decision-logic body, or an intercepted
		// payload (structural reflection guard + caBundle/Rego/CEL drop tests). Both
		// anchored to CFG-02 (the same configuration-hardening anchor the PSS admission
		// + webhook kinds share). Read-only get,list, no wildcard, no `secrets`.
		{Kind: "k8s.admission_webhook.v1", Version: "1.0.0"},
		{Kind: "k8s.admission_policy.v1", Version: "1.0.0"},
		// Slice 488: monitoring connectors (Datadog + Grafana) — shared
		// alert/monitor configuration-inventory evidence kind.
		{Kind: "monitoring.alert_config.v1", Version: "1.0.0"},
		// Slice 535: monitoring connectors (Datadog + Grafana) — shared
		// alert-FIRING-history evidence kind, the firing-history sibling of
		// monitoring.alert_config.v1. Slice 488 reads which alerts are CONFIGURED
		// (CC7.2); this reads what actually FIRED and resolved (CC7.3/CC7.4): one
		// record per firing event (rule_id, vendor, fired_at, resolved_at, state,
		// routing-target HANDLE). Anchors MON-01 + IRO-09 (the spec candidate
		// IRO-02 is absent from the bundled SCF fixture; IRO-09 is the closest
		// present incident anchor — decisions-log D3). Read-only (Datadog
		// events_read GET /api/v1/events; Grafana Viewer GET /api/v1/rules/history),
		// bounded PULL over a look-back window (NOT continuous monitoring, NOT
		// event-driven). Firing METADATA only — never the alert message body, the
		// triggering metric values, the secret webhook URL, or recipient PII (the
		// record struct has no field that can hold them; a reflection guard fails
		// the build if one is added).
		{Kind: "monitoring.alert_firing.v1", Version: "1.0.0"},
		// Slice 533: Datadog Cloud-SIEM / Security-Monitoring detection-rule
		// inventory — the deliberate slice-488 D1 sibling-kind SPLIT (a detection
		// rule carries a severity + detection-class field monitoring.alert_config
		// lacks, so it gets its own kind: MON-01 + THR-01). Read-only
		// (security_monitoring_rules_read); RULE configuration only — never firing
		// signals, raw log samples, matched-event payloads, secret notification
		// targets, recipient PII, or the raw detection query.
		{Kind: "datadog.siem_rule.v1", Version: "1.0.0"},
		// Slice 636: Datadog Cloud-SIEM signal-history triage outcomes — the
		// slice-533 CC7.3 sibling. Slice 533 reads detection-rule CONFIGURATION
		// (CC7.2: which rules exist); this reads what FIRED and how it was TRIAGED
		// (CC7.3 incident response: rules fired + were triaged over the audit
		// period, when, by whom): THR-01 + IRO-09. Read-only
		// (security_monitoring_signals_read), bounded PULL over a look-back window
		// (NOT continuous monitoring, NOT event-driven). Triage METADATA only —
		// never the signal message body, matched log/event samples, the
		// matched-event payload, the raw detection query, signal-body tags, or a
		// recipient/triager email (the record struct has no field that can hold
		// them; a reflection guard fails the build if one is added).
		{Kind: "datadog.siem_signal.v1", Version: "1.0.0"},
		// Slice 534: Grafana connector authn/authz CONFIG evidence — the
		// deliberate slice-488 deferred authn/authz surface (P0-488-7). Proves
		// SSO is enforced + access is role-based (SOC 2 CC6.1/CC6.2/CC6.3): SCF
		// IAC-06 Authenticator Management / IAC-22 Least Privilege. A SIBLING
		// kind to monitoring.alert_config.v1 because this is an IAM surface, not
		// a monitoring surface. Read-only (sso-settings + access-control reads);
		// CONFIGURATION + COUNTS only — never a SAML private key, an OAuth client
		// secret, an LDAP bind password, a signing certificate, or any individual
		// user / team-member / role-assignment identity (the record struct has no
		// field that can hold them; a reflection guard fails the build if one is
		// added).
		{Kind: "grafana.access_config.v1", Version: "1.0.0"},
		// Slice 489: PagerDuty connector (incident-response evidence) —
		// on-call coverage + bounded-window incident summaries.
		{Kind: "pagerduty.oncall_coverage.v1", Version: "1.0.0"},
		{Kind: "pagerduty.incident_summary.v1", Version: "1.0.0"},
		// Slice 538: PagerDuty connector postmortem / retrospective METADATA —
		// the deliberate slice-489 over-collection follow-on (P0-489-7). Proves
		// incidents are reviewed + corrective actions tracked (SOC 2 CC7.5; the
		// slice-372 IR continuous-improvement loop): SCF IRO-13 Root-Cause
		// Analysis / IRO-09 Incident Reporting. META-ONLY — existence, status,
		// timestamps, and the corrective-action COUNT + completed/open rollup;
		// NEVER the postmortem narrative / timeline / root-cause prose, an
		// action-item title, customer data, or responder PII (the struct has no
		// field that can hold it; a reflection guard fails the build if one is
		// added).
		{Kind: "pagerduty.postmortem_summary.v1", Version: "1.0.0"},
		// Slice 539: PagerDuty connector SERVICE-/TEAM-level incident-response
		// performance AGGREGATES (MTTA / MTTR — mean + p50/p90/p95 + counts) over a
		// bounded look-back window — the deliberate slice-489 over-collection
		// follow-on (P0-489-7). Proves incidents are acknowledged + resolved within
		// target windows at the PROGRAM level (SOC 2 CC7.4): SCF IRO-02 Incident
		// Handling / MON-02 Continuous Monitoring. AGGREGATE-ONLY — the grain is the
		// service, NEVER a named responder; the connector never decodes which
		// individual acknowledged or resolved an incident (the record struct has no
		// field that can hold a responder identity; a reflection guard fails the
		// build if one is added). NOT a per-engineer scorecard.
		{Kind: "pagerduty.response_metrics.v1", Version: "1.0.0"},
		// Slice 490: MDM connectors (Jamf + Intune) — shared managed-device
		// endpoint-posture summary evidence kind (SOC 2 CC6.7 / CC6.8, ISO A.8).
		{Kind: "endpoint.device_posture.v1", Version: "1.0.0"},
		// Slice 555: MDM connectors (Jamf + Intune) — shared managed-device
		// installed-software inventory evidence kind, the deliberate slice-490
		// over-collection follow-on (patch-/vuln-mgmt + asset inventory:
		// SCF VPM-04 / AST-03).
		{Kind: "endpoint.software_inventory.v1", Version: "1.0.0"},
		// Slice 556: MDM connectors (Jamf + Intune) — shared managed-device
		// configuration-profile detail evidence kind (which compliance/config
		// profiles are deployed + their compliance-relevant settings), evidence
		// for configuration-management controls (SCF CFG-02 / CFG-04). Secrets
		// embedded in profiles (Wi-Fi PSKs, VPN shared secrets, certificate
		// private keys, SCEP challenges, payload blobs) are structurally redacted.
		{Kind: "endpoint.config_profile.v1", Version: "1.0.0"},
		// Slice 491: HRIS connectors (Rippling + BambooHR) — shared
		// worker-lifecycle (joiner/mover/leaver) evidence kind feeding the
		// access-review + deprovisioning controls (SOC 2 CC6.1/CC6.2/CC6.3).
		{Kind: "hris.worker_lifecycle.v1", Version: "1.0.0"},
		// Slice 571: HRIS connectors (Rippling + BambooHR) — shared
		// manager-hierarchy evidence kind derived from the same roster's manager
		// ASSIGNMENT id. Feeds access-review approver-chain routing + orphaned-report
		// detection (terminated/absent manager): SCF IAC-22 / IAC-09. Opaque
		// assignment ids only — NEVER manager personal contact detail (the slice-491
		// identity boundary).
		{Kind: "hris.manager_hierarchy.v1", Version: "1.0.0"},
		// Slice 023: policy acknowledgment workflow. Each
		// POST /v1/policies/{id}/acknowledge emits one record of this
		// kind through the slice-013 evidence ledger.
		{Kind: "policy.acknowledgment.v1", Version: "1.0.0"},
		// Slice 443: Slack connector — three high-signal evidence surfaces for
		// the SaaS-startup persona that runs operations in Slack. Read-only
		// least-privilege OAuth token; membership / admin / retention METADATA
		// ONLY — never message content, DMs, or channel history (threat-model
		// I; a reflection guard + an over-collection test fail the build if a
		// message-body field is ever added). Pull profile on an honest interval;
		// platform wire stays push (invariant #3).
		//   - workspace_member: access evidence (who has workspace access +
		//     admin/owner role + 2FA enforcement). Anchors IAC-01 (identity &
		//     access mgmt) + IAC-06 (authenticator mgmt — the 2FA dimension).
		//   - admin_audit_event: admin-action evidence from the audit-logs API.
		//     Anchor MON-01 (continuous monitoring — same anchor as
		//     github.audit_event.v1).
		//   - retention_settings: data-retention evidence (message/file
		//     retention durations + policy flags). Anchors DCH-01 (data
		//     protection) + DCH-03 (retention).
		{Kind: "slack.workspace_member.v1", Version: "1.0.0"},
		{Kind: "slack.admin_audit_event.v1", Version: "1.0.0"},
		{Kind: "slack.retention_settings.v1", Version: "1.0.0"},
		// Slice 442: GCP connector — the highest-demand missing cloud after
		// AWS, for the SaaS-startup persona that runs on GCP. ONE connector,
		// TWO evidence surfaces. Read-only least-privilege identity (ADC /
		// service-account key, roles/iam.securityReviewer +
		// roles/storage.bucketViewer); CONFIGURATION + binding METADATA ONLY —
		// never stored object contents, a service-account KEY, or a secret
		// VALUE (threat-model I; a reflection guard + an over-collection test
		// fail the build if a content/secret field is ever added). Pull profile
		// on an honest interval; platform wire stays push (invariant #3).
		//   - iam_policy_binding: access evidence (one record per project-IAM
		//     (role, member) binding + service-account inventory facts).
		//     Anchors IAC-21 (Privileged Account Management — the "who has what
		//     role" surface) + IAC-15 (Account Review — feeds access review).
		//     The spec candidate IAC-22 (Least Privilege) is ABSENT from the
		//     bundled SCF catalog fixture, so IAC-21 — the closest present
		//     privileged-access anchor — is the primary (decisions-log D3).
		//   - storage_bucket_config: bucket hardening evidence (encryption,
		//     public-access prevention, uniform access, versioning, retention).
		//     Anchors CRY-04 (Encryption At Rest) + NET-04 (Boundary Protection
		//     — the public-access dimension); the SAME pair the analogous
		//     azure.storage_account_config.v1 kind uses.
		{Kind: "gcp.iam_policy_binding.v1", Version: "1.0.0"},
		{Kind: "gcp.storage_bucket_config.v1", Version: "1.0.0"},
	}
}

// IsRegistered returns true if (kind, version) is known.
func (r *InMemory) IsRegistered(kind, version string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if versions, ok := r.kinds[kind]; ok {
		_, present := versions[version]
		return present
	}
	return false
}

// Register adds a (kind, semver) pair to the in-memory registry. Used by
// the DB-backed Service to seed its inner cache after a successful insert.
func (r *InMemory) Register(kind, version string) {
	r.register(kind, version)
}

func (r *InMemory) register(kind, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kinds[kind]; !ok {
		r.kinds[kind] = map[string]struct{}{}
	}
	r.kinds[kind][version] = struct{}{}
}
