// Package buildinfo exposes the build identity injected by the build pipeline.
package buildinfo

// The values are variables so that a release build can set them with
// -ldflags -X. A local build keeps the defaults and claims no commit.
var (
	version = "dev"
	commit  = "unknown"
	builtAt = "unknown"
)

// Info is the source and build identity of a running binary.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
}

// Current returns the identity embedded in this binary.
func Current() Info {
	return Info{Version: version, Commit: commit, BuiltAt: builtAt}
}
