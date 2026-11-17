package openapi

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

const header = "openapi: 3.1.2\ninfo: {title: Shipments, version: v1}\n"

func TestCaptureKeepsOriginalBytesAndAStableReferenceClosure(t *testing.T) {
	entry := domain.SourceFile{Path: "contracts/openapi.yaml", Content: header + `
components:
  schemas:
    Status: {$ref: '../schemas/common.json#/state~1code/~0value/0'}
`}
	reference := domain.SourceFile{Path: "schemas/common.json", Content: `{"state/code":{"~value":[{"type":"string"}]}}` + "\r\n"}
	unused := domain.SourceFile{Path: "unused.yaml", Content: "type: string\n"}
	first, err := Capture(entry.Path, []domain.SourceFile{reference, unused, entry})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Capture(entry.Path, []domain.SourceFile{entry, reference})
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceDigest != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(entry.Content))) ||
		first.RefClosureDigest != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(first.Manifest()))) ||
		first.RefClosureDigest != second.RefClosureDigest {
		t.Fatalf("unexpected digests: %+v / %+v", first, second)
	}
	files := first.Files()
	if len(files) != 2 || files[0] != entry || files[1] != reference {
		t.Fatalf("captured files: %+v", files)
	}
	files[1].Content = ""
	if got := first.Files()[1]; got != reference {
		t.Fatal("a reader changed the snapshot")
	}
	reference.Content += "\n"
	changed, err := Capture(entry.Path, []domain.SourceFile{entry, reference})
	if err != nil || changed.SourceDigest != first.SourceDigest || changed.RefClosureDigest == first.RefClosureDigest {
		t.Fatalf("changing referenced bytes: %+v, %v", changed, err)
	}
}

func TestCaptureRefusesUnsafeOrAmbiguousSources(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{"remote", header + "schema: {$ref: 'https://169.254.169.254/s.json'}\n", domain.ErrInvalidSource},
		{"absolute", header + "schema: {$ref: '/s.json'}\n", domain.ErrInvalidSource},
		{"escape", header + "schema: {$ref: '%2e%2e/s.json'}\n", domain.ErrInvalidSource},
		{"missing", header + "schema: {$ref: '#/missing'}\n", domain.ErrInvalidSource},
		{"anchor", header + "schema: {$ref: '#anchor'}\n", domain.ErrInvalidSource},
		{"cycle", header + "schema: {items: {$ref: '#/schema'}}\n", domain.ErrInvalidSource},
		{"duplicate key", header + "schema: {type: string, type: number}\n", domain.ErrInvalidSource},
		{"alias", header + "schema: [&a {type: string}, *a]\n", domain.ErrInvalidSource},
		{"schema base", header + "schema: {$id: 'https://example.test/schema'}\n", domain.ErrInvalidSource},
		{"multiple documents", header + "---\nother: true\n", domain.ErrInvalidSource},
		{"unsupported version", strings.Replace(header, "3.1.2", "3.2.1", 1), domain.ErrUnsupportedSpecVersion},
		{"too big", header + "#" + strings.Repeat("x", domain.MaxSourceBytes), domain.ErrInvalidSource},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Capture("openapi.yaml", []domain.SourceFile{{Path: "openapi.yaml", Content: test.body}})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestCaptureBoundsSharedReferenceChains(t *testing.T) {
	var body strings.Builder
	body.WriteString(header)
	for i := 0; i < maxReferenceDepth+1; i++ {
		fmt.Fprintf(&body, "schema%d: {$ref: '#/schema%d'}\n", i, i+1)
	}
	fmt.Fprintf(&body, "schema%d: {type: string}\n", maxReferenceDepth+1)
	_, err := Capture("openapi.yaml", []domain.SourceFile{{Path: "openapi.yaml", Content: body.String()}})
	if !errors.Is(err, domain.ErrInvalidSource) {
		t.Fatalf("overlong reference chain returned %v", err)
	}
}
