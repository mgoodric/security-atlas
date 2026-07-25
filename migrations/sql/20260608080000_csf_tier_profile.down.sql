-- Down migration for 20260608080000_csf_tier_profile.sql (slice 515).
-- Drops the four assessment tables + the two CSF assessment enums for a
-- byte-clean up → down → up round-trip. Child tables first (FK order), then
-- the enums (no longer referenced once the tables are gone).

DROP TABLE IF EXISTS csf_assessment_audit;
DROP TABLE IF EXISTS csf_profile_selections;
DROP TABLE IF EXISTS csf_profiles;
DROP TABLE IF EXISTS csf_tier_ratings;

DROP TYPE IF EXISTS csf_profile_kind;
DROP TYPE IF EXISTS csf_tier;
