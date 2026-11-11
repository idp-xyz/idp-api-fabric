// Package architecture holds the dependency gates of the Fabric modular
// monolith (F07 §3.1, ENG-16). The rules live in tests so that a boundary
// violation fails the build rather than a review.
package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "go.idp.xyz/idp-api-fabric"

// contexts are the seven bounded contexts of F03 §3, under the package names
// F07 §3 gives them. Adding or renaming one is a design change: it starts in
// the design documents, not here.
var contexts = []string{
	"productgovernance", "apicatalog", "contractregistry", "policyreview",
	"publication", "developeraccess", "platformoperations",
}

// sameContextImports lists, for each layer of a context, the layers of the
// same context it may import: adapters → application / ports → domain
// (F07 §3.1).
var sameContextImports = map[string][]string{
	"domain":      {"domain"},
	"ports":       {"domain", "ports"},
	"application": {"domain", "ports", "application"},
	"adapters":    {"domain", "ports", "application", "adapters"},
}

// freeRoots are the top-level directories F07 §3 names besides internal/.
// Their packages follow no layering rule of their own, and nothing in a
// context may import them.
var freeRoots = []string{"cmd", "tests", "tools", "api", "migrations", "deployments"}

// sharedRoots are the directories allowed under internal/ besides the
// contexts.
var sharedRoots = []string{"transport", "platform", "architecture"}

// infrastructurePackages are the standard library packages that speak HTTP or
// SQL. Inside a context only adapters may use them (F07 §3.1).
var infrastructurePackages = []string{"net/http", "database/sql"}

// persistenceDrivers must not be reached from the inbound transport: a handler
// goes through a use case, never to the database.
var persistenceDrivers = []string{"database/sql", "github.com/jackc/pgx"}

// frameworkTestkit must never enter a production package (idp-bento-go
// consumer contract). Only test files may import it.
const frameworkTestkit = "go.idp.xyz/idp-bento-go/testkit"

// testDatabases creates and drops databases for integration tests.
const testDatabases = modulePath + "/internal/platform/db/pgtest"

// testSupport are the packages that exist for tests only. No production
// package imports them.
var testSupport = []string{frameworkTestkit, testDatabases}

// place is where a package of this module sits in the layout of F07 §3. The
// zero place is a package the layout does not know.
type place struct {
	root    string // a free root, a shared root, or "context"
	context string
	layer   string
}

// placeOf locates pkg. The second result is false for a package of another
// module or of the standard library.
func placeOf(pkg string) (place, bool) {
	relative, inside := strings.CutPrefix(pkg, modulePath+"/")
	if !inside {
		return place{}, pkg == modulePath
	}
	segments := strings.Split(relative, "/")
	if slices.Contains(freeRoots, segments[0]) {
		return place{root: segments[0]}, true
	}
	if segments[0] != "internal" || len(segments) < 2 {
		return place{}, true
	}
	switch name := segments[1]; {
	case slices.Contains(contexts, name):
		where := place{root: "context", context: name}
		if len(segments) > 2 {
			where.layer = segments[2]
		}
		return where, true
	case slices.Contains(sharedRoots, name):
		return place{root: name}, true
	}
	return place{}, true
}

// violation says why a production file of package pkg may not import imported,
// or returns "" when it may.
func violation(pkg, imported string) string {
	if hasAnyPathPrefix(imported, testSupport) && !hasAnyPathPrefix(pkg, testSupport) {
		return "production code imports test support; only test files may"
	}

	from, _ := placeOf(pkg)
	to, sameModule := placeOf(imported)

	switch from.root {
	case "context":
		return contextViolation(from, to, sameModule, imported)
	case "transport":
		if to.root == "context" && to.layer == "adapters" {
			return "the inbound transport imports an adapter; a handler goes through a use case"
		}
		if hasAnyPathPrefix(imported, persistenceDrivers) {
			return "the inbound transport reaches persistence; a handler goes through a use case"
		}
	case "platform":
		if to.root == "context" || to.root == "transport" {
			return "platform is context-free infrastructure; it imports neither a context nor the transport"
		}
	}
	return ""
}

