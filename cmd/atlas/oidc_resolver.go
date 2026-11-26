package main

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/idpconfig"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/netguard"
)

// oidcResolverFromEnv picks the OIDC sign-in resolver (#2034) and returns it
// with a label for the boot log.
//
// The client-secret key (idpconfig.SecretKeyEnv / SecretKeyFileEnv) is
// checked first, in every mode (#2609): a key that is set but malformed is a
// boot error even with ATLAS_OIDC_LOCAL_MODE=true, where sign-in does not use
// it, because PATCH /v1/admin/sso still seals secrets with it and a bad key
// found only when local mode is turned off is found too late. An unset key
// is not an error: every saved config then refuses sign-in.
func oidcResolverFromEnv(pool *pgxpool.Pool, issuerPolicy netguard.Policy) (oidc.IdpResolver, string, error) {
	crypter, err := idpconfig.SecretCrypterFromEnv()
	if err != nil && !errors.Is(err, idpconfig.ErrSecretKeyUnconfigured) {
		return nil, "", err
	}
	localMode, err := idpconfig.LocalModeFromEnv()
	if err != nil {
		return nil, "", err
	}
	if localMode {
		return idpconfig.LocalModeResolver{}, "local-mode (OIDC off)", nil
	}
	return idpconfig.NewResolver(pool, crypter, issuerPolicy),
		fmt.Sprintf("oidc_idp_configs (secret_key_configured=%t)", crypter != nil), nil
}
