package domain

type Compatibility string

const (
	Compatible Compatibility = "COMPATIBLE"
	Risky      Compatibility = "RISKY"
	Unknown    Compatibility = "UNKNOWN"
	Breaking   Compatibility = "BREAKING"
)

type CompatibilityChange struct {
	Classification Compatibility
	Code           string
	Pointer        string
}

const MaxCompatibilityChanges = 100

type Comparison struct {
	Classification Compatibility
	Changes        []CompatibilityChange
	Truncated      bool
}
