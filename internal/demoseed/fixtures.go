package demoseed

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Per-primitive row-count floors at scale=1.0. Multiplied by Seeder.scale
// + rounded to int. Documented in AC-5.
const (
	controlsFloor     = 50
	risksFloor        = 20
	evidenceFloor     = 200
	policiesFloor     = 5
	vendorsFloor      = 10
	auditPeriodsFloor = 3 // 1 frozen + 2 open (AC-10)
	walkthroughsFloor = 5
	exceptionsFloor   = 10
	boardBriefsFloor  = 2
	boardPacksFloor   = 1
	frameworkScopeNum = 3 // SOC 2 + ISO 27001 + NIST CSF (D5)
	samplesPerPeriod  = 5
	auditTrailFloor   = 50
)

// applyScale multiplies a floor by the seeder's scale knob and rounds
// to the nearest int with a minimum of 1 (AC-6: every primitive has
// at least one row even at scale=0.1).
func (s *Seeder) applyScale(floor int) int {
	n := int(math.Round(float64(floor) * s.scale))
	if n < 1 {
		return 1
	}
	return n
}

// tenantRow is the demo tenant's identity. One per Apply.
type tenantRow struct {
	ID                uuid.UUID
	Name              string
	Slug              string
	IsBootstrapTenant bool
}

// scopeRow holds the seeded scope dimension + cell uuids so the
// fixture builder can reference them when sizing evidence_records
// (every evidence row optionally references a scope).
type scopeRow struct {
	DimensionID uuid.UUID
	CellID      uuid.UUID
}

// userRow is the demo administrator user.
type userRow struct {
	ID    uuid.UUID
	Email string
	Name  string
}

// controlFixture is one row of `controls`.
type controlFixture struct {
	ID                 uuid.UUID
	SCFID              string // SCF code (matches catalog families); plain TEXT — not FK
	Title              string
	Description        string
	Family             string
	ImplementationType string // 'automated' | 'semi_automated' | 'manual_attested' | 'manual_periodic'
	OwnerRole          string
	Lifecycle          string // always 'active' for demo
}

// riskFixture is one row of `risks`.
type riskFixture struct {
	ID              uuid.UUID
	Title           string
	Description     string
	Category        string // confidentiality | integrity | availability | privacy | regulatory | operational | financial
	Treatment       string // accept | mitigate | transfer | avoid
	TreatmentOwner  string
	InherentScoreJ  string // {"likelihood":3,"impact":4} -> 12
	ResidualScoreJ  string
	ReviewDueAt     time.Time
	LinkedControlID uuid.UUID // links to one of the seeded controls
}

// evidenceFixture is one row of `evidence_records`. The seeder spreads
// these across the prior 12 months per AC-8.
type evidenceFixture struct {
	ID           uuid.UUID
	ControlID    uuid.UUID
	ScopeID      *uuid.UUID
	ObservedAt   time.Time
	Result       string // pass | fail | na | inconclusive
	EvidenceKind string // e.g. "osquery.host_posture"
	FreshnessCl  string // realtime | daily | weekly | monthly | quarterly | annual
	Payload      map[string]any
	Provenance   map[string]any
	HashHex      string
}

// policyFixture is one row of `policies`.
type policyFixture struct {
	ID            uuid.UUID
	Title         string
	BodyMD        string
	Owner         string
	Approver      string
	Status        string // draft | under_review | approved | published | superseded
	EffectiveDate time.Time
}

// vendorFixture is one row of `vendors`.
type vendorFixture struct {
	ID             uuid.UUID
	Name           string
	Domain         string
	Criticality    string // low | medium | high
	ContractStart  time.Time
	ContractEnd    time.Time
	DPASigned      bool
	DPASignedAt    *time.Time
	Cadence        string // monthly | quarterly | biannual | annual
	LastReviewDate time.Time
	OwnerUser      string
	Notes          string
}

// auditPeriodFixture is one row of `audit_periods`. AC-10 requires
// exactly one of these to be frozen (1 frozen + 2 open at scale 1.0).
type auditPeriodFixture struct {
	ID                 uuid.UUID
	Name               string
	FrameworkVersionID uuid.UUID
	PeriodStart        time.Time
	PeriodEnd          time.Time
	Frozen             bool
	FrozenAt           *time.Time
	FrozenBy           string
	CreatedBy          string
	// The slice of evidence rows pinned into this period's sample
	// population (used by writeAuditPeriodsAndSamples to draw samples).
	SampleEvidenceIDs []uuid.UUID
}

