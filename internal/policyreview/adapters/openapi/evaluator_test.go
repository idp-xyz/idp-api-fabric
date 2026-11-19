package openapi_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/openapi"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// report is what the evaluator said of a source, in the words of the tests:
// the places of the findings of each rule, as "file:line pointer", and the
// rules it could not judge. A rule is named by the three digits it ends in.
type report struct {
	findings  map[string][]string
	undecided []string
	// observations are what the evaluator handed over, as it did.
	observations []domain.Observation
}

func file(path, content string) domain.SourceFile {
	return domain.SourceFile{Path: path, Content: content}
}

// evaluate judges the files by the baseline. The first file is the
// entrypoint.
func evaluate(t *testing.T, files ...domain.SourceFile) report {
	t.Helper()
	source := domain.Source{Entrypoint: files[0].Path, Files: files, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(files[0].Content)))}
	said := report{findings: map[string][]string{}, observations: openapi.Evaluator{}.Evaluate(source, domain.Baseline().Rules())}
	for _, observation := range said.observations {
		rule := observation.RuleID[len(observation.RuleID)-3:]
		if observation.Undecided {
			said.undecided = append(said.undecided, rule)
		}
		for _, finding := range observation.Findings {
			place := finding.Path
			if finding.Line > 0 {
				place += fmt.Sprintf(":%d", finding.Line)
			}
			if finding.Pointer != "" {
				place += " " + finding.Pointer
			}
			said.findings[rule] = append(said.findings[rule], place)
		}
	}
	// Whatever the evaluator says must be something a check can record.
	draft := domain.Draft{
		ID: "check", PolicySnapshotID: "snapshot", StartedAt: time.Unix(0, 0),
		Revision:    domain.RevisionRef{ProductID: "product", WorkspaceID: "workspace", AssetID: "asset", RevisionID: "revision"},
		RequestedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "carol"},
	}
	if _, err := domain.Complete(draft, domain.Baseline(), source, openapi.Evaluator{}.Tool(), said.observations, time.Unix(1, 0)); err != nil {
		t.Fatalf("what the evaluator said cannot be recorded: %v", err)
	}
	return said
}

// expect compares a report with the findings and the undecided rules a test
// names. undecided lists rules by their three digits, separated by spaces.
func (r report) expect(t *testing.T, findings map[string][]string, undecided string) {
	t.Helper()
	if findings == nil {
		findings = map[string][]string{}
	}
	if !reflect.DeepEqual(r.findings, findings) {
		t.Errorf("the findings are\n %v\nwant\n %v", r.findings, findings)
	}
	if got := strings.Join(r.undecided, " "); got != undecided {
		t.Errorf("the undecided rules are %q, want %q", got, undecided)
	}
}

// The rules that read the documents, by their three digits.
const readers = "001 002 003 005 006 007 008"

// The rules about every operation.
const aboutOperations = "003 006 007 008"

// The tool evaluates the automated subset of the rule registry, the ten
// rules F04-GOV-001 to 010, and says nothing of any other rule: what it
// does not evaluate a check records as not run.
func TestTheToolEvaluatesTheAutomatedSubsetAndNothingElse(t *testing.T) {
	tool := openapi.Evaluator{}.Tool()
	if tool.Name != "fabric-openapi-rules" || tool.Version != openapi.Version {
		t.Fatalf("the tool is %+v, want fabric-openapi-rules at version %s", tool, openapi.Version)
	}
	said := evaluate(t, file("openapi.yaml", minimal))
	var evaluated []string
	for _, observation := range said.observations {
		evaluated = append(evaluated, observation.RuleID)
	}
	want := []string{
		"F04-GOV-001", "F04-GOV-002", "F04-GOV-003", "F04-GOV-004", "F04-GOV-005",
		"F04-GOV-006", "F04-GOV-007", "F04-GOV-008", "F04-GOV-009", "F04-GOV-010",
	}
	if !slices.Equal(evaluated, want) {
		t.Fatalf("the tool reports on %v, want %v in that order", evaluated, want)
	}
	said.expect(t, nil, "")
	for _, rule := range domain.Baseline().Rules() {
		if automated := rule.VerificationMode == "AUTOMATED_SUBSET"; automated != slices.Contains(evaluated, rule.ID) {
			t.Errorf("rule %s is of the automated subset: %t, and is evaluated: %t", rule.ID, automated, !automated)
		}
	}

	// A rule is evaluated for the evaluator it names, whatever it is called,
	// and a rule that names an evaluator the tool does not have is passed
	// over.
	renamed := domain.Rule{ID: "T-RULE-001", Evaluator: "operation_id_required"}
	unknown := domain.Rule{ID: "T-RULE-002", Evaluator: "semantic_diff"}
	source := domain.Source{Entrypoint: "openapi.yaml", Files: []domain.SourceFile{file("openapi.yaml", minimal)}}
	if got := (openapi.Evaluator{}).Evaluate(source, []domain.Rule{unknown, renamed}); len(got) != 1 || got[0].RuleID != "T-RULE-001" {
		t.Errorf("the tool reports %+v, want one observation of T-RULE-001", got)
	}
}