// contextViolation applies the rules for a package inside a bounded context.
func contextViolation(from, to place, sameModule bool, imported string) string {
	allowed, isLayer := sameContextImports[from.layer]
	if !isLayer {
		return "" // An unknown layer is reported once per file by the repository scan.
	}
	inner := from.layer != "adapters"

	if !sameModule {
		switch {
		case !isStandardLibrary(imported):
			if inner {
				return from.layer + " imports a third-party module; inside a context only adapters may"
			}
		case inner && hasAnyPathPrefix(imported, infrastructurePackages):
			return from.layer + " speaks HTTP or SQL; inside a context only adapters may"
		}
		return ""
	}

	switch to.root {
	case "context":
		if to.context != from.context {
			return "context " + from.context + " imports context " + to.context +
				"; contexts collaborate through ports wired in cmd"
		}
		if !slices.Contains(allowed, to.layer) {
			return from.layer + " imports " + to.layer + " of its own context; dependencies point inward"
		}
		return ""
	case "platform":
		if inner {
			return from.layer + " imports platform; inside a context only adapters may"
		}
		return ""
	case "transport":
		return "a context imports the transport; DTOs stay in the transport"
	}
	return "a context imports " + imported + "; nothing in a context depends on it"
}

// isStandardLibrary reports whether path belongs to the standard library: its
// first element has no dot, unlike any module path.
func isStandardLibrary(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func hasAnyPathPrefix(path string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(prefix string) bool { return hasPathPrefix(path, prefix) })
}

func TestRulesRefuseForbiddenImports(t *testing.T) {
	const internal = modulePath + "/internal/"

	cases := []struct {
		name     string
		pkg      string
		imported string
		reason   string // a fragment of the expected reason
	}{
		{"domain reaches its adapters", internal + "publication/domain", internal + "publication/adapters/postgres", "dependencies point inward"},
		{"domain reaches its use cases", internal + "publication/domain", internal + "publication/application", "dependencies point inward"},
		{"ports reach the use cases", internal + "publication/ports", internal + "publication/application", "dependencies point inward"},
		{"use cases reach the adapters", internal + "publication/application", internal + "publication/adapters", "dependencies point inward"},
		{"domain speaks HTTP", internal + "publication/domain", "net/http", "HTTP or SQL"},
		{"use cases speak SQL", internal + "apicatalog/application", "database/sql/driver", "HTTP or SQL"},
		{"domain imports a driver", internal + "publication/domain", "github.com/jackc/pgx/v5", "third-party"},
		{"use cases import the framework", internal + "publication/application", "go.idp.xyz/idp-bento-go/eventing", "third-party"},
		{"ports import the platform", internal + "publication/ports", internal + "platform/db", "only adapters may"},
		{"use cases import another context", internal + "publication/application", internal + "contractregistry/domain", "imports context contractregistry"},
		{"adapters import another context's ports", internal + "publication/adapters/postgres", internal + "contractregistry/ports", "imports context contractregistry"},
		{"adapters import the transport", internal + "publication/adapters/postgres", internal + "transport/http", "DTOs stay in the transport"},
		{"a context imports a command", internal + "publication/adapters/postgres", modulePath + "/cmd/fabric-api", "nothing in a context depends on it"},
		{"a context imports an unknown root", internal + "publication/adapters/postgres", internal + "common", "nothing in a context depends on it"},
		{"the transport imports an adapter", internal + "transport/http", internal + "publication/adapters/postgres", "goes through a use case"},
		{"the transport imports the driver", internal + "transport/http", "github.com/jackc/pgx/v5/pgxpool", "goes through a use case"},
		{"the platform imports a context", internal + "platform/db", internal + "publication/domain", "context-free"},
		{"the platform imports the transport", internal + "platform/telemetry", internal + "transport/http", "context-free"},
		{"a command imports the testkit", modulePath + "/cmd/fabric-worker", frameworkTestkit, "test support"},
		{"an adapter imports the testkit", internal + "publication/adapters/postgres", frameworkTestkit, "test support"},
		{"a command imports the test databases", modulePath + "/cmd/fabric-api", testDatabases, "test support"},
		{"the platform imports the test databases", internal + "platform/db", testDatabases, "test support"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := violation(c.pkg, c.imported)
			if got == "" || !strings.Contains(got, c.reason) {
				t.Errorf("%s importing %s: got %q, want a reason containing %q", c.pkg, c.imported, got, c.reason)
			}
		})
	}
}