// exceptionFixture is one row of `exceptions`. AC requires that we
// cover mixed lifecycle states to render the exceptions surface
// meaningfully.
type exceptionFixture struct {
	ID                uuid.UUID
	ControlID         uuid.UUID
	Justification     string
	CompensatingCtrls []string
	RequestedBy       string
	RequestedAt       time.Time
	ApprovedBy        *string
	ApprovedAt        *time.Time
	ActivatedBy       *string
	ActivatedAt       *time.Time
	EffectiveFrom     *time.Time
	ExpiresAt         time.Time
	Status            string // requested | approved | denied | active | expired
}

// walkthroughFixture is one row of `walkthroughs`. All demo walkthroughs
// are finalized (status='finalized') so the audit-workspace surface
// shows the "ready" state.
type walkthroughFixture struct {
	ID         uuid.UUID
	ControlID  uuid.UUID
	Narrative  string
	Transcript string
	HashBytes  []byte // sha256 of canonical narrative + transcript + control_id
	CreatedBy  string
	Status     string // 'finalized'
}

// boardBriefFixture is one row of `board_briefs`. Frozen-content shape.
type boardBriefFixture struct {
	ID          uuid.UUID
	PeriodEnd   time.Time
	Content     map[string]any
	NarrativeMD string
}

// boardPackFixture is one row of `board_packs`. v1 ships as 'published'.
type boardPackFixture struct {
	ID          uuid.UUID
	PeriodEnd   time.Time
	Content     map[string]any
	NarrativeMD string
	PublishedBy string
	PublishedAt time.Time
}

// fixtureSet is the in-memory dataset the seeder writes in one tx.
//
// Note: framework_scopes rows are NOT pre-built as fixtures — they are
// generated inside Apply() (writeFrameworkScopes path) once the seeder
// reads the available framework_versions from the catalog.
type fixtureSet struct {
	tenant       tenantRow
	scope        scopeRow
	user         userRow
	controls     []controlFixture
	risks        []riskFixture
	policies     []policyFixture
	vendors      []vendorFixture
	auditPeriods []auditPeriodFixture
	evidence     []evidenceFixture
	walkthroughs []walkthroughFixture
	exceptions   []exceptionFixture
	boardBriefs  []boardBriefFixture
	boardPacks   []boardPackFixture
	// frameworkVersionIDs is the pool of catalog framework_versions
	// rows the seeder discovered (read at apply time from the
	// scf_anchors + frameworks tables). The fixture builder cannot
	// know these until the seeder reads them inside the tx, so this
	// slice is populated by Apply itself (writeFrameworkScopes path).
	frameworkVersionIDs []uuid.UUID
	// auditTrailCount records the planned number of audit-trail rows
	// (used by writeDemoAuditTrail to size its loop).
	auditTrailCount int
	// now is the reference timestamp for evidence temporal spread.
	now time.Time
}

