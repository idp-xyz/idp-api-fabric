// Package domain is the Developer Access model: DeveloperOrganization,
// ConsumerApplication, OAuthClientRef and AccessGrant (F03 §3.1; INV-10,
// INV-11). It is V1 scope.
//
// A Grant lets an application read restricted documentation for one Product,
// audience and environment while it is approved, unexpired and unrevoked. It
// is not a credential and not permission to call the product API: client
// secrets stay in the identity provider, and runtime access is decided by the
// product's own gateway.
package domain
