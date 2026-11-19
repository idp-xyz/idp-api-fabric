package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"testing"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

const (
	apiAssetID       = "4b5c6d7e-8f90-4a1b-9c2d-3e4f5a6b7c8d"
	revisionID       = "6d7e8f90-a1b2-4c3d-8e4f-5a6b7c8d9e0f"
	sourceSnapshotID = "8f90a1b2-c3d4-4e5f-a6b7-c8d9e0f1a2b3"
	// revisions is where the revisions of the asset are imported and read.
	revisions = "/v1/products/" + productID + "/assets/" + apiAssetID + "/revisions"

	contractsRepository = "https://git.example/logistics/contracts"
	contractsCommit     = "2ff6cad0b6a1e8c4f0d3a5b79c1e2d4f6a8b0c1d"
	contractSource      = "openapi: 3.1.2\ninfo: {title: Shipments, version: v1}\n"
)

// contractRegistry stands in for the use cases of Contract Registry. It
// keeps what it was asked and answers what the test set. The real use cases
// are tested in cmd/fabric-api, with the store behind them.
type contractRegistry struct {
	revision registrydomain.Revision
	files    []registrydomain.RecordedFile
	created  bool
	err      error

	imported    []registry.ImportRevision
	read        []string
	sourcesRead []sourceRead
}

// sourceRead is one request for the files of a revision.
type sourceRead struct {
	Query    registryports.ScopeQuery
	Revision string
}

func (r *contractRegistry) GetRevisionSource(_ context.Context, query registryports.ScopeQuery, id string) (registry.RevisionSource, error) {
	r.sourcesRead = append(r.sourcesRead, sourceRead{Query: query, Revision: id})
	return registry.RevisionSource{Revision: r.revision, Files: r.files}, r.err
}

func (r *contractRegistry) ImportRevision(_ context.Context, command registry.ImportRevision) (registryports.Registration, error) {
	r.imported = append(r.imported, command)
	return registryports.Registration{Revision: r.revision, Created: r.created}, r.err
}

func (r *contractRegistry) GetRevision(_ context.Context, _ registryports.ScopeQuery, id string) (registrydomain.Revision, error) {
	r.read = append(r.read, id)
	return r.revision, r.err
}

func (r *contractRegistry) ListRevisions(context.Context, registry.ListRevisions) (registry.Page, error) {
	return registry.Page{Items: []registrydomain.Revision{r.revision}}, r.err
}

// fromGit is the revision of the asset as alice imported it from a commit
// on main.
func fromGit() registrydomain.Revision {
	return registrydomain.Revision{
		ID: revisionID, SourceSnapshotID: sourceSnapshotID,
		Asset:    registrydomain.AssetRef{ProductID: productID, WorkspaceID: workspaceID, AssetID: apiAssetID},
		Sequence: 2, Status: registrydomain.RevisionDraft, SourceKind: registrydomain.SourceGit,
		Origin:     registrydomain.GitOrigin{Repository: contractsRepository, Commit: contractsCommit, Ref: "refs/heads/main"},
		Entrypoint: "api/openapi.yaml", SpecVersion: "3.1.2",
		SourceDigest:     "sha256:" + fmt.Sprintf("%064x", 1),
		RefClosureDigest: "sha256:" + fmt.Sprintf("%064x", 2),
		CreatedAt:        createdAt,
	}
}

func newRegistryService(t *testing.T, fake ContractRegistry) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: unusedCatalog{}, Registry: fake, PolicyReview: unusedPolicyReview{},
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

