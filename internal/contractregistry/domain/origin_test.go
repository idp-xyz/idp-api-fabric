package domain

import (
	"errors"
	"strings"
	"testing"
)

const (
	sha1Commit   = "2ff6cad0b6a1e8c4f0d3a5b79c1e2d4f6a8b0c1d"
	sha256Commit = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
)

func TestADeclaredOriginNamesARepositoryAndAFullCommit(t *testing.T) {
	longestRepository := "https://git.example/" + strings.Repeat("r", RepositoryMaxLength-len("https://git.example/"))
	longestRef := "refs/heads/" + strings.Repeat("订", RefMaxLength-len("refs/heads/"))

	accepted := map[string]GitOrigin{
		"a repository and a commit":           {Repository: "https://github.com/idp-xyz/idp-api-fabric", Commit: sha1Commit},
		"a SHA-256 commit and a tag":          {Repository: "https://git.sr.ht/~cargopai/contracts", Commit: sha256Commit, Ref: "refs/tags/v1.2.0"},
		"a port, a nested group and .git":     {Repository: "https://gitlab.example.com:8443/logistics/apis/shipping.git", Commit: sha1Commit, Ref: "refs/heads/main"},
		"a host by its address":               {Repository: "https://10.0.0.5/scm/shipping", Commit: sha1Commit},
		"a repository that starts with a dot": {Repository: "https://github.com/idp-xyz/.github", Commit: sha1Commit},
		"the highest port":                    {Repository: "https://git.example:65535/shipping", Commit: sha1Commit},
		"a branch in another script":          {Repository: "https://git.example/shipping", Commit: sha1Commit, Ref: "refs/heads/功能/订单接口"},
		"a reference of a pull request":       {Repository: "https://git.example/shipping", Commit: sha1Commit, Ref: "refs/pull/12/merge"},
		"the longest repository":              {Repository: longestRepository, Commit: sha1Commit},
		"the longest reference":               {Repository: "https://git.example/shipping", Commit: sha1Commit, Ref: longestRef},
	}
	for name, origin := range accepted {
		t.Run(name, func(t *testing.T) {
			if err := origin.Validate(); err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !origin.Declared() || origin.SourceKind() != SourceGit {
				t.Fatalf("declared %t as %s, want a declared GIT source", origin.Declared(), origin.SourceKind())
			}
		})
	}

	valid := GitOrigin{Repository: "https://git.example/shipping", Commit: sha1Commit, Ref: "refs/heads/main"}
	repository := func(value string) GitOrigin { origin := valid; origin.Repository = value; return origin }
	commit := func(value string) GitOrigin { origin := valid; origin.Commit = value; return origin }
	ref := func(value string) GitOrigin { origin := valid; origin.Ref = value; return origin }
	refused := map[string]GitOrigin{
		"no repository":                    repository(""),
		"plain http":                       repository("http://git.example/shipping"),
		"an SSH remote":                    repository("git@git.example:logistics/shipping.git"),
		"an SSH URL":                       repository("ssh://git@git.example/logistics/shipping.git"),
		"a user and a token":               repository("https://ci:glpat-secret@git.example/shipping"),
		"a token alone":                    repository("https://glpat-secret@git.example/shipping"),
		"a host alone":                     repository("https://git.example"),
		"a host and a slash":               repository("https://git.example/"),
		"a trailing slash":                 repository("https://git.example/shipping/"),
		"a query":                          repository("https://git.example/shipping?ref=main"),
		"a fragment":                       repository("https://git.example/shipping#readme"),
		"a host in upper case":             repository("https://Git.Example/shipping"),
		"a scheme in upper case":           repository("HTTPS://git.example/shipping"),
		"a parent segment":                 repository("https://git.example/logistics/../shipping"),
		"a current segment":                repository("https://git.example/logistics/./shipping"),
		"a segment of dots":                repository("https://git.example/logistics/..."),
		"an empty segment":                 repository("https://git.example//shipping"),
		"a percent escape":                 repository("https://git.example/ship%20ping"),
		"a space":                          repository("https://git.example/ship ping"),
		"a space in front":                 repository(" https://git.example/shipping"),
		"a line break behind":              repository("https://git.example/shipping\n"),
		"an IPv6 host":                     repository("https://[::1]/shipping"),
		"port zero":                        repository("https://git.example:0/shipping"),
		"a port with a leading zero":       repository("https://git.example:0443/shipping"),
		"a port above the highest":         repository("https://git.example:65536/shipping"),
		"a repository longer than allowed": repository(longestRepository + "r"),

		"no commit":                    commit(""),
		"an abbreviated commit":        commit(sha1Commit[:7]),
		"a commit one digit short":     commit(sha1Commit[:39]),
		"a commit one digit long":      commit(sha1Commit + "0"),
		"a commit between the two":     commit(sha256Commit[:48]),
		"a commit in upper case":       commit(strings.ToUpper(sha1Commit)),
		"a commit that is not hex":     commit(strings.Repeat("g", 40)),
		"a branch for a commit":        commit("main"),
		"HEAD for a commit":            commit("HEAD"),
		"a commit and a line break":    commit(sha1Commit + "\n"),
		"a commit with a space behind": commit(sha1Commit + " "),

		"a short branch name":             ref("main"),
		"a reference without refs":        ref("heads/main"),
		"refs alone":                      ref("refs/"),
		"refs in upper case":              ref("Refs/heads/main"),
		"a reference with a space":        ref("refs/heads/my branch"),
		"a reference with a tab":          ref("refs/heads/my\tbranch"),
		"a reference with a line break":   ref("refs/heads/main\n"),
		"a reference with a no-break gap": ref("refs/heads/my\u00a0branch"),
		"a reference that is not UTF-8":   ref("refs/heads/\xff"),
		"a reference longer than allowed": ref(longestRef + "订"),
	}
	for name, origin := range refused {
		t.Run(name, func(t *testing.T) {
			err := origin.Validate()
			if !errors.Is(err, ErrInvalidOrigin) {
				t.Fatalf("got %v, want %v", err, ErrInvalidOrigin)
			}
			for _, said := range []string{origin.Repository, origin.Commit, origin.Ref} {
				if said != "" && strings.Contains(err.Error(), said) {
					t.Fatalf("the error repeats what was refused: %v", err)
				}
			}
		})
	}
}

func TestASourceWithoutAnOriginIsAnUpload(t *testing.T) {
	if none := (GitOrigin{}); none.Declared() || none.SourceKind() != SourceUpload {
		t.Fatalf("no origin is declared %t as %s, want an undeclared UPLOAD", none.Declared(), none.SourceKind())
	}
	// Half an origin is one that was declared, and it is refused as such
	// rather than taken for an upload.
	for name, half := range map[string]GitOrigin{
		"a repository alone": {Repository: "https://git.example/shipping"},
		"a commit alone":     {Commit: sha1Commit},
		"a reference alone":  {Ref: "refs/heads/main"},
	} {
		if !half.Declared() || half.SourceKind() != SourceGit || !errors.Is(half.Validate(), ErrInvalidOrigin) {
			t.Errorf("%s: declared %t as %s, validated %v; want a declared GIT origin that is refused",
				name, half.Declared(), half.SourceKind(), half.Validate())
		}
	}
}
