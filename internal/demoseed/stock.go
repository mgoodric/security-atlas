package demoseed

// Stock bootstrap seed: the organisation data a fresh single-tenant install
// holds before anyone has used it (#1765). ApplyInPlace accepts a tenant only
// when every control and policy it holds is stock.

// stockControlBundleIDs is the bundle_id of every stock SOC 2 control bundle
// that deploy/docker/bootstrap/bootstrap.sh uploads from controls/soc2/.
// TestStockControlBundleIDs_MatchControlsDir fails when this list drifts from
// that directory.
var stockControlBundleIDs = []string{
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
	"soc2_pi1_1_processing_objectives",
	"soc2_pi1_2_input_completeness",
	"soc2_pi1_3_processing_validity",
	"soc2_pi1_4_output_delivery",
	"soc2_pi1_5_storage_integrity",
}

// stockPolicyCreatedBy is the created_by stamp internal/policy/seed writes on
// the stock policies (`atlas-cli policies seed-stock`).
const stockPolicyCreatedBy = "system:stock-seed"
