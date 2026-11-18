package http

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// revisionSource is where the files of the revision are read.
const revisionSource = revisions + "/" + revisionID + "/source"

func recordedFile(path, content string) registrydomain.RecordedFile {
	return registrydomain.RecordedFile{Path: path, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content))), Content: content}
}

// The files of a revision are answered byte for byte: a line ending, a byte
// order mark and text that is not ASCII leave as they were recorded, each
// under the digest the use case handed over, in the order it handed them in.
func TestTheSourceOfARevisionIsAnsweredByteForByte(t *testing.T) {
	entrypoint := "\ufeffopenapi: 3.1.2\r\ninfo: {title: 运单, version: v1}\r\n"
	schema := "{\"type\":\"object\",\"description\":\"Größe \\u0000 <b>&</b>\"}\n\n"
	fake := &contractRegistry{revision: fromGit(), files: []registrydomain.RecordedFile{
		recordedFile("api/openapi.yaml", entrypoint),
		recordedFile("api/schemas/Shipment.json", schema),
	}}
	s := newRegistryService(t, fake)

	recorder := s.do(t, call{Method: http.MethodGet, Target: revisionSource, Token: alice.Subject})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	revision := fromGit()
	want := map[string]any{
		"revision_id": revisionID, "source_snapshot_id": sourceSnapshotID, "entrypoint": "api/openapi.yaml",
		"source_digest": revision.SourceDigest, "ref_closure_digest": revision.RefClosureDigest,
		"files": []any{
			map[string]any{"path": "api/openapi.yaml", "digest": fake.files[0].Digest,
				"content_base64": base64.StdEncoding.EncodeToString([]byte(entrypoint))},
			map[string]any{"path": "api/schemas/Shipment.json", "digest": fake.files[1].Digest,
				"content_base64": base64.StdEncoding.EncodeToString([]byte(schema))},
		},
	}
	if got := members(t, recorder); !reflect.DeepEqual(got, want) {
		t.Errorf("the answer is\n %v\nwant\n %v", got, want)
	}

	if len(fake.sourcesRead) != 1 {
		t.Fatalf("the use case was asked %d times, want once", len(fake.sourcesRead))
	}
	asked := fake.sourcesRead[0]
	if asked.Revision != revisionID || asked.Query.Caller != registrydomain.Principal(alice) ||
		asked.Query.ProductID != productID || asked.Query.AssetID != apiAssetID || !uuid.MatchString(asked.Query.CorrelationID) {
		t.Errorf("the use case was asked %+v, want the revision of the asset, for alice, under the identifier of the request", asked)
	}
	if len(fake.read) != 0 || len(fake.imported) != 0 {
		t.Errorf("reading the source also read the revision %d times and imported %d times", len(fake.read), len(fake.imported))
	}
}

// Files that are not the recorded ones are a failure of the service. The
// caller learns that much and no more; whoever operates the service finds
// the revision in the log, under the identifier the caller was given.
func TestADamagedSourceIsAnInternalErrorThatIsLogged(t *testing.T) {
	fake := &contractRegistry{
		revision: fromGit(), files: []registrydomain.RecordedFile{recordedFile("api/openapi.yaml", contractSource)},
		err: fmt.Errorf("the source of revision %s: %w", revisionID, registrydomain.ErrSourceDamaged),
	}
	s := newRegistryService(t, fake)

	recorder := s.do(t, call{Method: http.MethodGet, Target: revisionSource, Token: alice.Subject})
	traceID := wantProblem(t, recorder, http.StatusInternalServerError, CodeInternalError, "")
	if body := recorder.Body.String(); strings.Contains(body, "source") || strings.Contains(body, revisionID) || strings.Contains(body, "content_base64") {
		t.Errorf("the answer says what failed, or carries a file: %s", body)
	}
	logged := s.log.String()
	for _, want := range []string{"operation failed", registrydomain.ErrSourceDamaged.Error(), revisionID, "correlation_id=" + traceID} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log does not hold %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, base64.StdEncoding.EncodeToString([]byte(contractSource))) || strings.Contains(logged, "Shipments") {
		t.Errorf("the log carries the files:\n%s", logged)
	}
}

