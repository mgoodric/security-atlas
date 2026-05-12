-- name: UpsertLocalCredential :exec
-- Set or overwrite the argon2id hash for a user. Idempotent so password-change
-- and initial-provisioning go through the same path.
INSERT INTO local_credentials (user_id, tenant_id, password_hash, algo, params)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET
    password_hash = EXCLUDED.password_hash,
    algo = EXCLUDED.algo,
    params = EXCLUDED.params,
    updated_at = now();

-- name: GetLocalCredentialByUserID :one
SELECT *
FROM local_credentials
WHERE tenant_id = $1 AND user_id = $2;
