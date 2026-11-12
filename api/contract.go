// Package api carries the contract of the Fabric management API into the
// build. openapi.yaml is the source: the transport is generated from it, and
// fabric-api validates every request against the very bytes embedded here,
// so what is served cannot drift from what is declared (F07 §4.1).
package api

import _ "embed"

//go:embed openapi.yaml
var contract []byte

// Contract returns the OpenAPI document of the management API, as this build
// carries it.
func Contract() []byte {
	return append([]byte(nil), contract...)
}
