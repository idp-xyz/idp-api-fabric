// Package identity verifies the bearer tokens of the identity provider a
// deployment trusts (F05 §4.2, F07 §8.1). Fabric verifies tokens: it never
// issues them and never stores a client secret.
//
// A token is accepted only if it is an RS256 JWT signed with a key the issuer
// publishes at the configured JWKS URL, names the configured issuer, is
// addressed to the configured audience and is inside its validity window.
//
// The package answers who the caller is and nothing else. What a caller may
// do is decided from state the server holds, never from a claim of the token
// (F05 §4.2), so no role, scope or tenant is read here.
//
// It uses the standard library only.
package identity
