// Package authctx threads the authenticated credential through
// context.Context so service handlers can reach it without re-parsing
// the bearer token.
package authctx

import (
	"context"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

type ctxKey struct{}

// WithCredential attaches an authenticated credential to ctx.
func WithCredential(ctx context.Context, cred credstore.Credential) context.Context {
	return context.WithValue(ctx, ctxKey{}, cred)
}

// CredentialFromContext returns the attached credential and true if present.
func CredentialFromContext(ctx context.Context) (credstore.Credential, bool) {
	v, ok := ctx.Value(ctxKey{}).(credstore.Credential)
	return v, ok
}