// buildFixtures produces every per-row fixture in memory. Pure (no
// DB I/O). The fixture builder applies the seeder's scale knob to
// each per-primitive floor.
//
// The function is intentionally long-form: each primitive's per-row
// values live near each other so a reader can see the dataset shape
// at a glance.
func (s *Seeder) buildFixtures(slug string) *fixtureSet {
	now := s.clock().UTC()
	fs := &fixtureSet{
		tenant: tenantRow{
			ID:                uuid.New(),
			Name:              capitalize(slug) + " " + fictionalCompanyName,
			Slug:              slug,
			IsBootstrapTenant: false,
		},
		scope: scopeRow{
			DimensionID: uuid.New(),
			CellID:      uuid.New(),
		},
		user: userRow{
			ID:    uuid.New(),
			Email: "admin@" + personEmailDomain,
			Name:  fictionalPeople[0].First + " " + fictionalPeople[0].Last,
		},
		now: now,
	}

	// Controls — pick fictional control families spanning common
	// security domains. Each control's SCF ID is left blank (the
	// real SCF anchor IDs are bundled with the platform; the demo
	// seeder doesn't bind to those because they may not be present
	// in every CI database).
	cFamilies := []string{
		"Access Control",
		"Asset Management",
		"Business Continuity",
		"Change Management",
		"Configuration Management",
		"Cryptographic Protections",
		"Data Classification & Handling",
		"Endpoint Security",
		"Human Resources Security",
		"Identification & Authentication",
		"Incident Response",
		"Logging & Monitoring",
		"Network Security",
		"Physical Security",
		"Risk Assessment",
		"Secure Engineering",
		"Third-Party Management",
		"Vulnerability Management",
		"Web Application Security",
	}
	cImplTypes := []string{"automated", "semi_automated", "manual_attested", "manual_periodic"}
	cTitles := []string{
		"Multi-factor authentication required for all human access",
		"Quarterly access review across production systems",
		"Endpoint encryption baseline enforced via MDM",
		"Continuous vulnerability scanning of cloud workloads",
		"Encryption-at-rest on every data store",
		"Annual third-party penetration test",
		"Documented incident response runbook",
		"30-day log retention for production services",
		"Backup verification with quarterly restore drill",
		"Change-management approval gate on production deploys",
		"SAST scanning enabled on every commit",
		"Container image baseline scan",
		"Secrets management via centralized vault",
		"Privileged access management for break-glass accounts",
		"Network segmentation between prod and non-prod",
		"Web application firewall in front of customer-facing apps",
		"DDoS protection on edge endpoints",
		"Anti-malware coverage on every endpoint",
		"Cloud configuration drift detection",
		"Identity-provider single source of truth",
		"OS patching cadence — critical within 14 days",
		"Mobile device management on company-issued devices",
		"Physical access controls at data centers",
		"Visitor log retention for 90 days",
		"Disaster recovery plan tested annually",
		"Business continuity tabletop exercise",
		"Vendor due-diligence questionnaire on onboarding",
		"Annual security awareness training for all staff",
		"Phishing simulation cadence — monthly",
		"Background checks on all new hires",
		"Termination access removal SLA — 24 hours",
		"Service account inventory with quarterly review",
		"Hardware inventory tracked in CMDB",
		"Software inventory via endpoint agent",
		"Data classification labels enforced in document storage",
		"PII tokenization for analytical workloads",
		"Encryption key rotation cadence — annual",
		"TLS 1.2+ enforced on all external endpoints",
		"Code review required before merge",
		"Branch protection on default branches",
		"Two-person rule for production secret access",
		"GDPR data-subject request handling SLA",
		"HIPAA-aligned PHI handling for healthcare data",
		"PCI scope minimization documented",
		"Cloud account boundary controls",
		"Cross-region failover validated quarterly",
		"Time synchronization across infrastructure",
		"Centralized audit log aggregation",
		"User behavior analytics on privileged accounts",
		"Threat intelligence feed integration",
	}
	nControls := s.applyScale(controlsFloor)
	if nControls > len(cTitles) {
		nControls = len(cTitles)
	}
	for i := 0; i < nControls; i++ {
		fs.controls = append(fs.controls, controlFixture{
			ID:                 uuid.New(),
			SCFID:              "",
			Title:              cTitles[i],
			Description:        cTitles[i] + ". Owner ensures continuous operation; deviations recorded as exceptions.",
			Family:             cFamilies[i%len(cFamilies)],
			ImplementationType: cImplTypes[i%len(cImplTypes)],
			OwnerRole:          ownerRoles[i%len(ownerRoles)],
			Lifecycle:          "active",
		})
	}

	// Risks — common security-program risk shapes, fictional asset
	// names from `fictionalAssets`.
	rTitles := []string{
		"Credential stuffing against customer-portal-prod",
		"Insider threat: privileged data access from departing engineer",
		"Ransomware impacting billing-svc-prod",
		"Vendor compromise: third-party SaaS supply chain",
		"Misconfigured S3 bucket exposing PII",
		"Phishing leading to MFA bypass",
		"Zero-day in customer-facing web framework",
		"Cloud account hijack via overprivileged service token",
		"DDoS exhausting CDN credits",
		"Backup integrity loss",
		"Stale access on inactive accounts",
		"Source code exfiltration via developer endpoint",
		"Failed disaster recovery in primary region",
		"Insufficient logging for breach forensics",
		"Customer data leak via support tool screenshot",
		"PII over-collection violating GDPR minimization",
		"Unencrypted laptop loss",
		"Software supply chain (malicious npm dependency)",
		"Misuse of LLM API keys (cost + data exposure)",
		"Drift between documented and deployed network ACLs",
	}
	rCategories := []string{"confidentiality", "integrity", "availability", "privacy", "regulatory", "operational"}
	// All demo risks are treated "mitigate". The other treatments
	// (accept, transfer, avoid) carry per-treatment required fields
	// (accepted_until, instrument_reference, etc.) the demo doesn't
	// need to surface — keeping all rows on "mitigate" sidesteps
	// the CHECK constraints without losing demo value.
	rTreatments := []string{"mitigate"}
	nRisks := s.applyScale(risksFloor)
	if nRisks > len(rTitles) {
		nRisks = len(rTitles)
	}
	for i := 0; i < nRisks; i++ {
		linked := fs.controls[i%len(fs.controls)].ID
		inh := riskScoreJSON(3+(i%3), 3+(i%2)) // likelihood 3-5, impact 3-4
		res := riskScoreJSON(2+(i%2), 2+(i%2))
		fs.risks = append(fs.risks, riskFixture{
			ID:              uuid.New(),
			Title:           rTitles[i],
			Description:     rTitles[i] + ". Treatment owned by " + ownerRoles[i%len(ownerRoles)] + ".",
			Category:        rCategories[i%len(rCategories)],
			Treatment:       rTreatments[i%len(rTreatments)],
			TreatmentOwner:  ownerRoles[i%len(ownerRoles)],
			InherentScoreJ:  inh,
			ResidualScoreJ:  res,
			ReviewDueAt:     now.AddDate(0, 3, 0),
			LinkedControlID: linked,
		})
	}

	// Policies — 5 standard categories. Bodies are short markdown
	// stubs (D1: "polished but obviously fictional").
	pTitles := []string{
		"Information Security Policy",
		"Acceptable Use Policy",
		"Access Control Policy",
		"Incident Response Plan",
		"Vendor Risk Management Policy",
	}
	nPolicies := s.applyScale(policiesFloor)
	if nPolicies > len(pTitles) {
		nPolicies = len(pTitles)
	}
	for i := 0; i < nPolicies; i++ {
		fs.policies = append(fs.policies, policyFixture{
			ID:    uuid.New(),
			Title: pTitles[i],
			BodyMD: "# " + pTitles[i] + "\n\n" +
				"## Purpose\n\nDemo policy body — replace before publishing.\n\n" +
				"## Scope\n\nApplies to all systems and personnel of " + fs.tenant.Name + ".\n\n" +
				"## Policy\n\nDocumented controls follow our security baseline.\n",
			Owner:         ownerRoles[i%len(ownerRoles)],
			Approver:      "CISO",
			Status:        "published",
			EffectiveDate: now.AddDate(0, -6, 0),
		})
	}

	// Vendors — pick from fictionalVendors. Spread criticality + DPA
	// status + review cadence so the vendor surface looks meaningful.
	nVendors := s.applyScale(vendorsFloor)
	if nVendors > len(fictionalVendors) {
		nVendors = len(fictionalVendors)
	}
	vCriticality := []string{"high", "high", "medium", "medium", "medium", "low", "low", "medium", "high", "low"}
	vCadence := []string{"annual", "annual", "biannual", "quarterly", "annual", "biannual", "annual", "annual", "quarterly", "biannual"}
	for i := 0; i < nVendors; i++ {
		v := fictionalVendors[i]
		signed := i%2 == 0
		var signedAt *time.Time
		if signed {
			t := now.AddDate(-1, -i, 0)
			signedAt = &t
		}
		fs.vendors = append(fs.vendors, vendorFixture{
			ID:             uuid.New(),
			Name:           v.Name,
			Domain:         v.Domain,
			Criticality:    vCriticality[i%len(vCriticality)],
			ContractStart:  now.AddDate(-1, 0, 0),
			ContractEnd:    now.AddDate(1, 0, 0),
			DPASigned:      signed,
			DPASignedAt:    signedAt,
			Cadence:        vCadence[i%len(vCadence)],
			LastReviewDate: now.AddDate(0, -3-i%6, 0),
			OwnerUser:      ownerRoles[i%len(ownerRoles)],
			Notes:          "Demo vendor — fictional.",
		})
	}

	// Framework-version IDs are resolved at write-time (by the
	// seeder reading the bundled catalog). Audit-period names + dates
	// are seeded here; their framework_version_id is populated by
	// writeAuditPeriodsAndSamples once the framework lookup completes.
	nPeriods := s.applyScale(auditPeriodsFloor)
	if nPeriods < 3 {
		// AC-10 requires at least 1 frozen + 2 open even at low scale.
		nPeriods = 3
	}
	for i := 0; i < nPeriods; i++ {
		// First period (i==0) is frozen; the rest are open.
		frozen := i == 0
		periodStart := now.AddDate(0, -12+i*4, 0)
		periodEnd := periodStart.AddDate(0, 3, 0)
		ap := auditPeriodFixture{
			ID:          uuid.New(),
			Name:        fmt.Sprintf("SOC 2 %d %s", now.Year(), []string{"Q1", "Q2", "Q3", "Q4"}[i%4]),
			PeriodStart: periodStart,
			PeriodEnd:   periodEnd,
			Frozen:      frozen,
			CreatedBy:   fs.user.Email,
		}
		if frozen {
			ft := periodEnd.AddDate(0, 0, 7) // frozen 7 days after period end
			ap.FrozenAt = &ft
			ap.FrozenBy = fs.user.Email
		}
		fs.auditPeriods = append(fs.auditPeriods, ap)
	}

	// Evidence — spread observed_at across the prior 12 months per
	// AC-8: ~30% within 7 days, ~40% within 30 days, ~20% 30-90,
	// ~10% > 90.
	nEvidence := s.applyScale(evidenceFloor)
	// Each evidence row picks a control + a kind + a temporal band.
	bands := []struct {
		Fraction float64
		MinDays  int
		MaxDays  int
	}{
		{0.30, 0, 7},
		{0.40, 8, 30},
		{0.20, 31, 90},
		{0.10, 91, 365},
	}
	cellID := fs.scope.CellID
	for i := 0; i < nEvidence; i++ {
		// Pick a band by index.
		band := bands[0]
		acc := 0.0
		for _, b := range bands {
			acc += b.Fraction
			if float64(i)/float64(nEvidence) < acc {
				band = b
				break
			}
		}
		// Days back within the band.
		bandLen := band.MaxDays - band.MinDays
		days := band.MinDays + (i % (bandLen + 1))
		obs := now.AddDate(0, 0, -days)
		controlID := fs.controls[i%len(fs.controls)].ID
		kind := evidenceKindsPool[i%len(evidenceKindsPool)]
		result := evidenceResults[i%len(evidenceResults)]
		freshness := evidenceFreshness[i%len(evidenceFreshness)]
		payload := buildEvidencePayload(kind, i)
		provenance := map[string]any{
			"connector":     kindToConnector(kind),
			"connector_run": fmt.Sprintf("demo-run-%d", i),
			"connector_ver": "demo-1.0.0",
		}
		hashBytes, _ := hashCanonicalJSON(map[string]any{
			"control_id":  controlID.String(),
			"observed_at": obs.UTC().Format(time.RFC3339),
			"payload":     payload,
		})
		fs.evidence = append(fs.evidence, evidenceFixture{
			ID:           uuid.New(),
			ControlID:    controlID,
			ScopeID:      &cellID,
			ObservedAt:   obs,
			Result:       result,
			EvidenceKind: kind,
			FreshnessCl:  freshness,
			Payload:      payload,
			Provenance:   provenance,
			HashHex:      hexString(hashBytes),
		})
	}

	// Walkthroughs — N walkthroughs across N controls, all finalized.
	nWalkthroughs := s.applyScale(walkthroughsFloor)
	for i := 0; i < nWalkthroughs; i++ {
		c := fs.controls[i%len(fs.controls)]
		narrative := "Walkthrough for control: " + c.Title + ".\n\n" +
			"Owner stepped through the operating procedure: change initiated in ticketing system, " +
			"two-person approval recorded, change executed via automation, post-change verification logged. " +
			"All steps confirmed working in the production environment."
		transcript := "00:00 Setup\n01:00 Ticket creation\n02:30 Approval gate\n04:00 Execution\n05:00 Verification"
		hash, _ := hashCanonicalJSON(map[string]any{
			"control_id": c.ID.String(),
			"narrative":  narrative,
			"transcript": transcript,
		})
		fs.walkthroughs = append(fs.walkthroughs, walkthroughFixture{
			ID:         uuid.New(),
			ControlID:  c.ID,
			Narrative:  narrative,
			Transcript: transcript,
			HashBytes:  hash,
			CreatedBy:  fs.user.Email,
			Status:     "finalized",
		})
	}

	// Exceptions — mixed lifecycle states.
	nExceptions := s.applyScale(exceptionsFloor)
	exStates := []string{"requested", "approved", "active", "active", "expired", "denied", "active", "active", "expired", "active"}
	for i := 0; i < nExceptions; i++ {
		c := fs.controls[(i*3)%len(fs.controls)]
		state := exStates[i%len(exStates)]
		req := now.AddDate(0, -2-i, 0)
		exp := req.AddDate(0, 6, 0) // 6 months expiry (well under 365d cap)
		ex := exceptionFixture{
			ID:                uuid.New(),
			ControlID:         c.ID,
			Justification:     "Compensating manual review while automated control is implemented.",
			CompensatingCtrls: []string{"Weekly SRE review", "Quarterly manual access audit"},
			RequestedBy:       fs.user.Email,
			RequestedAt:       req,
			ExpiresAt:         exp,
			Status:            state,
		}
		switch state {
		case "approved", "active":
			a := "approver@" + personEmailDomain
			ts := req.AddDate(0, 0, 7)
			ex.ApprovedBy = &a
			ex.ApprovedAt = &ts
			if state == "active" {
				a2 := fs.user.Email
				ts2 := ts.AddDate(0, 0, 1)
				ex.ActivatedBy = &a2
				ex.ActivatedAt = &ts2
				ex.EffectiveFrom = &ts2
			}
		case "expired":
			a := "approver@" + personEmailDomain
			ts := req.AddDate(0, 0, 7)
			ex.ApprovedBy = &a
			ex.ApprovedAt = &ts
		case "denied":
			// approved_by must differ from requested_by (slice 021 SoD).
			// denied path leaves approved fields NULL, denied_by set.
			ex.Status = "denied"
		}
		fs.exceptions = append(fs.exceptions, ex)
	}

	// Board briefs (monthly) + board packs (quarterly).
	nBriefs := s.applyScale(boardBriefsFloor)
	for i := 0; i < nBriefs; i++ {
		periodEnd := now.AddDate(0, -i, 0)
		fs.boardBriefs = append(fs.boardBriefs, boardBriefFixture{
			ID:        uuid.New(),
			PeriodEnd: periodEnd,
			Content: map[string]any{
				"posture_by_framework": map[string]any{
					"SOC 2":     map[string]any{"controls": 47, "passing": 42, "exceptions": 3},
					"ISO 27001": map[string]any{"controls": 47, "passing": 41, "exceptions": 4},
					"NIST CSF":  map[string]any{"controls": 47, "passing": 43, "exceptions": 2},
				},
				"drift_last_30_days": 8,
				"top_risks":          []string{rTitles[0], rTitles[1], rTitles[2]},
				"demo_seed_v":        DemoSeedVersion,
			},
			NarrativeMD: "Monthly board brief — demo content. Posture remains stable across SOC 2, ISO 27001, and NIST CSF.",
		})
	}
	nPacks := s.applyScale(boardPacksFloor)
	for i := 0; i < nPacks; i++ {
		periodEnd := now.AddDate(0, -i*3, 0)
		fs.boardPacks = append(fs.boardPacks, boardPackFixture{
			ID:        uuid.New(),
			PeriodEnd: periodEnd,
			Content:   demoBoardPackContent(periodEnd),
			// Slice 662: a published demo pack is rendered read-only; the
			// list endpoint deserializes `content` into board.Pack, which
			// requires `sections` to be a key→section MAP (not an array)
			// carrying all eight SectionKeys. The prior fixture wrote a
			// 5-element array missing the key field, which failed to
			// unmarshal and 500'd the list endpoint (slice 673) and the
			// detail page. demoBoardPackContent now mirrors board.Pack.
			NarrativeMD: "Quarterly board pack — demo content. See sections above for details.",
			PublishedBy: fs.user.Email,
			PublishedAt: periodEnd.AddDate(0, 0, 7),
		})
	}

	fs.auditTrailCount = s.applyScale(auditTrailFloor)
	return fs
}