// Who may not read the revision may not read its files, and is told what
// the revision itself would tell.
func TestTheSourceOfARevisionIsWithheldAsTheRevisionIs(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"the caller is no author of the workspace": {
			registry.ErrForbidden, http.StatusForbidden, CodeForbidden, "The caller is not a current internal author of the workspace."},
		"the asset has no such revision": {
			fmt.Errorf("read the source: %w", registry.ErrRevisionNotFound), http.StatusNotFound, "REVISION_NOT_FOUND", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			for _, target := range []string{revisionSource, revisions + "/" + revisionID} {
				fake := &contractRegistry{revision: fromGit(), files: []registrydomain.RecordedFile{recordedFile("api/openapi.yaml", contractSource)}, err: c.err}
				recorder := newRegistryService(t, fake).do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject})
				wantProblem(t, recorder, c.status, c.code, c.detail)
			}
		})
	}

	t.Run("nobody is asked for a caller nobody verified", func(t *testing.T) {
		for name, token := range map[string]string{"no credentials": "", "a token nobody accepts": "mallory"} {
			fake := &contractRegistry{revision: fromGit(), files: []registrydomain.RecordedFile{recordedFile("api/openapi.yaml", contractSource)}}
			recorder := newRegistryService(t, fake).do(t, call{Method: http.MethodGet, Target: revisionSource, Token: token})
			if recorder.Code != http.StatusUnauthorized || len(fake.sourcesRead) != 0 || strings.Contains(recorder.Body.String(), "content_base64") {
				t.Errorf("%s: status %d after %d reads, want 401 and no read: %s", name, recorder.Code, len(fake.sourcesRead), recorder.Body)
			}
		}
	})

	t.Run("an identifier that is none", func(t *testing.T) {
		fake := &contractRegistry{revision: fromGit()}
		recorder := newRegistryService(t, fake).do(t, call{Method: http.MethodGet, Target: revisions + "/latest/source", Token: alice.Subject})
		wantProblem(t, recorder, http.StatusBadRequest, CodeRequestInvalid, "The path parameter revision_id does not match the contract.")
		if len(fake.sourcesRead) != 0 {
			t.Errorf("the use case was asked for a revision named %q", "latest")
		}
	})
}