func sourceUpload(origin any) string {
	body := map[string]any{
		"entrypoint": "api/openapi.yaml",
		"files": []map[string]string{
			{"path": "api/openapi.yaml", "content_base64": base64.StdEncoding.EncodeToString([]byte(contractSource))},
		},
	}
	if origin != nil {
		body["git_origin"] = origin
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// What a caller declares of the origin of its files reaches the use case as
// it was declared, and nothing is made up where it declared nothing.
func TestAnImportHandsTheUseCaseTheOriginAsItWasDeclared(t *testing.T) {
	cases := map[string]struct {
		declared any
		want     registrydomain.GitOrigin
	}{
		"a repository, a commit and a reference": {
			map[string]string{"repository": contractsRepository, "commit": contractsCommit, "ref": "refs/tags/v1.2.0"},
			registrydomain.GitOrigin{Repository: contractsRepository, Commit: contractsCommit, Ref: "refs/tags/v1.2.0"},
		},
		"a repository and a commit": {
			map[string]string{"repository": contractsRepository, "commit": contractsCommit},
			registrydomain.GitOrigin{Repository: contractsRepository, Commit: contractsCommit},
		},
		"no origin": {nil, registrydomain.GitOrigin{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &contractRegistry{revision: fromGit(), created: true}
			s := newRegistryService(t, fake)

			recorder := s.do(t, call{Method: http.MethodPost, Target: revisions, Token: alice.Subject, Body: sourceUpload(c.declared)})
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status %d, want 201: %s", recorder.Code, recorder.Body)
			}
			if len(fake.imported) != 1 {
				t.Fatalf("the use case was asked %d times, want once", len(fake.imported))
			}
			command := fake.imported[0]
			if command.Origin != c.want {
				t.Errorf("the use case was handed the origin %+v, want %+v", command.Origin, c.want)
			}
			wantFiles := []registrydomain.SourceFile{{Path: "api/openapi.yaml", Content: contractSource}}
			if command.Entrypoint != "api/openapi.yaml" || !reflect.DeepEqual(command.Files, wantFiles) ||
				command.Caller != registrydomain.Principal(alice) || command.ProductID != productID || command.AssetID != apiAssetID {
				t.Errorf("the use case was handed %+v, want the files as they were sent, for alice and the asset", command)
			}
		})
	}
}

// A revision shows its origin exactly when it has one, member for member as
// it was declared. One whose kind and origin disagree is a failure of the
// service and is answered as one, not shown half.
func TestARevisionShowsItsOriginExactlyWhenItHasOne(t *testing.T) {
	uploaded := fromGit()
	uploaded.SourceKind, uploaded.Origin = registrydomain.SourceUpload, registrydomain.GitOrigin{}
	unnamed := fromGit()
	unnamed.Origin.Ref = ""

	shown := map[string]struct {
		revision registrydomain.Revision
		kind     string
		origin   any
	}{
		"from a commit on a branch": {fromGit(), "GIT", map[string]any{
			"repository": contractsRepository, "commit": contractsCommit, "ref": "refs/heads/main"}},
		"from a commit, no reference said": {unnamed, "GIT", map[string]any{
			"repository": contractsRepository, "commit": contractsCommit}},
		"uploaded": {uploaded, "UPLOAD", nil},
	}
	for name, c := range shown {
		t.Run(name, func(t *testing.T) {
			fake := &contractRegistry{revision: c.revision}
			s := newRegistryService(t, fake)

			answers := map[string]map[string]any{}
			read := s.do(t, call{Method: http.MethodGet, Target: revisions + "/" + revisionID, Token: alice.Subject})
			imported := s.do(t, call{Method: http.MethodPost, Target: revisions, Token: alice.Subject, Body: sourceUpload(nil)})
			listed := s.do(t, call{Method: http.MethodGet, Target: revisions, Token: alice.Subject})
			if read.Code != http.StatusOK || imported.Code != http.StatusOK || listed.Code != http.StatusOK {
				t.Fatalf("read %d, imported again %d, listed %d; want 200 each", read.Code, imported.Code, listed.Code)
			}
			answers["read"], answers["imported again"] = members(t, read), members(t, imported)
			items, _ := members(t, listed)["items"].([]any)
			if len(items) != 1 {
				t.Fatalf("the list holds %v, want the one revision", items)
			}
			answers["listed"], _ = items[0].(map[string]any)

			for how, body := range answers {
				origin, said := body["git_origin"]
				if body["source_kind"] != c.kind || said != (c.origin != nil) || (said && !reflect.DeepEqual(origin, c.origin)) {
					t.Errorf("%s: source_kind %v and git_origin %v, want %s and %v", how, body["source_kind"], origin, c.kind, c.origin)
				}
			}
		})
	}

	headless := fromGit()
	headless.Origin = registrydomain.GitOrigin{}
	stray := fromGit()
	stray.SourceKind = registrydomain.SourceUpload
	unknown := fromGit()
	unknown.SourceKind, unknown.Origin = "EDITOR", registrydomain.GitOrigin{}
	for name, revision := range map[string]registrydomain.Revision{
		"GIT without an origin":  headless,
		"UPLOAD with an origin":  stray,
		"a kind nobody declared": unknown,
		"no kind and no origin":  {ID: revisionID},
		"an origin and no kind":  {ID: revisionID, Origin: fromGit().Origin},
	} {
		t.Run(name, func(t *testing.T) {
			s := newRegistryService(t, &contractRegistry{revision: revision})
			for _, c := range []call{
				{Method: http.MethodGet, Target: revisions + "/" + revisionID, Token: alice.Subject},
				{Method: http.MethodGet, Target: revisions, Token: alice.Subject},
				{Method: http.MethodPost, Target: revisions, Token: alice.Subject, Body: sourceUpload(nil)},
			} {
				wantProblem(t, s.do(t, c), http.StatusInternalServerError, CodeInternalError, "")
			}
		})
	}
}

// The contract states what an origin is in the words of the model, so what
// the validator lets through is what the model accepts.
func TestTheContractStatesAGitOriginAsTheModelDoes(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	schemas := contract.Components.Schemas
	for name, want := range map[string]struct {
		pattern   string
		maxLength uint64
	}{
		"GitRepository": {registrydomain.RepositoryPattern, registrydomain.RepositoryMaxLength},
		"GitCommit":     {registrydomain.CommitPattern, 0},
		"GitRef":        {registrydomain.RefPattern, registrydomain.RefMaxLength},
	} {
		if schemas[name] == nil {
			t.Errorf("the contract declares no %s", name)
			continue
		}
		schema := schemas[name].Value
		if schema.Pattern != want.pattern {
			t.Errorf("the contract states the pattern of %s %q, the model %q", name, schema.Pattern, want.pattern)
		}
		switch {
		case want.maxLength == 0 && schema.MaxLength != nil:
			t.Errorf("the contract bounds %s at %d characters, the model by its pattern alone", name, *schema.MaxLength)
		case want.maxLength != 0 && (schema.MaxLength == nil || *schema.MaxLength != want.maxLength):
			t.Errorf("the contract bounds %s at %v characters, the model at %d", name, schema.MaxLength, want.maxLength)
		}
	}
	origin := schemas["GitOrigin"]
	if origin == nil {
		t.Fatal("the contract declares no GitOrigin")
	}
	if got := origin.Value.Required; !reflect.DeepEqual(got, []string{"repository", "commit"}) {
		t.Errorf("the contract requires %v of an origin, the model a repository and a commit", got)
	}
	for member, schema := range map[string]string{"repository": "GitRepository", "commit": "GitCommit", "ref": "GitRef"} {
		if property := origin.Value.Properties[member]; property == nil || property.Ref != "#/components/schemas/"+schema {
			t.Errorf("the %s of an origin is not declared by %s", member, schema)
		}
	}
	for _, object := range []string{"ContractSourceUpload", "ContractRevision"} {
		if property := schemas[object].Value.Properties["git_origin"]; property == nil || property.Ref != "#/components/schemas/GitOrigin" {
			t.Errorf("the git_origin of %s is not declared by GitOrigin", object)
		}
	}
	var kinds []string
	for _, value := range schemas["ContractRevision"].Value.Properties["source_kind"].Value.Enum {
		kind, _ := value.(string)
		kinds = append(kinds, kind)
	}
	if !reflect.DeepEqual(kinds, []string{registrydomain.SourceUpload, registrydomain.SourceGit}) {
		t.Errorf("the contract declares the source kinds %v, the model %s and %s", kinds, registrydomain.SourceUpload, registrydomain.SourceGit)
	}
}

// What the registry refuses of an origin is a problem of its own code, and
// what the model refuses says nothing of what it was handed.
func TestARefusedOriginIsAProblemThatRepeatsNothing(t *testing.T) {
	const secret = "glpat-0123456789abcdefghij"
	cases := map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"the commit is recorded with other files": {
			fmt.Errorf("register the revision: %w", registryports.ErrOriginConflict),
			http.StatusConflict, "GIT_ORIGIN_CONFLICT", "This entrypoint of the commit is already recorded for the asset with other files."},
		"an origin the model refuses": {
			fmt.Errorf("%w: it was https://ci:%s@git.example/contracts", registrydomain.ErrInvalidOrigin, secret),
			http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newRegistryService(t, &contractRegistry{err: c.err})
			origin := map[string]string{"repository": contractsRepository, "commit": contractsCommit}
			recorder := s.do(t, call{Method: http.MethodPost, Target: revisions, Token: alice.Subject, Body: sourceUpload(origin)})
			wantProblem(t, recorder, c.status, c.code, c.detail)
			if bytes.Contains(recorder.Body.Bytes(), []byte(secret)) || bytes.Contains(s.log.Bytes(), []byte(secret)) {
				t.Errorf("the answer or the log repeats what was refused: %s / %s", recorder.Body, s.log)
			}
		})
	}
}