// demoBoardPackSection is one entry of the demo pack's section map. It
// mirrors the JSON shape of board.Section (key/title/templated_text/
// override_text/approved/data) so the row deserializes cleanly into
// board.Pack on read. Slice 662.
type demoBoardPackSection struct {
	key, title, text string
	data             map[string]any
}

// demoBoardPackSections is the canonical, ordered set of the eight fixed
// board-pack sections (mirrors board.SectionKeys + board.sectionTitles).
// Kept in-package so the demoseed fixture stays a self-contained
// dependency-free literal; the board package's keys are the source of
// truth and the slice-662 integration test asserts the two stay in sync.
// Slot §05 (vendor_burndown) carries the generated burndown scalars so the
// demo pack renders the §05 visual end-to-end.
func demoBoardPackSections() []demoBoardPackSection {
	return []demoBoardPackSection{
		{key: "posture", title: "Posture summary", text: "Posture remains stable across SOC 2, ISO 27001, and NIST CSF.", data: map[string]any{}},
		{key: "top_risks", title: "Top risks aging", text: "The top residual risks and their aging are summarized below.", data: map[string]any{}},
		{key: "coverage_trend", title: "Coverage trend", text: "Control coverage held flat versus the prior quarter baseline.", data: map[string]any{"coverage_pct": 78, "baseline_coverage_pct": 78, "coverage_delta": 0}},
		{key: "open_findings", title: "Open findings", text: "Open findings are tracked from failing control evaluations.", data: map[string]any{"findings_count": 0}},
		{key: "vendor_burndown", title: "Vendor risk burndown", text: "High-criticality vendor reviews and their on-time burndown.", data: map[string]any{
			"vendor_burndown_total":            8,
			"vendor_burndown_on_time":          6,
			"vendor_burndown_past_due":         2,
			"vendor_burndown_on_time_pct":      75,
			"vendor_burndown_on_time_fraction": 0.75,
		}},
		{key: "operational_metrics", title: "Operational metrics", text: "Operator-entered operational metrics for the quarter.", data: map[string]any{}},
		{key: "investment", title: "Investment vs coverage", text: "Security spend this quarter against coverage delta.", data: map[string]any{"spend_usd": 0, "cost_per_coverage_point": 0}},
		{key: "asks", title: "Asks of the board", text: "Asks of the board this quarter.", data: map[string]any{}},
	}
}

