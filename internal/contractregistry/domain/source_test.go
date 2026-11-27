package domain_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

func digestOf(content string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
}

// The files a revision was recorded from are read back as they were: in the
// order of their paths whatever order storage hands them in, each with the
// digest of its bytes.
func TestTheSourceOfARevisionIsReadBackAsItWasRecorded(t *testing.T) {
	files := []domain.SourceFile{
		{Path: "schemas/shipment.yaml", Content: "type: object\n"},
		{Path: "api/openapi.yaml", Content: "openapi: 3.1.2\n"},
		{Path: "schemas/Address.json", Content: `{"type":"object"}`},
	}
	snapshot, err := domain.CaptureSource("api/openapi.yaml", domain.OpenAPI, "3.1.2", files)
	if err != nil {
		t.Fatal(err)
	}
	revision := domain.Revision{
		Entrypoint: snapshot.Entrypoint, SpecVersion: snapshot.SpecVersion,
		SourceDigest: snapshot.SourceDigest, RefClosureDigest: snapshot.RefClosureDigest,
	}
	want := []domain.RecordedFile{
		{Path: "api/openapi.yaml", Digest: digestOf("openapi: 3.1.2\n"), Content: "openapi: 3.1.2\n"},
		{Path: "schemas/Address.json", Digest: digestOf(`{"type":"object"}`), Content: `{"type":"object"}`},
		{Path: "schemas/shipment.yaml", Digest: digestOf("type: object\n"), Content: "type: object\n"},
	}
	if want[0].Digest != revision.SourceDigest {
		t.Fatalf("the digest of the source %s is not the digest of the entrypoint %s", revision.SourceDigest, want[0].Digest)
	}

	for name, stored := range map[string][]domain.SourceFile{
		"as they were captured": snapshot.Files(),
		"as they were sent":     files,
		"the other way round":   {files[2], files[1], files[0]},
	} {
		handed := slices.Clone(stored)
		got, err := domain.RecordedSource(revision, stored)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v %v, want the three files by path with their digests", name, got, err)
		}
		if !reflect.DeepEqual(stored, handed) {
			t.Errorf("%s: the files handed in were reordered", name)
		}
	}
}

// Whatever storage did to the files of a revision, they are not handed out
// as its source: one byte, one path, one file more or one less is enough.
func TestStoredFilesThatAreNotTheRecordedOnesAreNotTheSource(t *testing.T) {
	entrypoint := domain.SourceFile{Path: "openapi.yaml", Content: "openapi: 3.1.2\ninfo: {title: Shipments, version: v1}\n"}
	schema := domain.SourceFile{Path: "schemas/shipment.yaml", Content: "type: object\n"}
	snapshot, err := domain.CaptureSource(entrypoint.Path, domain.OpenAPI, "3.1.2", []domain.SourceFile{entrypoint, schema})
	if err != nil {
		t.Fatal(err)
	}
	recorded := domain.Revision{
		Entrypoint: snapshot.Entrypoint, SpecVersion: snapshot.SpecVersion,
		SourceDigest: snapshot.SourceDigest, RefClosureDigest: snapshot.RefClosureDigest,
	}
	if _, err := domain.RecordedSource(recorded, []domain.SourceFile{entrypoint, schema}); err != nil {
		t.Fatalf("the files as they were recorded: %v", err)
	}

	with := func(change func(*domain.Revision)) domain.Revision {
		changed := recorded
		change(&changed)
		return changed
	}
	cases := map[string]struct {
		revision domain.Revision
		stored   []domain.SourceFile
	}{
		"a byte of the entrypoint changed": {recorded, []domain.SourceFile{
			{Path: entrypoint.Path, Content: entrypoint.Content + " "}, schema}},
		"a byte of a referenced file changed": {recorded, []domain.SourceFile{
			entrypoint, {Path: schema.Path, Content: "type: array\n"}}},
		"a line ending changed": {recorded, []domain.SourceFile{
			entrypoint, {Path: schema.Path, Content: "type: object\r\n"}}},
		"a referenced file was renamed": {recorded, []domain.SourceFile{
			entrypoint, {Path: "schemas/Shipment.yaml", Content: schema.Content}}},
		"a referenced file is gone":  {recorded, []domain.SourceFile{entrypoint}},
		"the entrypoint is gone":     {recorded, []domain.SourceFile{schema}},
		"every file is gone":         {recorded, nil},
		"a file was added":           {recorded, []domain.SourceFile{entrypoint, schema, {Path: "extra.yaml", Content: "x: 1\n"}}},
		"a file is stored twice":     {recorded, []domain.SourceFile{entrypoint, schema, schema}},
		"the two files were swapped": {recorded, []domain.SourceFile{{Path: entrypoint.Path, Content: schema.Content}, {Path: schema.Path, Content: entrypoint.Content}}},
		"the revision names another entrypoint": {
			with(func(r *domain.Revision) { r.Entrypoint = schema.Path }), []domain.SourceFile{entrypoint, schema}},
		"the revision carries another source digest": {
			with(func(r *domain.Revision) { r.SourceDigest = digestOf("other") }), []domain.SourceFile{entrypoint, schema}},
		"the revision carries another closure digest": {
			with(func(r *domain.Revision) { r.RefClosureDigest = digestOf("other") }), []domain.SourceFile{entrypoint, schema}},
		"the revision carries no digest": {
			with(func(r *domain.Revision) { r.SourceDigest, r.RefClosureDigest = "", "" }), []domain.SourceFile{entrypoint, schema}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := domain.RecordedSource(c.revision, c.stored)
			if !errors.Is(err, domain.ErrSourceDamaged) || got != nil {
				t.Fatalf("got %v %v, want no file and %v", got, err, domain.ErrSourceDamaged)
			}
		})
	}
}
