package anchorseed

// defaultAnchors is the curated SCF subset slice 005 ships with. Real SCF
// importer (slice 006) replaces this with the full catalog.
func defaultAnchors() []Anchor {
	return []Anchor{
		{ID: "anch_iac01", SCFID: "IAC-01", Family: "IAC", Name: "Identification & Authentication Policy", Description: "Mechanisms exist to facilitate the implementation of identification and access management controls."},
		{ID: "anch_iac06", SCFID: "IAC-06", Family: "IAC", Name: "Multi-Factor Authentication (MFA)", Description: "Mechanisms exist to require Multi-Factor Authentication for privileged accounts, network access, and remote access."},
		{ID: "anch_iac07", SCFID: "IAC-07", Family: "IAC", Name: "User Provisioning & Lifecycle", Description: "Mechanisms exist to establish, activate, modify, disable, and remove logical access for users."},
		{ID: "anch_iac15", SCFID: "IAC-15", Family: "IAC", Name: "Account Review", Description: "Mechanisms exist to periodically review and validate user accounts and access privileges."},
		{ID: "anch_aaa01", SCFID: "AAA-01", Family: "AAA", Name: "Auditing & Accountability", Description: "Mechanisms exist to facilitate the implementation of operational auditing and accountability."},
		{ID: "anch_aaa10", SCFID: "AAA-10", Family: "AAA", Name: "Event Log Retention", Description: "Mechanisms exist to retain event logs for a defined period consistent with risk and legal requirements."},
		{ID: "anch_ast01", SCFID: "AST-01", Family: "AST", Name: "Asset Management Policy", Description: "Mechanisms exist to facilitate the implementation of asset management."},
		{ID: "anch_ast03", SCFID: "AST-03", Family: "AST", Name: "Asset Inventory", Description: "Mechanisms exist to maintain an authoritative inventory of technology assets."},
		{ID: "anch_cfg02", SCFID: "CFG-02", Family: "CFG", Name: "Secure Baseline Configurations", Description: "Mechanisms exist to develop, document, and maintain secure baseline configurations for technology platforms."},
		{ID: "anch_chg02", SCFID: "CHG-02", Family: "CHG", Name: "Change Control Process", Description: "Mechanisms exist to govern the technical configuration change control processes."},
		{ID: "anch_cry01", SCFID: "CRY-01", Family: "CRY", Name: "Use of Cryptographic Controls", Description: "Mechanisms exist to facilitate the use of cryptographic protections of data."},
		{ID: "anch_cry04", SCFID: "CRY-04", Family: "CRY", Name: "Encryption At Rest", Description: "Cryptographic mechanisms exist to protect the confidentiality of sensitive data at rest."},
		{ID: "anch_vpm04", SCFID: "VPM-04", Family: "VPM", Name: "Vulnerability Remediation Process", Description: "Mechanisms exist to identify, assess, prioritize, and remediate vulnerabilities."},
		{ID: "anch_irp04", SCFID: "IRO-04", Family: "IRO", Name: "Incident Response Plan", Description: "Mechanisms exist to maintain and make available a current and viable Incident Response Plan."},
		{ID: "anch_bcd02", SCFID: "BCD-02", Family: "BCD", Name: "Business Continuity Plan", Description: "Mechanisms exist to document and assign roles and responsibilities for the continuity of operations."},
	}
}

func defaultFrameworks() []FrameworkVersion {
	return []FrameworkVersion{
		{ID: "fw_soc2_2017", Framework: "SOC 2", Version: "2017 Trust Services Criteria"},
		{ID: "fw_iso27001_2022", Framework: "ISO 27001", Version: "2022"},
		{ID: "fw_nist_csf_2", Framework: "NIST CSF", Version: "2.0"},
	}
}