// demoBoardPackContent builds the published demo pack's `content` JSONB in
// the exact shape board.Pack marshals to: a self-describing envelope whose
// `sections` is a key→section MAP carrying all eight SectionKeys, every
// section approved (the pack ships published). Slice 662 — the prior
// array-shaped literal failed board.Pack deserialization.
func demoBoardPackContent(periodEnd time.Time) map[string]any {
	sections := map[string]any{}
	for _, s := range demoBoardPackSections() {
		sections[s.key] = map[string]any{
			"key":            s.key,
			"title":          s.title,
			"templated_text": s.text,
			"override_text":  "",
			"approved":       true,
			"data":           s.data,
		}
	}
	return map[string]any{
		"period_end":   periodEnd.Format("2006-01-02"),
		"generated_at": periodEnd.Format(time.RFC3339),
		"status":       "published",
		"sections":     sections,
		"demo_seed_v":  DemoSeedVersion,
	}
}

// ownerRoles is the cycle of fictional role labels stamped on control
// rows + risk-treatment-owner columns. Keeps the demo's owner column
// from looking mono-tonal.
var ownerRoles = []string{
	"Head of Security",
	"Security Engineering Lead",
	"Compliance Manager",
	"Platform Engineering",
	"IT Operations",
	"CISO",
	"Privacy Officer",
	"DevSecOps",
	"Site Reliability",
	"People Operations",
}

