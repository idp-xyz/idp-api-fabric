package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAReleaseVersionIsOneShortToken(t *testing.T) {
	for _, version := range []string{"1", "1.0.0", "2026.11", "v1.2.3-rc.1+build.5", "A_b", strings.Repeat("9", 64)} {
		if err := ValidateReleaseVersion(version); err != nil {
			t.Errorf("%q is refused: %v", version, err)
		}
	}
	for _, version := range []string{
		"", " 1.0.0", "1.0.0 ", "-1", ".1", "+1", "1/0", "1 0", "一", "1.0\n", strings.Repeat("9", 65),
	} {
		if err := ValidateReleaseVersion(version); !errors.Is(err, ErrReleaseVersion) {
			t.Errorf("%q: got %v, want %v", version, err, ErrReleaseVersion)
		}
	}
}

// The largest source a revision can be recorded from makes an artifact the
// table of artifacts takes: as many files as a source may have, under the
// longest paths, with as many bytes as a source may have, each file of a
// size that its encoding pads.
func TestTheLargestSourceMakesAnArtifactWithinTheBound(t *testing.T) {
	each := MaxSourceBytes / MaxSourceFiles
	if each%3 != 1 || each*MaxSourceFiles != MaxSourceBytes {
		t.Fatalf("%d bytes in %d files are %d each, which is not the worst case this test is written for", MaxSourceBytes, MaxSourceFiles, each)
	}
	files := make([]SourceFile, 0, MaxSourceFiles)
	for i := range MaxSourceFiles {
		name := fmt.Sprintf("%02d", i) + strings.Repeat("p", MaxSourcePath-len("00.yaml")) + ".yaml"
		if len(name) != MaxSourcePath || !ValidSourcePath(name) {
			t.Fatalf("%q is no path of the longest size", name)
		}
		files = append(files, SourceFile{Path: name, Content: strings.Repeat("#", each)})
	}
	snapshot, err := CaptureSource(files[0].Path, "3.1.2", files)
	if err != nil {
		t.Fatal(err)
	}
	payload, digest, err := BuildArtifact(Revision{
		Entrypoint: snapshot.Entrypoint, SpecVersion: snapshot.SpecVersion,
		SourceDigest: snapshot.SourceDigest, RefClosureDigest: snapshot.RefClosureDigest,
	}, files)
	if err != nil || VerifyArtifact(digest, payload) != nil {
		t.Fatalf("the artifact of the largest source: %v, %v", err, VerifyArtifact(digest, payload))
	}
	if len(payload) > MaxArtifactBytes {
		t.Errorf("the largest source makes an artifact of %d bytes, more than the %d the table takes", len(payload), MaxArtifactBytes)
	}
}

// An artifact is the files a revision was recorded from, written out one
// way, under the digest of what was written. Files that are not the recorded
// ones make no artifact, and bytes that are not the ones of the digest are
// not an artifact.
func TestAnArtifactIsTheRecordedFilesUnderItsDigest(t *testing.T) {
	files := []SourceFile{
		{Path: "openapi.yaml", Content: "openapi: 3.1.2\r\ninfo: {title: 运单, version: v1}\r\npaths: {}\r\n" +
			"components:\r\n  schemas:\r\n    Shipment: {$ref: 'schemas/common.json#/Shipment'}\r\n"},
		{Path: "schemas/common.json", Content: `{"Shipment":{"type":"object"}}` + "\n"},
	}
	snapshot, err := CaptureSource("openapi.yaml", "3.1.2", files)
	if err != nil {
		t.Fatal(err)
	}
	revision := Revision{
		Entrypoint: snapshot.Entrypoint, SpecVersion: snapshot.SpecVersion,
		SourceDigest: snapshot.SourceDigest, RefClosureDigest: snapshot.RefClosureDigest,
	}

	payload, digest, err := BuildArtifact(revision, []SourceFile{files[1], files[0]})
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("sha256:%x", sha256.Sum256(payload)); digest != want || VerifyArtifact(digest, payload) != nil {
		t.Fatalf("the artifact is under %s, its bytes digest to %s (%v)", digest, want, VerifyArtifact(digest, payload))
	}
	if again, same, err := BuildArtifact(revision, files); err != nil || same != digest || string(again) != string(payload) {
		t.Fatalf("the same files in another order make another artifact: %s, %v", same, err)
	}
	var written struct {
		Format      string `json:"format"`
		Entrypoint  string `json:"entrypoint"`
		SpecVersion string `json:"spec_version"`
		Closure     string `json:"ref_closure_digest"`
		Files       []struct {
			Path    string `json:"path"`
			Digest  string `json:"digest"`
			Content []byte `json:"content_base64"`
		} `json:"files"`
	}
	if err := json.Unmarshal(payload, &written); err != nil {
		t.Fatal(err)
	}
	if written.Format != ArtifactFormat || written.Entrypoint != "openapi.yaml" || written.SpecVersion != "3.1.2" ||
		written.Closure != revision.RefClosureDigest || len(written.Files) != 2 {
		t.Fatalf("the artifact says %+v", written)
	}
	for i, file := range files {
		if got := written.Files[i]; got.Path != file.Path || string(got.Content) != file.Content ||
			got.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(file.Content))) {
			t.Errorf("file %d of the artifact is %s under %s, want %s byte for byte", i, got.Path, got.Digest, file.Path)
		}
	}

	damaged := map[string][]SourceFile{
		"a byte changed":   {{Path: files[0].Path, Content: files[0].Content + " "}, files[1]},
		"a file is gone":   {files[0]},
		"a file was added": {files[0], files[1], {Path: "extra.yaml", Content: "x: 1\n"}},
		"no file":          nil,
	}
	for name, stored := range damaged {
		if payload, digest, err := BuildArtifact(revision, stored); !errors.Is(err, ErrSourceDamaged) || payload != nil || digest != "" {
			t.Errorf("%s: got %s, %v; want %v and no artifact", name, digest, err, ErrSourceDamaged)
		}
	}
	other := append([]byte(nil), payload...)
	other[len(other)-2] ^= 1
	for name, c := range map[string]struct {
		digest  string
		payload []byte
	}{
		"a byte of the artifact changed": {digest, other},
		"another digest":                 {"sha256:" + strings.Repeat("0", 64), payload},
		"no bytes":                       {digest, nil},
		"no digest":                      {"", payload},
	} {
		if err := VerifyArtifact(c.digest, c.payload); !errors.Is(err, ErrArtifactDamaged) {
			t.Errorf("%s: got %v, want %v", name, err, ErrArtifactDamaged)
		}
	}
}
