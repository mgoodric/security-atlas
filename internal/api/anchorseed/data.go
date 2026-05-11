package anchorseed

// defaultFrameworks + defaultRequirements + defaultMappings model a small
// sample of cross-framework crosswalk data. Slice 008 replaces this whole
// substrate with DB-backed framework_requirements + framework_requirement_mappings
// tables. The mappings here are keyed on scf_id so the slice-005-to-slice-008
// transition doesn't touch the wire shape.

func defaultFrameworks() []FrameworkVersion {
	return []FrameworkVersion{
		{ID: "fw_soc2_2017", Framework: "SOC 2", Version: "2017 Trust Services Criteria"},
		{ID: "fw_iso27001_2022", Framework: "ISO 27001", Version: "2022"},
		{ID: "fw_nist_csf_2", Framework: "NIST CSF", Version: "2.0"},
	}
}

func defaultRequirements() []Requirement {
	return []Requirement{
		{ID: "req_soc2_cc6_1", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.1", Text: "The entity implements logical access security software, infrastructure, and architectures over protected information assets."},
		{ID: "req_soc2_cc6_2", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.2", Text: "Prior to issuing system credentials, the entity registers and authorizes new internal and external users."},
		{ID: "req_soc2_cc6_3", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.3", Text: "The entity authorizes, modifies, or removes access to data, software, functions, and other protected information assets."},
		{ID: "req_soc2_cc7_2", FrameworkVersionID: "fw_soc2_2017", Code: "CC7.2", Text: "The entity monitors system components and the operation of those components for anomalies."},
		{ID: "req_soc2_cc7_3", FrameworkVersionID: "fw_soc2_2017", Code: "CC7.3", Text: "The entity evaluates security events to determine whether they could or have resulted in a failure."},

		{ID: "req_iso_a5_15", FrameworkVersionID: "fw_iso27001_2022", Code: "A.5.15", Text: "Rules to control physical and logical access to information and other associated assets shall be established."},
		{ID: "req_iso_a5_17", FrameworkVersionID: "fw_iso27001_2022", Code: "A.5.17", Text: "Allocation and management of authentication information shall be controlled by a management process."},
		{ID: "req_iso_a8_2", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.2", Text: "Privileged access rights shall be allocated and managed in a restrictive manner."},
		{ID: "req_iso_a8_24", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.24", Text: "Rules for the effective use of cryptography, including key management, shall be defined and implemented."},
		{ID: "req_iso_a8_15", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.15", Text: "Logs that record activities, exceptions, faults and other relevant events shall be produced, stored, protected and analysed."},

		{ID: "req_nist_pr_aa_01", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.AA-01", Text: "Identities and credentials for authorized users, services, and hardware are managed by the organization."},
		{ID: "req_nist_pr_aa_03", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.AA-03", Text: "Users, services, and hardware are authenticated."},
		{ID: "req_nist_pr_aa_05", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.AA-05", Text: "Access permissions, entitlements, and authorizations are defined in a policy, managed, enforced, and reviewed."},
		{ID: "req_nist_pr_ds_01", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.DS-01", Text: "The confidentiality, integrity, and availability of data-at-rest are protected."},
		{ID: "req_nist_de_cm_01", FrameworkVersionID: "fw_nist_csf_2", Code: "DE.CM-01", Text: "Networks and network services are monitored to find potentially adverse events."},
	}
}

// defaultMappings keys each STRM edge on scf_id (e.g., "IAC-06") so the
// lookup works against slice 006's DB-backed anchors without renaming.
func defaultMappings() []Mapping {
	return []Mapping{
		{RequirementID: "req_soc2_cc6_1", AnchorSCFID: "IAC-06", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_iso_a5_17", AnchorSCFID: "IAC-06", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_nist_pr_aa_03", AnchorSCFID: "IAC-06", STRMType: "equal", Strength: 1.0},

		{RequirementID: "req_soc2_cc6_1", AnchorSCFID: "IAC-01", STRMType: "subset_of", Strength: 0.7},
		{RequirementID: "req_iso_a5_15", AnchorSCFID: "IAC-01", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_nist_pr_aa_01", AnchorSCFID: "IAC-01", STRMType: "equal", Strength: 0.9},

		{RequirementID: "req_soc2_cc6_2", AnchorSCFID: "IAC-07", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_soc2_cc6_3", AnchorSCFID: "IAC-07", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_iso_a8_2", AnchorSCFID: "IAC-07", STRMType: "subset_of", Strength: 0.7},

		{RequirementID: "req_nist_pr_aa_05", AnchorSCFID: "IAC-15", STRMType: "equal", Strength: 1.0},

		{RequirementID: "req_soc2_cc7_2", AnchorSCFID: "AAA-01", STRMType: "intersects", Strength: 0.9},
		{RequirementID: "req_soc2_cc7_3", AnchorSCFID: "AAA-01", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_iso_a8_15", AnchorSCFID: "AAA-01", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_nist_de_cm_01", AnchorSCFID: "AAA-01", STRMType: "subset_of", Strength: 0.6},

		{RequirementID: "req_iso_a8_15", AnchorSCFID: "AAA-10", STRMType: "intersects", Strength: 0.7},

		{RequirementID: "req_iso_a8_24", AnchorSCFID: "CRY-04", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_nist_pr_ds_01", AnchorSCFID: "CRY-04", STRMType: "equal", Strength: 1.0},

		{RequirementID: "req_iso_a8_24", AnchorSCFID: "CRY-01", STRMType: "equal", Strength: 1.0},
	}
}