// evidenceKindsPool is the D3 choice — the 12 evidence_kinds the demo
// seeds (out of the 15 registered in internal/api/schemaregistry).
// Selection criteria: span the major connector surfaces (host posture,
// SaaS posture, access reviews, SAST, change reviews, manual).
//
// Documented in docs/audit-log/205-demo-seed-data-decisions.md D3.
var evidenceKindsPool = []string{
	"osquery.host_posture",
	"github.repo_protection",
	"github.audit_event",
	"github.scim_user",
	"okta.app_assignment",
	"okta.mfa_policy",
	"okta.user_lifecycle",
	"aws.s3.bucket_encryption_state",
	"access_review.completion",
	"sast.scan_result",
	"manual.attestation",
	"manual.upload",
}

var evidenceResults = []string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "fail", "inconclusive", "na"}
var evidenceFreshness = []string{"daily", "daily", "weekly", "weekly", "monthly", "monthly", "monthly", "quarterly"}

// kindToConnector maps an evidence_kind to its source connector token.
// Used for provenance rows.
func kindToConnector(kind string) string {
	parts := strings.SplitN(kind, ".", 2)
	if len(parts) == 0 {
		return "unknown"
	}
	return parts[0]
}

// buildEvidencePayload synthesizes a plausible payload for the given
// evidence_kind. Shapes match the JSON Schemas under
// internal/api/schemaregistry/schemas/ (loose match — schema-validator
// is not invoked, but the keys present in the payload are the
// load-bearing ones a reader of the schema would expect).
func buildEvidencePayload(kind string, idx int) map[string]any {
	switch kind {
	case "osquery.host_posture":
		return map[string]any{
			"hostname":         fictionalAssets[idx%len(fictionalAssets)],
			"os_version":       "Ubuntu 22.04 LTS",
			"disk_encryption":  true,
			"firewall_enabled": true,
			"agent_version":    "osquery-5.10.2",
		}
	case "github.repo_protection":
		return map[string]any{
			"repo":               "demo-org/" + fictionalAssets[idx%len(fictionalAssets)],
			"default_branch":     "main",
			"require_pr_reviews": true,
			"required_approvers": 2,
			"enforce_admins":     true,
		}
	case "github.audit_event":
		return map[string]any{
			"actor":   fictionalPeople[idx%len(fictionalPeople)].First,
			"action":  "team.add_member",
			"repo":    "demo-org/" + fictionalAssets[idx%len(fictionalAssets)],
			"created": fmt.Sprintf("2026-0%d-15T12:00:00Z", 1+(idx%9)),
		}
	case "github.scim_user":
		return map[string]any{
			"user_id":          fmt.Sprintf("scim-%d", idx),
			"email":            fmt.Sprintf("user%d@%s", idx, personEmailDomain),
			"active":           idx%5 != 0,
			"provisioned_from": "okta",
		}
	case "okta.app_assignment":
		return map[string]any{
			"app":         "GitHub Enterprise",
			"user":        fmt.Sprintf("user%d@%s", idx, personEmailDomain),
			"assigned_at": fmt.Sprintf("2026-0%d-01T00:00:00Z", 1+(idx%9)),
			"groups":      []string{"engineering", "security"},
		}
	case "okta.mfa_policy":
		return map[string]any{
			"policy_name":   "Production MFA Policy",
			"factor_types":  []string{"webauthn", "okta_verify"},
			"enforced":      true,
			"users_covered": 142,
		}
	case "okta.user_lifecycle":
		return map[string]any{
			"user":   fmt.Sprintf("user%d@%s", idx, personEmailDomain),
			"event":  "USER.LIFECYCLE.DEACTIVATE",
			"actor":  "auto-deprovision",
			"reason": "termination",
		}
	case "aws.s3.bucket_encryption_state":
		return map[string]any{
			"bucket":            fictionalAssets[idx%len(fictionalAssets)] + "-bucket",
			"region":            "us-east-1",
			"sse_algorithm":     "aws:kms",
			"kms_key_id":        "alias/atlas-demo",
			"bucket_versioning": true,
		}
	case "access_review.completion":
		return map[string]any{
			"reviewer":       fictionalPeople[idx%len(fictionalPeople)].First + " " + fictionalPeople[idx%len(fictionalPeople)].Last,
			"system":         fictionalAssets[idx%len(fictionalAssets)],
			"users_reviewed": 47,
			"removals":       3,
			"completed_at":   fmt.Sprintf("2026-0%d-15T17:00:00Z", 1+(idx%9)),
		}
	case "sast.scan_result":
		return map[string]any{
			"repo":           "demo-org/" + fictionalAssets[idx%len(fictionalAssets)],
			"scanner":        "semgrep",
			"critical":       0,
			"high":           idx % 3,
			"medium":         idx % 7,
			"scan_completed": fmt.Sprintf("2026-0%d-12T08:00:00Z", 1+(idx%9)),
		}
	case "manual.attestation":
		return map[string]any{
			"attester":    fictionalUserEmail(idx),
			"control_ref": fictionalAssets[idx%len(fictionalAssets)],
			"statement":   "I attest the documented procedure was followed during the period.",
		}
	case "manual.upload":
		return map[string]any{
			"uploader":    fictionalUserEmail(idx),
			"filename":    fmt.Sprintf("evidence-%d.pdf", idx),
			"size_bytes":  102400 + idx*1024,
			"description": "Quarterly compliance report upload.",
		}
	default:
		return map[string]any{"note": "demo placeholder"}
	}
}

// fictionalUserEmail returns one of the fictional people's lower-case
// first-name @ demo.example. Used by the manual.* evidence payloads
// so the attester/uploader fields look like a real human email.
func fictionalUserEmail(idx int) string {
	p := fictionalPeople[idx%len(fictionalPeople)]
	return strings.ToLower(p.First) + "@" + personEmailDomain
}

// riskScoreJSON returns the JSONB shape for risks.inherent_score /
// residual_score: {"likelihood": L, "impact": I, "rating": L*I}.
func riskScoreJSON(likelihood, impact int) string {
	return fmt.Sprintf(`{"likelihood":%d,"impact":%d,"rating":%d}`, likelihood, impact, likelihood*impact)
}

// capitalize returns slug's first character upper-cased. Used to
// derive a display name from a lower-case slug.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	c := s[0]
	if c >= 'a' && c <= 'z' {
		c -= 32
	}
	return string(c) + s[1:]
}
