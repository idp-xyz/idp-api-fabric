package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const (
	contractsRepository = "https://git.example/logistics/contracts"
	commitOnMain        = "1111111111111111111111111111111111111111"
	commitTagged        = "2222222222222222222222222222222222222222"
	commitRevised       = "3333333333333333333333333333333333333333333333333333333333333333"
)

// importFromGit is the body of an import that names where its one file came
// from. origin is sent as it is given, member for member, so a test can say
// what the contract refuses.
func importFromGit(entrypoint, content string, origin map[string]any) string {
	encoded, err := json.Marshal(map[string]any{
		"entrypoint": entrypoint,
		"files": []map[string]string{
			{"path": entrypoint, "content_base64": base64.StdEncoding.EncodeToString([]byte(content))},
		},
		"git_origin": origin,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func gitOrigin(repository, commit, ref string) map[string]any {
	origin := map[string]any{"repository": repository, "commit": commit}
	if ref != "" {
		origin["ref"] = ref
	}
	return origin
}

// An internal author imports the files of a commit and says which commit.
// The revision carries the origin as it was declared, member for member. The
// uploads of the asset and the repository are two sources: the same files
// are recorded once from each. What a later commit left alone is the revision
// recorded first, and other files said to be of a recorded commit are
// refused. Every import that records something leaves one audit record and
// one event, and one that is refused leaves nothing.
func TestARevisionIsImportedWithTheGitOriginItsSubmitterDeclares(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "ci", "internal_author")
	other := s.createWorkspace(product, "owner", "operations")
	s.join(product, other, "owner", "elsewhere", "internal_author")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")
	const entrypoint = "api/openapi.yaml"
	revised := revisionSource + "# revised\n"

	uploaded := s.call(http.MethodPost, path, "ci", uploadRevision(entrypoint, revisionSource))
	if _, said := uploaded.body["git_origin"]; uploaded.status != http.StatusCreated || uploaded.body["source_kind"] != "UPLOAD" || said {
		t.Fatalf("an upload: %d %v, want an UPLOAD that says nothing of an origin", uploaded.status, uploaded.body)
	}

	onMain := gitOrigin(contractsRepository, commitOnMain, "refs/heads/main")
	created := s.call(http.MethodPost, path, "ci", importFromGit(entrypoint, revisionSource, onMain))
	if created.status != http.StatusCreated || created.body["source_kind"] != "GIT" ||
		!reflect.DeepEqual(created.body["git_origin"], onMain) || created.body["revision_sequence"] != float64(2) ||
		created.body["ref_closure_digest"] != uploaded.body["ref_closure_digest"] ||
		created.header.Get("Location") != path+"/"+created.id("revision_id") || created.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the same files from a commit: %d %v %v, want a second revision under the origin as it was declared",
			created.status, created.body, created.header)
	}
	for when, origin := range map[string]map[string]any{
		"the same commit again":                    onMain,
		"a later commit that left the files alone": gitOrigin(contractsRepository, commitTagged, "refs/tags/v1.0.0"),
	} {
		again := s.call(http.MethodPost, path, "ci", importFromGit(entrypoint, revisionSource, origin))
		if again.status != http.StatusOK || !reflect.DeepEqual(again.body, created.body) || again.header.Get("Location") != "" {
			t.Fatalf("%s: %d %v, want 200 with the revision as it was recorded %v", when, again.status, again.body, created.body)
		}
	}

	s.expect("other files said to be of a commit that is recorded", "ci", []exchange{
		{http.MethodPost, path, importFromGit(entrypoint, revised, onMain), http.StatusConflict, "GIT_ORIGIN_CONFLICT"},
		{http.MethodPost, path, importFromGit(entrypoint, revised, gitOrigin(contractsRepository, commitOnMain, "")), http.StatusConflict, "GIT_ORIGIN_CONFLICT"},
	})
	unnamed := gitOrigin(contractsRepository, commitRevised, "")
	next := s.call(http.MethodPost, path, "ci", importFromGit(entrypoint, revised, unnamed))
	if next.status != http.StatusCreated || !reflect.DeepEqual(next.body["git_origin"], unnamed) || next.body["revision_sequence"] != float64(3) {
		t.Fatalf("other files of another commit, without a reference: %d %v, want a third revision whose origin names no reference", next.status, next.body)
	}
	s.expect("the first files said to be of the commit that revised them", "ci", []exchange{
		{http.MethodPost, path, importFromGit(entrypoint, revisionSource, unnamed), http.StatusConflict, "GIT_ORIGIN_CONFLICT"},
	})

	// The origin is read back with the revision, alone and in the history.
	detail := s.call(http.MethodGet, created.header.Get("Location"), "ci", "")
	if detail.status != http.StatusOK || !reflect.DeepEqual(detail.body, created.body) {
		t.Fatalf("the revision read back: %d %v, want %v", detail.status, detail.body, created.body)
	}
	items, token := s.page(path, "ci")
	if len(items) != 3 || token != "" || !reflect.DeepEqual(items[0], uploaded.body) ||
		!reflect.DeepEqual(items[1], created.body) || !reflect.DeepEqual(items[2], next.body) {
		t.Fatalf("the history: %v, want the upload and the two commits as they were answered", items)
	}

	// Who may import is what it was: naming a commit grants nothing.
	body := importFromGit(entrypoint, revisionSource+"# by somebody else\n", gitOrigin(contractsRepository, commitTagged, ""))
	for _, caller := range []string{"owner", "maintainer"} {
		s.expect("an author grant is required", caller, []exchange{{http.MethodPost, path, body, http.StatusForbidden, "FORBIDDEN"}})
	}
	s.expect("an author of another workspace does not see the asset", "elsewhere", []exchange{
		{http.MethodPost, path, body, http.StatusNotFound, "API_ASSET_NOT_FOUND"},
	})

	var events, snapshots int
	if err := s.owner.QueryRow(t.Context(), `
SELECT (SELECT count(*) FROM bento.outbox WHERE event_type = 'fabric.contractregistry.revision.registered' AND scope = $1),
       (SELECT count(*) FROM contractregistry.source_snapshot
        WHERE source_kind = 'GIT' AND origin_repository = $2 AND captured_by_subject = 'ci'
          AND ((origin_commit = $3 AND origin_ref = 'refs/heads/main') OR (origin_commit = $4 AND origin_ref IS NULL)))`,
		product, contractsRepository, commitOnMain, commitRevised).Scan(&events, &snapshots); err != nil {
		t.Fatal(err)
	}
	// Three imports recorded a revision; two were answered the one that was
	// recorded and three were refused for the commit they named, and those
	// left nothing. The two callers who may not import are on record.
	imports := s.recorded(func(row audited) bool {
		return row.Action == "revision.create" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-01" && row.Subject == "ci"
	})
	others := s.recorded(func(row audited) bool {
		return row.Action == "revision.create" && (row.Outcome != "SUCCEEDED" || row.Rule != "AC-01" || row.Subject != "ci")
	})
	if events != 3 || snapshots != 2 || occurrences(imports) != 3 || len(imports) != 3 || occurrences(others) != 2 ||
		s.count("contractregistry.contract_revision") != 3 || s.count("contractregistry.source_snapshot") != 3 {
		t.Fatalf("the evidence: %d events, %d snapshots of the two commits, audit records of an import %v and of anything else %v, %d revisions",
			events, snapshots, imports, others, s.count("contractregistry.contract_revision"))
	}
}

