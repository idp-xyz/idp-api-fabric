// Package domain is the API Catalog model: APIAsset, AssetOwnership and
// AssetLifecycleTransition (F03 §3.1; INV-03).
//
// An APIAsset has a stable identity inside exactly one Product and its own
// lifecycle (EXPERIMENTAL, STABLE, DEPRECATED, RETIRED). That lifecycle is not
// a Release or Publication state, and this context never rewrites either.
package domain
