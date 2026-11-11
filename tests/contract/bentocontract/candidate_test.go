// Package bentocontract_test pins the idp-bento-go candidate Fabric builds
// against and checks, from Fabric's side, the framework behaviour its events
// will rely on.
//
// Fabric is an ordinary consumer of the framework. These checks protect Fabric
// from an unnoticed framework change; they are not part of the framework's own
// release evidence.
package bentocontract_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"go.idp.xyz/idp-bento-go/eventing"
	"go.idp.xyz/idp-bento-go/testkit"
)

// The candidate under contract. The build must resolve it through the module
// path: never through a replace directive, a GitHub import path or a source
// copy. A different checksum is a different artifact.
const (
	modulePath = "go.idp.xyz/idp-bento-go"
	version    = "v0.1.0-rc.2"
	moduleSum  = "h1:W9T1KsbXwuVs3lHTNwSUcEBY0Ids00hUH7I9KuBtCEo="
	goModSum   = "h1:RFR6ylLNIIA7e4PPGVCzojYiH6DB8eHd2s5vI9XcUgY="
)

func TestTheBuildResolvesThePinnedCandidate(t *testing.T) {
	// The go command is asked, rather than go.mod read, so that a replace
	// directive or a workspace override shows up as what it is.
	output, err := exec.Command("go", "list", "-m", "-json", modulePath).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("resolve the candidate module: %v\n%s", err, exit.Stderr)
		}
		t.Fatalf("resolve the candidate module: %v", err)
	}
	var resolved struct {
		Path    string
		Version string
		Replace *struct{ Path string }
	}
	if err := json.Unmarshal(output, &resolved); err != nil {
		t.Fatalf("decode the module identity: %v\n%s", err, output)
	}
	if resolved.Replace != nil {
		t.Fatalf("the candidate must not be replaced, got %s => %s", modulePath, resolved.Replace.Path)
	}
	if resolved.Path != modulePath || resolved.Version != version {
		t.Fatalf("the build resolves %s %s, want %s %s", resolved.Path, resolved.Version, modulePath, version)
	}
	sums := recordedSums(t)
	if got := sums[modulePath+" "+version]; got != moduleSum {
		t.Fatalf("candidate checksum %q, want %s", got, moduleSum)
	}
	if got := sums[modulePath+" "+version+"/go.mod"]; got != goModSum {
		t.Fatalf("candidate go.mod checksum %q, want %s", got, goModSum)
	}
}

// recordedSums reads the checksums go.sum records, keyed "module version".
func recordedSums(t *testing.T) map[string]string {
	t.Helper()

	content, err := os.ReadFile("../../../go.sum")
	if err != nil {
		t.Fatalf("read go.sum: %v", err)
	}
	sums := make(map[string]string)
	for line := range strings.Lines(string(content)) {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			sums[fields[0]+" "+fields[1]] = fields[2]
		}
	}
	return sums
}

// Fabric's events carry references and digests, never contract bodies, are
// scoped to a product and are ordered per entity (F03 §12). These are the
// Envelope v1 rules that design leans on.
func TestEnvelopeV1KeepsTheRulesFabricEventsLeanOn(t *testing.T) {
	envelope := testkit.ValidEnvelope()
	if err := envelope.Validate(); err != nil {
		t.Fatalf("the framework's own valid envelope must validate: %v", err)
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal the envelope: %v", err)
	}
	var decoded eventing.Envelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal the envelope: %v", err)
	}
	if decoded.ID != envelope.ID || decoded.Scope != envelope.Scope || decoded.Subject != envelope.Subject ||
		decoded.PartitionKey != envelope.PartitionKey || !decoded.OccurredAt.Equal(envelope.OccurredAt) {
		t.Fatalf("identity, scope, subject, partition key and occurrence time must round trip\n got: %+v\nwant: %+v",
			decoded, envelope)
	}

	oversized := json.RawMessage(`"` + strings.Repeat("a", eventing.MaxPayloadBytes) + `"`)
	for name, mutate := range map[string]func(*eventing.Envelope){
		"missing id":             func(e *eventing.Envelope) { e.ID = "" },
		"missing scope":          func(e *eventing.Envelope) { e.Scope = "" },
		"missing partition key":  func(e *eventing.Envelope) { e.PartitionKey = "" },
		"payload over the limit": func(e *eventing.Envelope) { e.Payload = oversized },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := envelope.Clone()
			mutate(&invalid)
			if err := invalid.Validate(); !errors.Is(err, eventing.ErrInvalidEnvelope) {
				t.Fatalf("expected an invalid envelope error, got %v", err)
			}
		})
	}
}
