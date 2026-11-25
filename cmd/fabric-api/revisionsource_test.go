package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// sent is one file of a bundle, as it is submitted.
type sent struct{ path, content string }

// importBundle is the body of an import of the files in the order given,
// with the origin if there is one.
func importBundle(entrypoint string, origin map[string]any, files ...sent) string {
	body := map[string]any{"entrypoint": entrypoint}
	encoded := make([]map[string]string, 0, len(files))
	for _, file := range files {
		encoded = append(encoded, map[string]string{
			"path": file.path, "content_base64": base64.StdEncoding.EncodeToString([]byte(file.content)),
		})
	}
	body["files"] = encoded
	if origin != nil {
		body["git_origin"] = origin
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// recordedAs is how a file is answered once it is recorded.
func recordedAs(file sent) map[string]any {
	return map[string]any{
		"path": file.path, "digest": fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(file.content))),
		"content_base64": base64.StdEncoding.EncodeToString([]byte(file.content)),
	}
}

// The bundle of these tests: an entrypoint with Windows line endings that
// references one file, and a file nothing references.
var (
	bundleEntrypoint = sent{"contracts/openapi.yaml", "openapi: 3.1.2\r\ninfo: {title: 运单, version: v1}\r\npaths: {}\r\n" +
		"components:\r\n  schemas:\r\n    Shipment: {$ref: '../schemas/common.json#/Shipment'}\r\n"}
	bundleSchema = sent{"schemas/common.json", `{"Shipment":{"type":"object","description":"Größe & <weight>"}}` + "\n\n"}
	bundleUnused = sent{"drafts/unused.yaml", "type: string\n"}
)

// An author reads back the files a revision was recorded from: the closure
// that was captured, byte for byte, each file under its digest. Whoever may
// not read the revision is answered for its files what it is answered for
// the revision. Reading leaves no audit record and no event.
func TestTheSourceOfARevisionIsReadBackByWhoMayReadTheRevision(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	membership := s.join(product, workspace, "owner", "author", "internal_author")
	other := s.createWorkspace(product, "owner", "operations")
	s.join(product, other, "owner", "elsewhere", "internal_author")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")
	sibling := s.revisionAsset(product, workspace, "maintainer", "billing-api", "HTTP")

	origin := gitOrigin(contractsRepository, commitOnMain, "refs/heads/main")
	imported := s.call(http.MethodPost, path, "author",
		importBundle(bundleEntrypoint.path, origin, bundleSchema, bundleUnused, bundleEntrypoint))
	uploaded := s.call(http.MethodPost, path, "author", uploadRevision("openapi.yaml", revisionSource))
	elsewhere := s.call(http.MethodPost, sibling, "author", uploadRevision("openapi.yaml", revisionSource))
	if imported.status != http.StatusCreated || uploaded.status != http.StatusCreated || elsewhere.status != http.StatusCreated {
		t.Fatalf("the imports: %d %v, %d %v, %d %v", imported.status, imported.body, uploaded.status, uploaded.body, elsewhere.status, elsewhere.body)
	}
	revision := path + "/" + imported.id("revision_id")
	source := revision + "/source"
	records, events := s.count("platformoperations.audit_record"), s.count("bento.outbox")

	got := s.call(http.MethodGet, source, "author", "")
	want := map[string]any{
		"revision_id": imported.id("revision_id"), "source_snapshot_id": imported.id("source_snapshot_id"),
		"entrypoint": bundleEntrypoint.path, "source_digest": imported.body["source_digest"],
		"ref_closure_digest": imported.body["ref_closure_digest"],
		"files":              []any{recordedAs(bundleEntrypoint), recordedAs(bundleSchema)},
	}
	if got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
		t.Fatalf("the source of the bundle: %d\n %v\nwant the entrypoint and what it references, by path, as they were sent\n %v", got.status, got.body, want)
	}
	if got.header.Get("Cache-Control") != "no-store" || got.header.Get("Content-Type") != "application/json" {
		t.Errorf("the source is answered with the headers %v, want JSON that is not stored", got.header)
	}
	if digest := recordedAs(bundleEntrypoint)["digest"]; got.body["source_digest"] != digest {
		t.Errorf("the digest of the source is %v, the digest of the entrypoint %v", got.body["source_digest"], digest)
	}
	if owner := s.call(http.MethodGet, source, "owner", ""); owner.status != http.StatusOK || !reflect.DeepEqual(owner.body, got.body) {
		t.Errorf("the source read by the owner is %d %v, want what the author read", owner.status, owner.body)
	}

	single := s.call(http.MethodGet, path+"/"+uploaded.id("revision_id")+"/source", "author", "")
	if files := []any{recordedAs(sent{"openapi.yaml", revisionSource})}; single.status != http.StatusOK ||
		!reflect.DeepEqual(single.body["files"], files) || single.body["source_digest"] != uploaded.body["source_digest"] {
		t.Errorf("the source of an upload: %d %v, want its one file", single.status, single.body)
	}

	// A revision is read under its asset, and so is its source.
	s.expect("the revision of another asset, and one that does not exist", "author", []exchange{
		{http.MethodGet, sibling + "/" + imported.id("revision_id") + "/source", "", http.StatusNotFound, "REVISION_NOT_FOUND"},
		{http.MethodGet, path + "/" + elsewhere.id("revision_id") + "/source", "", http.StatusNotFound, "REVISION_NOT_FOUND"},
		{http.MethodGet, path + "/00000000-0000-4000-8000-000000000000/source", "", http.StatusNotFound, "REVISION_NOT_FOUND"},
		{http.MethodGet, path + "/latest/source", "", http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, source, "{}", http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
	})

	// Reading is not a fact of the product: it leaves no audit record and
	// no event, found or not.
	if after, sent := s.count("platformoperations.audit_record"), s.count("bento.outbox"); after != records || sent != events {
		t.Errorf("the reads left %d audit records and %d events", after-records, sent-events)
	}

	// Whoever may not read the revision is told the same about its source.
	s.join(product, workspace, "owner", "reader", "internal_reader")
	withheld := map[string]int{
		"reader":    http.StatusForbidden,
		"elsewhere": http.StatusNotFound, "stranger": http.StatusNotFound, "": http.StatusUnauthorized,
	}
	refusals := func() int {
		return occurrences(s.recorded(func(row audited) bool { return row.Action == "revision.read" && row.Outcome != "SUCCEEDED" }))
	}
	for caller, status := range withheld {
		before := refusals()
		ofSource, ofRevision := s.call(http.MethodGet, source, caller, ""), s.call(http.MethodGet, revision, caller, "")
		if ofSource.status != status || !sameAnswer(ofSource, ofRevision) || ofSource.body["files"] != nil {
			t.Errorf("%q asks for the source: %d %v, want %d and what the revision answers %v", caller, ofSource.status, ofSource.body, status, ofRevision.body)
		}
		// A refusal of the source is on record exactly as a refusal of the
		// revision is.
		if recorded := refusals() - before; recorded%2 != 0 || (status == http.StatusForbidden) != (recorded == 2) {
			t.Errorf("%q: the two refusals left %d audit records", caller, recorded)
		}
	}
	// The grant is read with every request: an author whose membership
	// ended reads neither the revision nor its source.
	s.revoke(membership, "owner")
	ofSource, ofRevision := s.call(http.MethodGet, source, "author", ""), s.call(http.MethodGet, revision, "author", "")
	if ofSource.status < http.StatusForbidden || !sameAnswer(ofSource, ofRevision) || ofSource.body["files"] != nil {
		t.Errorf("an author whose membership ended asks for the source: %d %v, want what the revision answers: %d %v",
			ofSource.status, ofSource.body, ofRevision.status, ofRevision.body)
	}
}

