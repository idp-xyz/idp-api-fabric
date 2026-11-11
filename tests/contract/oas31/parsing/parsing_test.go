// What the parser makes of OpenAPI 3.1.2 documents and of their references
// (F07 §4.2 "引用安全", TC-01, EG-02). The parser is kin-openapi, the library
// the code generator and the request validator are built on.
//
// These tests read documents. They do not establish quotas on depth, size or
// time (EG-03), which need hostile inputs and a budget nobody has set yet.
package parsing_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// repository is the root of the repository, seen from this package.
const repository = "../../../.."

// load reads a document and refuses any reference that leaves its file.
func load(path string) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	return loader.LoadFromFile(path)
}

func TestTheContractsOfTheRepositoryAreValidDocuments(t *testing.T) {
	contracts := []string{
		repository + "/api/openapi.yaml",
		repository + "/contracts/examples/openapi/cargopai-contract-control.openapi.yaml",
	}
	samples, err := filepath.Glob("../samples/*.yaml")
	if err != nil || len(samples) == 0 {
		t.Fatalf("list the sample contracts: %v, %v", samples, err)
	}
	for _, path := range append(contracts, samples...) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			document, err := load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if err := document.Validate(context.Background()); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if document.OpenAPI != "3.1.2" {
				t.Errorf("the document declares OpenAPI %q, want 3.1.2", document.OpenAPI)
			}
		})
	}
}

// The negative fixtures of F04 §14, and how far the parser goes in refusing
// each. What it accepts has to be caught by a check of its own: the parser
// knows neither a policy on versions, nor a policy on security, nor a secret.
func TestWhatTheParserMakesOfTheNegativeFixtures(t *testing.T) {
	cases := []struct {
		fixture  string
		loading  string // a fragment of the error when loading, if any
		validity string // a fragment of the error when validating, if any
	}{
		{fixture: "F04_negative_external_ref", loading: "encountered disallowed external reference"},
		{fixture: "F04_negative_duplicate_yaml_key", loading: `mapping key "title" already defined`},
		{fixture: "F04_negative_duplicate_operation_id", validity: `operations "GET /v1/one" and "GET /v1/two" have the same operation id`},
		{fixture: "F04_negative_unsupported_version"},
		{fixture: "F04_negative_missing_security"},
		{fixture: "F04_negative_embedded_secret"},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			document, err := load(repository + "/tests/acceptance/fixtures/" + c.fixture + ".yaml")
			if c.loading != "" {
				if err == nil || !strings.Contains(err.Error(), c.loading) {
					t.Fatalf("loading yields %v, want an error with %q", err, c.loading)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}

			err = document.Validate(context.Background())
			switch {
			case c.validity == "" && err != nil:
				t.Fatalf("the parser now refuses the fixture: %v", err)
			case c.validity != "" && (err == nil || !strings.Contains(err.Error(), c.validity)):
				t.Fatalf("validating yields %v, want an error with %q", err, c.validity)
			}
		})
	}
}

// The parser reads a version it only partly supports without a word, so the
// supported versions have to be decided before a document is parsed further.
func TestTheParserDoesNotGateTheVersion(t *testing.T) {
	document, err := load(repository + "/tests/acceptance/fixtures/F04_negative_unsupported_version.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if document.OpenAPI != "3.2.1" {
		t.Errorf("the document declares OpenAPI %q, want 3.2.1", document.OpenAPI)
	}
}

// remote serves a schema and counts the requests it receives.
func remote(t *testing.T) (address string, requests *atomic.Int32) {
	t.Helper()

	requests = new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "type: string\n")
	}))
	t.Cleanup(server.Close)
	return server.URL, requests
}