// The contract states the source of a revision as it is answered. What the
// service answers satisfies the schema the contract declares for it, and
// that schema is as closed as the answer: it accepts nothing with a member
// more or a member less.
func TestTheContractStatesTheSourceOfARevisionAsItIsAnswered(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	declared := contract.Components.Schemas["ContractRevisionSource"]
	if declared == nil {
		t.Fatal("the contract declares no source of a revision")
	}
	operation := contract.Paths.Find("/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}/source").Get
	if answer := operation.Responses.Status(http.StatusOK).Value.Content.Get("application/json"); answer == nil ||
		answer.Schema.Ref != "#/components/schemas/ContractRevisionSource" {
		t.Fatal("the source of a revision is not answered as a ContractRevisionSource")
	}

	fake := &contractRegistry{revision: fromGit(), files: []registrydomain.RecordedFile{
		recordedFile("api/openapi.yaml", contractSource),
		recordedFile("api/schemas/Shipment.json", "{\"type\":\"object\"}\n"),
	}}
	recorder := newRegistryService(t, fake).do(t, call{Method: http.MethodGet, Target: revisionSource, Token: alice.Subject})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	if err := declared.Value.VisitJSON(members(t, recorder)); err != nil {
		t.Fatalf("the contract refuses what the service answers: %v\n%s", err, recorder.Body)
	}

	first := func(body map[string]any) map[string]any {
		files, _ := body["files"].([]any)
		file, _ := files[0].(map[string]any)
		return file
	}
	without := func(member string) func(map[string]any) {
		return func(body map[string]any) { delete(body, member) }
	}
	fileWithout := func(member string) func(map[string]any) {
		return func(body map[string]any) { delete(first(body), member) }
	}
	refused := map[string]func(map[string]any){
		"a member the contract does not declare": func(body map[string]any) { body["manifest"] = "{}" },
		"no revision":                            without("revision_id"),
		"no snapshot":                            without("source_snapshot_id"),
		"no entrypoint":                          without("entrypoint"),
		"no digest of the entrypoint":            without("source_digest"),
		"no digest of the closure":               without("ref_closure_digest"),
		"nothing said of files":                  without("files"),
		"no file":                                func(body map[string]any) { body["files"] = []any{} },
		"more files than a bundle holds": func(body map[string]any) {
			files := make([]any, registrydomain.MaxSourceFiles+1)
			for i := range files {
				files[i] = first(body)
			}
			body["files"] = files
		},
		"a file with a member the contract does not declare": func(body map[string]any) { first(body)["media_type"] = "application/yaml" },
		"a file without its path":                            fileWithout("path"),
		"a file without its digest":                          fileWithout("digest"),
		"a file without its bytes":                           fileWithout("content_base64"),
		"a file of no bytes":                                 func(body map[string]any) { first(body)["content_base64"] = "" },
		"a digest that is not SHA-256":                       func(body map[string]any) { first(body)["digest"] = "md5:d41d8cd98f00b204e9800998ecf8427e" },
		"a path that is an absolute one":                     func(body map[string]any) { first(body)["path"] = "/etc/openapi.yaml" },
	}
	for name, change := range refused {
		t.Run(name, func(t *testing.T) {
			body := members(t, recorder)
			change(body)
			if err := declared.Value.VisitJSON(body); err == nil {
				t.Errorf("the contract accepts an answer with %s: %v", name, body)
			}
		})
	}
}

// The contract bounds a recorded file as it bounds a submitted one: what
// was accepted on the way in can be answered on the way out.
func TestARecordedFileIsDeclaredAsASubmittedOneIs(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	schemas := contract.Components.Schemas
	submitted, recorded, source := schemas["ContractSourceFile"], schemas["ContractRecordedFile"], schemas["ContractRevisionSource"]
	upload := schemas["ContractSourceUpload"]
	if submitted == nil || recorded == nil || source == nil || upload == nil {
		t.Fatal("the contract declares no submitted file, no recorded file, no upload or no source of a revision")
	}
	in, out := submitted.Value.Properties["content_base64"].Value, recorded.Value.Properties["content_base64"].Value
	if out.MaxLength == nil || in.MaxLength == nil || *out.MaxLength != *in.MaxLength || out.MinLength != in.MinLength {
		t.Errorf("a recorded file holds %d to %v characters, a submitted one %d to %v", out.MinLength, out.MaxLength, in.MinLength, in.MaxLength)
	}
	// The bound is the Base64 of the bytes a bundle may hold.
	if want := uint64((registrydomain.MaxSourceBytes + 2) / 3 * 4); out.MaxLength == nil || *out.MaxLength != want {
		t.Errorf("a recorded file holds up to %v characters, the model %d bytes, which are %d", out.MaxLength, registrydomain.MaxSourceBytes, want)
	}
	if got, want := recorded.Value.Properties["path"].Ref, submitted.Value.Properties["path"].Ref; got != want || want == "" {
		t.Errorf("the path of a recorded file is declared by %q, of a submitted one by %q", got, want)
	}
	files, sent := source.Value.Properties["files"].Value, upload.Value.Properties["files"].Value
	if files.MaxItems == nil || *files.MaxItems != registrydomain.MaxSourceFiles || files.MinItems != 1 ||
		sent.MaxItems == nil || *sent.MaxItems != *files.MaxItems {
		t.Errorf("a source holds %d to %v files, the model 1 to %d, an upload up to %v", files.MinItems, files.MaxItems, registrydomain.MaxSourceFiles, sent.MaxItems)
	}
}