// minimal is a contract no rule has anything to say of.
const minimal = `openapi: 3.1.2
info: {title: Shipments, version: '1'}
security: [{oauth: []}]
paths:
  /v1/shipments:
    get:
      operationId: listShipments
      responses:
        '200': {description: The shipments}
        '401':
          description: Nobody was authenticated
          content: {application/problem+json: {schema: {type: object}}}
`

func sample(t *testing.T, path ...string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "..", ".."}, path...)...))
	if err != nil {
		t.Fatalf("read a sample of the design documents: %v", err)
	}
	return string(content)
}

// The contracts F04 §14 ships as samples are judged as F04 says they are:
// the design example breaks no rule, and each negative sample breaks the
// rule it is named for. The samples that are no document at all, or of
// another version, leave the rules about their content undecided or say so
// themselves.
func TestTheSamplesOfF04AreJudgedAsF04SaysTheyAre(t *testing.T) {
	positive := sample(t, "contracts", "examples", "openapi", "cargopai-contract-control.openapi.yaml")
	evaluate(t, file("openapi.yaml", positive)).expect(t, nil, "")

	negative := map[string]struct {
		findings  map[string][]string
		undecided string
	}{
		"unsupported_version":    {map[string][]string{"001": {"openapi.yaml:1 /openapi"}}, ""},
		"embedded_secret":        {map[string][]string{"004": {"openapi.yaml:5"}}, ""},
		"external_ref":           {map[string][]string{"005": {"openapi.yaml:14 /paths/~1v1~1example/get/responses/200/content/application~1json/schema/$ref"}}, ""},
		"missing_security":       {map[string][]string{"006": {"openapi.yaml:5 /paths/~1v1~1open/get"}}, ""},
		"duplicate_operation_id": {map[string][]string{"008": {"openapi.yaml:11 /paths/~1v1~1two/get/operationId"}}, ""},
		// A document with a repeated key says two things at once, so the
		// rules about what it says are not judged on it.
		"duplicate_yaml_key": {map[string][]string{"009": {"openapi.yaml:4 /info"}}, readers},
	}
	for name, want := range negative {
		t.Run(name, func(t *testing.T) {
			content := sample(t, "tests", "acceptance", "fixtures", "F04_negative_"+name+".yaml")
			evaluate(t, file("openapi.yaml", content)).expect(t, want.findings, want.undecided)
		})
	}
}

