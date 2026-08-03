DROP TABLE IF EXISTS questionnaire_mapping_proposal_audit;
DROP TABLE IF EXISTS questionnaire_mapping_proposals;

ALTER TABLE questionnaire_questions
    DROP CONSTRAINT IF EXISTS questionnaire_questions_tenant_id_id_unique;