// Files that storage lost or altered are not handed out as the source of a
// revision. The revision itself is still read, the cause is logged for
// whoever operates the service, and once storage holds the recorded files
// again the source is answered as before.
func TestFilesThatAreNotTheRecordedOnesAreNotHandedOut(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "internal_author")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")

	imported := s.call(http.MethodPost, path, "author", importBundle(bundleEntrypoint.path, nil, bundleEntrypoint, bundleSchema))
	if imported.status != http.StatusCreated {
		t.Fatalf("the import: %d %v", imported.status, imported.body)
	}
	revision := path + "/" + imported.id("revision_id")
	snapshot := imported.id("source_snapshot_id")
	intact := s.call(http.MethodGet, revision+"/source", "author", "")
	if intact.status != http.StatusOK {
		t.Fatalf("the source as it was recorded: %d %v", intact.status, intact.body)
	}

	// The process cannot do any of this: it may add files and read them.
	app := pgtest.Connect(t, s.database.Application)
	for _, statement := range []string{
		`UPDATE contractregistry.source_file SET content = 'x'`,
		`DELETE FROM contractregistry.source_file`,
		`UPDATE contractregistry.source_snapshot SET ref_closure_digest = source_digest`,
		`TRUNCATE contractregistry.source_file`,
	} {
		var refused *pgconn.PgError
		if _, err := app.Exec(t.Context(), statement); !errors.As(err, &refused) || refused.Code != "42501" {
			t.Fatalf("%s as the application role: %v, want it refused for lack of privilege", statement, err)
		}
	}

	damage := []struct {
		name         string
		break_, mend string
	}{
		{"a byte was added to a referenced file",
			`UPDATE contractregistry.source_file SET content = content || '\x20'::bytea WHERE source_snapshot_id = $1 AND path = 'schemas/common.json'`,
			`UPDATE contractregistry.source_file SET content = substring(content FROM 1 FOR octet_length(content) - 1) WHERE source_snapshot_id = $1 AND path = 'schemas/common.json'`},
		{"a line ending of the entrypoint was rewritten",
			`UPDATE contractregistry.source_file SET content = convert_to(replace(convert_from(content, 'UTF8'), E'\r\n', E'\n'), 'UTF8') WHERE source_snapshot_id = $1 AND path = 'contracts/openapi.yaml'`,
			`UPDATE contractregistry.source_file SET content = convert_to(replace(convert_from(content, 'UTF8'), E'\n', E'\r\n'), 'UTF8') WHERE source_snapshot_id = $1 AND path = 'contracts/openapi.yaml'`},
		{"a referenced file was renamed",
			`UPDATE contractregistry.source_file SET path = 'schemas/Common.json' WHERE source_snapshot_id = $1 AND path = 'schemas/common.json'`,
			`UPDATE contractregistry.source_file SET path = 'schemas/common.json' WHERE source_snapshot_id = $1 AND path = 'schemas/Common.json'`},
		{"a file was added",
			`INSERT INTO contractregistry.source_file (source_snapshot_id, path, content) VALUES ($1, 'extra.yaml', 'type: string')`,
			`DELETE FROM contractregistry.source_file WHERE source_snapshot_id = $1 AND path = 'extra.yaml'`},
		{"the digest of the closure was rewritten",
			`UPDATE contractregistry.source_snapshot SET ref_closure_digest = 'sha256:' || repeat('0', 64) WHERE source_snapshot_id = $1`,
			`UPDATE contractregistry.source_snapshot SET ref_closure_digest = '` + imported.id("ref_closure_digest") + `' WHERE source_snapshot_id = $1`},
		{"the digest of the entrypoint was rewritten",
			`UPDATE contractregistry.source_snapshot SET source_digest = 'sha256:' || repeat('0', 64) WHERE source_snapshot_id = $1`,
			`UPDATE contractregistry.source_snapshot SET source_digest = '` + imported.id("source_digest") + `' WHERE source_snapshot_id = $1`},
		{"a referenced file is gone",
			`DELETE FROM contractregistry.source_file WHERE source_snapshot_id = $1 AND path = 'schemas/common.json'`,
			`INSERT INTO contractregistry.source_file (source_snapshot_id, path, content)
			 VALUES ($1, 'schemas/common.json', decode('` + base64.StdEncoding.EncodeToString([]byte(bundleSchema.content)) + `', 'base64'))`},
		{"every file is gone",
			`CREATE TEMP TABLE kept AS SELECT * FROM contractregistry.source_file WHERE source_snapshot_id = $1;
			 DELETE FROM contractregistry.source_file WHERE source_snapshot_id = (SELECT source_snapshot_id FROM kept LIMIT 1)`,
			`INSERT INTO contractregistry.source_file SELECT * FROM kept; DROP TABLE kept`},
	}
	for _, d := range damage {
		t.Run(d.name, func(t *testing.T) {
			logged := len(s.log.String())
			s.alter(d.break_, snapshot)

			got := s.call(http.MethodGet, revision+"/source", "author", "")
			if got.status != http.StatusInternalServerError || got.code() != "INTERNAL_ERROR" || len(got.body) != 5 {
				t.Errorf("the source: %d %v, want 500 INTERNAL_ERROR and nothing of the files", got.status, got.body)
			}
			trace, _ := got.body["trace_id"].(string)
			// The path of the request names the revision as well, so the
			// cause is looked for in the words that carry it.
			if entry := s.log.String()[logged:]; trace == "" ||
				!strings.Contains(entry, "the source of revision "+imported.id("revision_id")+": the stored source files are not the ones the revision was recorded from") ||
				!strings.Contains(entry, "correlation_id="+trace) {
				t.Errorf("the log does not name the revision and the cause under the identifier of the request %s:\n%s", trace, entry)
			}
			// What the revision says of itself does not depend on its files.
			if metadata := s.call(http.MethodGet, revision, "author", ""); metadata.status != http.StatusOK {
				t.Errorf("the revision: %d %v, want it read", metadata.status, metadata.body)
			}

			s.alter(d.mend, snapshot)
			if mended := s.call(http.MethodGet, revision+"/source", "author", ""); mended.status != http.StatusOK || !reflect.DeepEqual(mended.body, intact.body) {
				t.Fatalf("with the recorded files back: %d %v, want the source as it was", mended.status, mended.body)
			}
		})
	}
}

// alter runs statements as the owner of the database, the way an operator or
// a fault of the storage would change what a process cannot. $1 stands for
// the snapshot, an identifier this service made.
func (s *stack) alter(statements, snapshot string) {
	s.t.Helper()

	for _, statement := range strings.Split(statements, ";") {
		statement = strings.ReplaceAll(statement, "$1", "'"+snapshot+"'")
		if _, err := s.owner.Exec(s.t.Context(), statement); err != nil {
			s.t.Fatalf("%s: %v", statement, err)
		}
	}
}