// The same files are judged the same by one version of the tool: a check is
// recorded once for a revision, a policy and a set of tools, and asking
// again answers what was recorded. This test holds what the tool finds in a
// fixed set of files to the version it says it is. When it fails, the tool
// finds something else in the same files than it did: raise
// openapi.Version, and record the new behaviour here with it.
func TestTheBehaviourOfTheToolIsTheBehaviourOfItsVersion(t *testing.T) {
	const recorded = "1 sha256:5259d37022100bf6d492abc9a51cf520dd3939887892829153f07b1645717a79"

	sources := [][]domain.SourceFile{
		{file("openapi.yaml", minimal)},
		{file("openapi.yaml", sample(t, "contracts", "examples", "openapi", "cargopai-contract-control.openapi.yaml"))},
		{file("api/openapi.yaml", everyRule), file("api/paths/tracking.yaml", trackingPaths), file("schemas/common.json", commonSchemas)},
		{file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: a, title: b}\n")},
		{file("openapi.json", `{"openapi":"3.1.2","info":{"title":"Shipments","version":"1"},"paths":[]}`)},
	}
	for _, name := range []string{"unsupported_version", "embedded_secret", "external_ref", "missing_security", "duplicate_operation_id", "duplicate_yaml_key"} {
		sources = append(sources, []domain.SourceFile{file("openapi.yaml", sample(t, "tests", "acceptance", "fixtures", "F04_negative_"+name+".yaml"))})
	}
	var said []string
	for index, files := range sources {
		for _, observation := range evaluate(t, files...).observations {
			said = append(said, fmt.Sprintf("%d %s undecided=%t", index, observation.RuleID, observation.Undecided))
			for _, finding := range observation.Findings {
				said = append(said, fmt.Sprintf("%d %s %s %q %d %q", index, finding.RuleID, finding.Path, finding.Pointer, finding.Line, finding.Message))
			}
		}
	}
	sort.Strings(said)
	behaviour := fmt.Sprintf("%s sha256:%x", openapi.Version, sha256.Sum256([]byte(strings.Join(said, "\n"))))
	if behaviour != recorded {
		t.Fatalf("the tool behaves as %s, and %s is recorded for it.\n"+
			"If it finds something else in the same files than before, raise openapi.Version and record %s.\n"+
			"What it says now:\n%s", behaviour, recorded, behaviour, strings.Join(said, "\n"))
	}
}

// everyRule is a contract in three files that every rule of the tool has
// something to say of, except the two that would keep it from being read.
const everyRule = `openapi: 3.1.1
info: {title: ' ', version: 1.0}
servers:
  - url: http://localhost:8080
paths:
  /v1/shipments:
    get:
      responses:
        '200': {description: The shipments}
        '404': {description: No such shipment}
        default: {$ref: '#/components/responses/Failure'}
    post:
      operationId: createShipment
      security: []
      description: Send Authorization, as in Bearer 8f2c1d9e7b6a5f4e3d2c1b0a9f8e7d6c
      requestBody: {$ref: 'https://contracts.example/bodies.yaml#/Shipment'}
      responses:
        '201': {description: The shipment}
  /v1/tracking: {$ref: 'paths/tracking.yaml#/tracking'}
webhooks:
  shipped:
    post:
      operationId: createShipment
      responses:
        '200': {description: Taken}
components:
  responses:
    Failure:
      description: A failure
      content: {application/json: {schema: {$ref: '../schemas/common.json#/Failure'}}}
`

const trackingPaths = `tracking:
  get:
    operationId: track
    security: [{oauth: [tracking:read]}]
    responses:
      '200': {description: Where the shipment is}
      '4XX': {$ref: '../openapi.yaml#/components/responses/Failure'}
      '503':
        description: Tracking is down, try https://tracking.internal/status
        content: {'application/problem+json; charset=utf-8': {}}
`

const commonSchemas = `{"Failure": {"type": "object", "example": {"client_secret": "s3cr3t-0f-the-cl1ent"}}}`

// A contract that is spread over files is judged as one: a path item that
// is a reference is followed to its operations, the findings name the file
// and the place they are in, and each rule finds what is its to find.
func TestAContractInSeveralFilesIsJudgedAsOne(t *testing.T) {
	evaluate(t, file("api/openapi.yaml", everyRule), file("api/paths/tracking.yaml", trackingPaths), file("schemas/common.json", commonSchemas)).expect(t,
		map[string][]string{
			"001": {"api/openapi.yaml:1 /openapi"},
			"002": {"api/openapi.yaml:2 /info/title", "api/openapi.yaml:2 /info/version"},
			"003": {"api/openapi.yaml:7 /paths/~1v1~1shipments/get"},
			"004": {"api/openapi.yaml:15", "schemas/common.json:1"},
			"005": {"api/openapi.yaml:16 /paths/~1v1~1shipments/post/requestBody/$ref"},
			"006": {"api/openapi.yaml:7 /paths/~1v1~1shipments/get", "api/openapi.yaml:14 /paths/~1v1~1shipments/post/security"},
			"007": {
				"api/openapi.yaml:10 /paths/~1v1~1shipments/get/responses/404",
				"api/openapi.yaml:11 /paths/~1v1~1shipments/get/responses/default",
				"api/paths/tracking.yaml:7 /tracking/get/responses/4XX",
			},
			"008": {"api/openapi.yaml:23 /webhooks/shipped/post/operationId"},
			"010": {"api/openapi.yaml:4", "api/paths/tracking.yaml:9"},
		}, "")
}