// occurrences is how many audit records were counted, alike or not.
func occurrences(records map[audited]int) int {
	total := 0
	for _, count := range records {
		total += count
	}
	return total
}

// The contract states what an origin is, so a request that names another one
// is refused before anything runs: nothing is recorded, and a URL with a
// token in it reaches neither the database nor the log.
func TestAnOriginTheContractDoesNotStateIsRefusedBeforeAnythingRuns(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "ci", "internal_author")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")
	const secret = "glpat-0123456789abcdefghij"

	with := func(member string, value any) map[string]any {
		origin := gitOrigin(contractsRepository, commitOnMain, "refs/heads/main")
		if value == nil {
			delete(origin, member)
		} else {
			origin[member] = value
		}
		return origin
	}
	refused := map[string]map[string]any{
		"a repository over plain http":       with("repository", "http://git.example/logistics/contracts"),
		"an SSH remote":                      with("repository", "git@git.example:logistics/contracts.git"),
		"a repository with a token":          with("repository", "https://ci:"+secret+"@git.example/logistics/contracts"),
		"a repository with a query":          with("repository", contractsRepository+"?private_token="+secret),
		"a repository with a trailing slash": with("repository", contractsRepository+"/"),
		"a host in upper case":               with("repository", "https://Git.Example/logistics/contracts"),
		"a host alone":                       with("repository", "https://git.example"),
		"a repository longer than allowed":   with("repository", "https://git.example/"+strings.Repeat("r", 281)),
		"no repository":                      with("repository", nil),
		"an abbreviated commit":              with("commit", commitOnMain[:7]),
		"a commit in upper case":             with("commit", strings.ToUpper("abcdef0123456789abcdef0123456789abcdef01")),
		"a branch for a commit":              with("commit", "main"),
		"no commit":                          with("commit", nil),
		"a short branch name":                with("ref", "main"),
		"a reference with a space":           with("ref", "refs/heads/my branch"),
		"a reference longer than allowed":    with("ref", "refs/heads/"+strings.Repeat("b", 190)),
		"a reference that is no text":        with("ref", 7),
		"a member nobody declared":           with("author", "alice@example.org"),
		"no origin at all":                   {},
	}
	for name, origin := range refused {
		got := s.call(http.MethodPost, path, "ci", importFromGit("openapi.yaml", revisionSource, origin))
		if got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
			t.Errorf("%s: %d %v, want 400 REQUEST_INVALID", name, got.status, got.body)
		}
	}
	null := strings.Replace(importFromGit("openapi.yaml", revisionSource, map[string]any{}), `"git_origin":{}`, `"git_origin":null`, 1)
	if got := s.call(http.MethodPost, path, "ci", null); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
		t.Errorf("an origin that is null: %d %v, want 400 REQUEST_INVALID", got.status, got.body)
	}

	// The bounds themselves are accepted: what is refused above is refused
	// for what it says, not for its length.
	longest := gitOrigin("https://git.example/"+strings.Repeat("r", 280), commitOnMain, "refs/heads/"+strings.Repeat("订", 189))
	if got := s.call(http.MethodPost, path, "ci", importFromGit("openapi.yaml", revisionSource, longest)); got.status != http.StatusCreated ||
		!reflect.DeepEqual(got.body["git_origin"], longest) {
		t.Fatalf("the longest origin: %d %v, want it recorded as it was declared", got.status, got.body)
	}
	if rows := s.count("contractregistry.source_snapshot"); rows != 1 {
		t.Errorf("%d snapshots are recorded, want the one that was accepted", rows)
	}
	if strings.Contains(s.log.String(), secret) {
		t.Error("the log repeats a token a refused repository carried")
	}
	var stored int
	if err := s.owner.QueryRow(t.Context(), `
SELECT (SELECT count(*) FROM platformoperations.audit_record WHERE audit_record::text LIKE '%' || $1 || '%')
     + (SELECT count(*) FROM bento.outbox WHERE outbox::text LIKE '%' || $1 || '%')`, secret).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("%d rows of the audit trail and the outbox repeat the token: %v", stored, err)
	}
}
