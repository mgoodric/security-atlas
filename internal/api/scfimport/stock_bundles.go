// Stock bundles that ship today (#2599).

package scfimport

import "slices"

// StockControlBundleIDs is the bundle_id of every stock SOC 2 control
// bundle that deploy/docker/bootstrap/bootstrap.sh uploads from
// controls/soc2/. stockManifestHashes also names bundles that once shipped
// and no longer do; this list is the ones that ship now.
// TestStockControlBundleIDs_MatchControlsDir fails when it drifts from that
// directory.
var StockControlBundleIDs = []string{
	"network_segmentation_default_deny",
	"soc2_a1_1_capacity_monitoring",
	"soc2_a1_2_backup_processes",
	"soc2_a1_3_backup_testing",
	"soc2_c1_1_confidentiality_identification",
	"soc2_c1_2_confidential_disposal",
	"soc2_cc1_1_code_of_conduct",
	"soc2_cc1_2_board_oversight",
	"soc2_cc1_3_roles_responsibilities",
	"soc2_cc1_4_competence_training",
	"soc2_cc1_5_accountability_acknowledgment",
	"soc2_cc2_1_information_quality",
	"soc2_cc2_2_internal_communication",
	"soc2_cc2_3_external_communication",
	"soc2_cc3_1_risk_objectives",
	"soc2_cc3_2_risk_assessment",
	"soc2_cc3_3_fraud_risk",
	"soc2_cc3_4_change_risk_reassessment",
	"soc2_cc4_1_continuous_monitoring",
	"soc2_cc4_2_deficiency_communication",
	"soc2_cc5_1_control_selection",
	"soc2_cc5_2_general_tech_controls",
	"soc2_cc5_3_policies_procedures",
	"soc2_cc6_1_logical_access_baseline",
	"soc2_cc6_2_new_user_provisioning",
	"soc2_cc6_3_access_modification_revocation",
	"soc2_cc6_3_periodic_access_review",
	"soc2_cc6_3_privileged_account_management",
	"soc2_cc6_4_physical_access",
	"soc2_cc6_5_asset_disposal",
	"soc2_cc6_6_boundary_protection",
	"soc2_cc6_6_mfa_external_threats",
	"soc2_cc6_7_data_classification",
	"soc2_cc6_7_encryption_in_transit",
	"soc2_cc6_8_malware_protection",
	"soc2_cc7_1_vulnerability_detection",
	"soc2_cc7_1_vulnerability_remediation",
	"soc2_cc7_2_anomaly_detection",
	"soc2_cc7_3_security_event_evaluation",
	"soc2_cc7_4_incident_response",
	"soc2_cc7_5_incident_recovery",
	"soc2_cc8_1_change_management",
	"soc2_cc8_1_configuration_change_control",
	"soc2_cc9_1_business_continuity",
	"soc2_cc9_2_vendor_management",
	"soc2_cc9_2_vendor_risk_assessment",
	"soc2_p1_1_privacy_notice",
	"soc2_p2_1_consent_records",
	"soc2_p3_1_collection_minimization",
	"soc2_p3_2_explicit_consent",
	"soc2_p4_1_use_limitation",
	"soc2_p4_2_retention_schedule",
	"soc2_p5_1_data_subject_requests",
	"soc2_p6_1_third_party_disclosure",
	"soc2_p6_2_disclosure_log",
	"soc2_p6_3_privacy_breach_handling",
	"soc2_p6_4_vendor_privacy_terms",
	"soc2_p7_1_data_quality",
	"soc2_p8_1_privacy_monitoring",
	"soc2_pi1_1_processing_objectives",
	"soc2_pi1_2_input_completeness",
	"soc2_pi1_3_processing_validity",
	"soc2_pi1_4_output_delivery",
	"soc2_pi1_5_storage_integrity",
}

// IsShippedStockBundle reports whether bundleID is a stock control bundle
// the platform ships today.
func IsShippedStockBundle(bundleID string) bool {
	return slices.Contains(StockControlBundleIDs, bundleID)
}
