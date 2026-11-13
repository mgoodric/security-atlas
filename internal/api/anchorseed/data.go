package anchorseed

// defaultFrameworks + defaultRequirements + defaultMappings model a small
// sample of cross-framework crosswalk data. Slice 008 replaces this whole
// substrate with DB-backed framework_requirements + framework_requirement_mappings
// tables. The mappings here are keyed on scf_id so the slice-005-to-slice-008
// transition doesn't touch the wire shape.
//
// Requirement Text is an original one-line summary for SOC 2 and ISO 27001,
// whose source text is copyrighted (#1896; posture as in the crosswalk
// headers under data/crosswalks/). NIST CSF text is public domain and quoted.

func defaultFrameworks() []FrameworkVersion {
	return []FrameworkVersion{
		{ID: "fw_soc2_2017", Framework: "SOC 2", Version: "2017 Trust Services Criteria"},
		{ID: "fw_iso27001_2022", Framework: "ISO 27001", Version: "2022"},
		{ID: "fw_nist_csf_2", Framework: "NIST CSF", Version: "2.0"},
	}
}

func defaultRequirements() []Requirement {
	return []Requirement{
		{ID: "req_soc2_cc6_1", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.1", Text: "Access to protected systems and data goes through managed logical access controls."},
		{ID: "req_soc2_cc6_2", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.2", Text: "No one gets credentials until they have been registered and approved."},
		{ID: "req_soc2_cc6_3", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.3", Text: "Access is granted, changed and taken away on approval and on time."},
		{ID: "req_soc2_cc7_2", FrameworkVersionID: "fw_soc2_2017", Code: "CC7.2", Text: "Systems are watched for abnormal behaviour."},
		{ID: "req_soc2_cc7_3", FrameworkVersionID: "fw_soc2_2017", Code: "CC7.3", Text: "Security events are triaged to decide whether they amount to an incident."},

		{ID: "req_iso_a5_15", FrameworkVersionID: "fw_iso27001_2022", Code: "A.5.15", Text: "Physical and logical access to information is governed by written rules."},
		{ID: "req_iso_a5_17", FrameworkVersionID: "fw_iso27001_2022", Code: "A.5.17", Text: "Passwords, keys and other authenticators are issued and handled under a managed process."},
		{ID: "req_iso_a8_2", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.2", Text: "Admin-level access is kept to the few who need it and tracked."},
		{ID: "req_iso_a8_24", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.24", Text: "Encryption use and key handling follow defined rules."},
		{ID: "req_iso_a8_15", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.15", Text: "Activity and error logs are kept, protected and reviewed."},

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