func TestRulesAllowIntendedImports(t *testing.T) {
	const internal = modulePath + "/internal/"

	cases := []struct {
		pkg      string
		imported string
	}{
		{internal + "publication/domain", "time"},
		{internal + "publication/domain", internal + "publication/domain/slot"},
		{internal + "publication/ports", internal + "publication/domain"},
		{internal + "publication/ports", "context"},
		{internal + "publication/application", internal + "publication/ports"},
		{internal + "publication/application", internal + "publication/domain"},
		{internal + "publication/adapters/postgres", internal + "publication/application"},
		{internal + "publication/adapters/postgres", internal + "publication/ports"},
		{internal + "publication/adapters/postgres", internal + "platform/db"},
		{internal + "publication/adapters/postgres", "github.com/jackc/pgx/v5"},
		{internal + "publication/adapters/postgres", "go.idp.xyz/idp-bento-go/postgres/outbox"},
		{internal + "publication/adapters/http", "net/http"},
		{internal + "transport/http", "net/http"},
		{internal + "transport/http", internal + "publication/application"},
		{internal + "transport/http", internal + "platform/buildinfo"},
		{internal + "platform/db", "github.com/jackc/pgx/v5/pgxpool"},
		{internal + "platform/db", "go.idp.xyz/idp-bento-go/postgres"},
		{modulePath + "/cmd/fabric-api", internal + "publication/adapters/postgres"},
		{modulePath + "/cmd/fabric-api", internal + "transport/http"},
	}
	for _, c := range cases {
		if got := violation(c.pkg, c.imported); got != "" {
			t.Errorf("%s importing %s must be allowed, got %q", c.pkg, c.imported, got)
		}
	}
}

func TestRepositoryKeepsTheBoundaries(t *testing.T) {
	packages := make(map[string]bool)

	for _, file := range loadSources(t) {
		packages[file.pkg] = true

		where, _ := placeOf(file.pkg)
		switch {
		case where == place{}:
			t.Errorf("%s: the package is outside the layout of F07 §3; move it or register its root here", file.path)
		case where.root == "context" && sameContextImports[where.layer] == nil:
			t.Errorf("%s: %q is not a layer of a context (domain, ports, application, adapters)", file.path, where.layer)
		}

		for _, imported := range file.imports {
			if reason := violation(file.pkg, imported); reason != "" {
				t.Errorf("%s imports %q: %s", file.path, imported, reason)
			}
		}
	}

	for _, context := range contexts {
		for layer := range sameContextImports {
			pkg := modulePath + "/internal/" + context + "/" + layer
			if !packages[pkg] {
				t.Errorf("%s is missing: every context of F03 §3 keeps the four layers of F07 §3", pkg)
			}
		}
	}
}

type sourceFile struct {
	pkg     string
	path    string
	imports []string
}

// loadSources parses the non-test Go files of the repository and returns their
// package path and imports. Test files are left out so that a test-only
// dependency cannot fail a production boundary rule.
func loadSources(t *testing.T) []sourceFile {
	t.Helper()

	root := repositoryRoot(t)
	var files []sourceFile

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}

		file := sourceFile{
			pkg:  modulePath + "/" + filepath.ToSlash(relative),
			path: filepath.ToSlash(relative) + "/" + filepath.Base(path),
		}
		for _, spec := range parsed.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			file.imports = append(file.imports, imported)
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go sources found; the boundary gates would pass vacuously")
	}
	return files
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("go.mod not found above the test working directory")
		}
		directory = parent
	}
}
