// Module oas31 holds the sample contracts that record what the pinned
// generator makes of OpenAPI 3.1.2, the code generated from them and the
// tests that state it (F07 §4.2, ENG-17).
//
// It is a module of its own so that the libraries these tests need, a request
// validator among them, do not enter the dependency graph of the product.
module go.idp.xyz/idp-api-fabric/tests/contract/oas31

go 1.26.0

toolchain go1.26.5

require (
	github.com/getkin/kin-openapi v0.152.0
	github.com/oapi-codegen/nethttp-middleware v1.3.1
	github.com/oapi-codegen/nullable v1.2.0
	github.com/oapi-codegen/runtime v1.8.0
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/go-openapi/jsonpointer v1.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/oasdiff/yaml v0.1.1 // indirect
	github.com/oasdiff/yaml3 v0.0.14 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	golang.org/x/text v0.40.0 // indirect
)