// referring writes, into directory, a document whose only schema is a
// reference to target.
func referring(t *testing.T, directory, target string) string {
	t.Helper()

	document := `openapi: 3.1.2
info: {title: Referring document, version: 0.0.0}
paths: {}
components:
  schemas:
    Referred:
      $ref: '` + target + `'
`
	path := filepath.Join(directory, "main.yaml")
	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// By default the parser follows no reference out of a document, and asks the
// network for nothing.
func TestARemoteReferenceIsRefusedWithoutBeingFetched(t *testing.T) {
	address, requests := remote(t)

	_, err := load(referring(t, t.TempDir(), address+"/schema.yaml"))
	if err == nil || !strings.Contains(err.Error(), "encountered disallowed external reference") {
		t.Fatalf("loading yields %v, want the reference refused", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("the remote server received %d requests, want none", got)
	}
}

// A hazard: allowing external references is one switch for files and for the
// network. With the parser's own reader, a document then makes the process
// fetch whatever address it names.
func TestAllowingExternalReferencesAlsoAllowsTheNetwork(t *testing.T) {
	address, requests := remote(t)

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	if _, err := loader.LoadFromFile(referring(t, t.TempDir(), address+"/schema.yaml")); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := requests.Load(); got == 0 {
		t.Fatal("the remote server received no request: the parser no longer fetches by default, so this hazard is gone")
	}
}

// workspace reads the references of a loader from the files below root only,
// and keeps the digest of every file it read: the reference closure of
// F04 §2.1.
type workspace struct {
	root string
	read map[string]string // path below root → SHA-256 of its bytes
}

func newWorkspace(t *testing.T, root string) *workspace {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return &workspace{root: resolved, read: make(map[string]string)}
}

func (w *workspace) loader() *openapi3.Loader {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = w.readFromURI
	return loader
}

func (w *workspace) readFromURI(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
	if (location.Scheme != "" && location.Scheme != "file") || location.Host != "" {
		return nil, fmt.Errorf("reference %s is not a file of the workspace", location)
	}
	// Resolving links first makes a link that points out of the workspace
	// fail the containment check below.
	path, err := filepath.EvalSymlinks(filepath.FromSlash(location.Path))
	if err != nil {
		return nil, fmt.Errorf("reference %s: %w", location, err)
	}
	relative, err := filepath.Rel(w.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("reference %s leaves the workspace", location)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reference %s: %w", location, err)
	}
	digest := sha256.Sum256(content)
	w.read[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
	return content, nil
}

// closureDigest is a digest over the files read and their digests, in a
// stable order.
func (w *workspace) closureDigest() string {
	paths := make([]string, 0, len(w.read))
	for path := range w.read {
		paths = append(paths, path)
	}
	slices.Sort(paths)

	hash := sha256.New()
	for _, path := range paths {
		fmt.Fprintf(hash, "%s sha256:%s\n", path, w.read[path])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// With a reader of our own, references between the files of a workspace are
// followed, every file read is known, and the closure has a digest that
// changes when any of those files does.
func TestReferencesInsideAWorkspaceGiveAClosureWithADigest(t *testing.T) {
	root := t.TempDir()
	main := referring(t, root, "schemas/thing.yaml#/Thing")
	write(t, filepath.Join(root, "schemas", "thing.yaml"),
		"Thing:\n  type: object\n  properties:\n    owner:\n      $ref: 'owner.yaml#/Owner'\n")
	write(t, filepath.Join(root, "schemas", "owner.yaml"), "Owner:\n  type: string\n")

	digestOf := func() string {
		workspace := newWorkspace(t, root)
		document, err := workspace.loader().LoadFromFile(main)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := document.Validate(context.Background()); err != nil {
			t.Fatalf("validate: %v", err)
		}
		read := make([]string, 0, len(workspace.read))
		for path := range workspace.read {
			read = append(read, path)
		}
		slices.Sort(read)
		if want := []string{"main.yaml", "schemas/owner.yaml", "schemas/thing.yaml"}; !slices.Equal(read, want) {
			t.Fatalf("files read: %v, want %v", read, want)
		}
		return workspace.closureDigest()
	}

	first := digestOf()
	if again := digestOf(); again != first {
		t.Errorf("the closure digest of unchanged files went from %s to %s", first, again)
	}
	write(t, filepath.Join(root, "schemas", "owner.yaml"), "Owner:\n  type: integer\n")
	if changed := digestOf(); changed == first {
		t.Error("the closure digest did not change with a referenced file")
	}
}

// The same reader keeps a document from reaching a file outside its
// workspace, by a parent path or through a link, and from reaching the
// network.
func TestReferencesThatLeaveAWorkspaceAreRefused(t *testing.T) {
	address, requests := remote(t)

	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	write(t, filepath.Join(parent, "outside.yaml"), "Secret:\n  type: string\n")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "outside.yaml"), filepath.Join(root, "link.yaml")); err != nil {
		t.Skipf("links cannot be created here: %v", err)
	}

	for name, target := range map[string]string{
		"a parent path":           "../outside.yaml#/Secret",
		"a link out of the root":  "link.yaml#/Secret",
		"an address on a network": address + "/schema.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			workspace := newWorkspace(t, root)
			_, err := workspace.loader().LoadFromFile(referring(t, root, target))
			if err == nil {
				t.Fatalf("the reference %s was followed", target)
			}
			if !strings.Contains(err.Error(), "the workspace") {
				t.Errorf("the reference was refused for another reason: %v", err)
			}
			if _, read := workspace.read["main.yaml"]; len(workspace.read) != 1 || !read {
				t.Errorf("files read: %v, want main.yaml only", workspace.read)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("the remote server received %d requests, want none", got)
	}
}

// A schema that refers to itself loads and validates, and the parser comes
// back from it.
func TestACyclicSchemaLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cycle.yaml")
	write(t, path, `openapi: 3.1.2
info: {title: Cyclic schema, version: 0.0.0}
paths: {}
components:
  schemas:
    Node:
      type: object
      properties:
        children:
          type: array
          items:
            $ref: '#/components/schemas/Node'
`)
	document, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	node := document.Components.Schemas["Node"].Value
	if items := node.Properties["children"].Value.Items; items.Value != node {
		t.Error("the reference to Node does not resolve to Node itself")
	}
}