func defaultRequirements() []Requirement {
	return []Requirement{
		// SOC 2 Trust Services Criteria
		{ID: "req_soc2_cc6_1", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.1", Text: "The entity implements logical access security software, infrastructure, and architectures over protected information assets."},
		{ID: "req_soc2_cc6_2", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.2", Text: "Prior to issuing system credentials, the entity registers and authorizes new internal and external users."},
		{ID: "req_soc2_cc6_3", FrameworkVersionID: "fw_soc2_2017", Code: "CC6.3", Text: "The entity authorizes, modifies, or removes access to data, software, functions, and other protected information assets."},
		{ID: "req_soc2_cc7_2", FrameworkVersionID: "fw_soc2_2017", Code: "CC7.2", Text: "The entity monitors system components and the operation of those components for anomalies."},
		{ID: "req_soc2_cc7_3", FrameworkVersionID: "fw_soc2_2017", Code: "CC7.3", Text: "The entity evaluates security events to determine whether they could or have resulted in a failure."},

		// ISO 27001:2022
		{ID: "req_iso_a5_15", FrameworkVersionID: "fw_iso27001_2022", Code: "A.5.15", Text: "Rules to control physical and logical access to information and other associated assets shall be established."},
		{ID: "req_iso_a5_17", FrameworkVersionID: "fw_iso27001_2022", Code: "A.5.17", Text: "Allocation and management of authentication information shall be controlled by a management process."},
		{ID: "req_iso_a8_2", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.2", Text: "Privileged access rights shall be allocated and managed in a restrictive manner."},
		{ID: "req_iso_a8_24", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.24", Text: "Rules for the effective use of cryptography, including key management, shall be defined and implemented."},
		{ID: "req_iso_a8_15", FrameworkVersionID: "fw_iso27001_2022", Code: "A.8.15", Text: "Logs that record activities, exceptions, faults and other relevant events shall be produced, stored, protected and analysed."},

		// NIST CSF v2.0
		{ID: "req_nist_pr_aa_01", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.AA-01", Text: "Identities and credentials for authorized users, services, and hardware are managed by the organization."},
		{ID: "req_nist_pr_aa_03", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.AA-03", Text: "Users, services, and hardware are authenticated."},
		{ID: "req_nist_pr_aa_05", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.AA-05", Text: "Access permissions, entitlements, and authorizations are defined in a policy, managed, enforced, and reviewed."},
		{ID: "req_nist_pr_ds_01", FrameworkVersionID: "fw_nist_csf_2", Code: "PR.DS-01", Text: "The confidentiality, integrity, and availability of data-at-rest are protected."},
		{ID: "req_nist_de_cm_01", FrameworkVersionID: "fw_nist_csf_2", Code: "DE.CM-01", Text: "Networks and network services are monitored to find potentially adverse events."},
	}
}

// defaultMappings hand-curates STRM edges so the SCF browser has substantive
// data to display. Real mappings (NIST IR 8477) land with slice 008.
func defaultMappings() []Mapping {
	return []Mapping{
		// MFA anchor (IAC-06)
		{RequirementID: "req_soc2_cc6_1", AnchorID: "anch_iac06", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_iso_a5_17", AnchorID: "anch_iac06", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_nist_pr_aa_03", AnchorID: "anch_iac06", STRMType: "equal", Strength: 1.0},

		// Identification & Auth Policy (IAC-01)
		{RequirementID: "req_soc2_cc6_1", AnchorID: "anch_iac01", STRMType: "subset_of", Strength: 0.7},
		{RequirementID: "req_iso_a5_15", AnchorID: "anch_iac01", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_nist_pr_aa_01", AnchorID: "anch_iac01", STRMType: "equal", Strength: 0.9},

		// User Provisioning (IAC-07)
		{RequirementID: "req_soc2_cc6_2", AnchorID: "anch_iac07", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_soc2_cc6_3", AnchorID: "anch_iac07", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_iso_a8_2", AnchorID: "anch_iac07", STRMType: "subset_of", Strength: 0.7},

		// Account Review (IAC-15)
		{RequirementID: "req_nist_pr_aa_05", AnchorID: "anch_iac15", STRMType: "equal", Strength: 1.0},

		// Auditing & Accountability (AAA-01)
		{RequirementID: "req_soc2_cc7_2", AnchorID: "anch_aaa01", STRMType: "intersects", Strength: 0.9},
		{RequirementID: "req_soc2_cc7_3", AnchorID: "anch_aaa01", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_iso_a8_15", AnchorID: "anch_aaa01", STRMType: "equal", Strength: 1.0},
		{RequirementID: "req_nist_de_cm_01", AnchorID: "anch_aaa01", STRMType: "subset_of", Strength: 0.6},

		// Event Log Retention (AAA-10)
		{RequirementID: "req_iso_a8_15", AnchorID: "anch_aaa10", STRMType: "intersects", Strength: 0.7},

		// Encryption At Rest (CRY-04)
		{RequirementID: "req_iso_a8_24", AnchorID: "anch_cry04", STRMType: "intersects", Strength: 0.8},
		{RequirementID: "req_nist_pr_ds_01", AnchorID: "anch_cry04", STRMType: "equal", Strength: 1.0},

		// Use of Cryptographic Controls (CRY-01)
		{RequirementID: "req_iso_a8_24", AnchorID: "anch_cry01", STRMType: "equal", Strength: 1.0},
	}
}
