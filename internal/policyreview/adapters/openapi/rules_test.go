package openapi_test

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// head opens a contract no rule has anything to say of so far. It is three
// lines, and states a security requirement for the whole document.
const head = "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\nsecurity: [{oauth: []}]\n"

// of returns the places of the findings of one rule.
func (r report) of(rule string) []string { return r.findings[rule] }

func (r report) decided(rule string) bool { return !slices.Contains(r.undecided, rule) }

// messages returns what the findings of one rule say.
func (r report) messages(rule string) []string {
	var said []string
	for _, observation := range r.observations {
		if strings.HasSuffix(observation.RuleID, rule) {
			for _, finding := range observation.Findings {
				said = append(said, finding.Message)
			}
		}
	}
	return said
}

// F04-GOV-001: the entrypoint declares OpenAPI 3.1.2 as a text, and no
// other version passes for it. A version another file declares is not
// asked for.
func TestTheEntrypointDeclaresTheOneSupportedVersion(t *testing.T) {
	const rest = "info: {title: Shipments, version: '1'}\n"
	cases := map[string]struct {
		entrypoint string
		want       []string
	}{
		"3.1.2":                       {"openapi: 3.1.2\n" + rest, nil},
		"3.1.2 in quotes":             {"openapi: '3.1.2'\n" + rest, nil},
		"3.1.2 behind other members":  {rest + "openapi: 3.1.2\n", nil},
		"the patch before":            {"openapi: 3.1.1\n" + rest, []string{"openapi.yaml:1 /openapi"}},
		"the next minor":              {"openapi: 3.2.1\n" + rest, []string{"openapi.yaml:1 /openapi"}},
		"the major before":            {"openapi: 3.0.3\n" + rest, []string{"openapi.yaml:1 /openapi"}},
		"a number":                    {"openapi: 3.1\n" + rest, []string{"openapi.yaml:1 /openapi"}},
		"a list":                      {"openapi: [3.1.2]\n" + rest, []string{"openapi.yaml:1 /openapi"}},
		"3.1.2 and a space":           {"openapi: '3.1.2 '\n" + rest, []string{"openapi.yaml:1 /openapi"}},
		"a prefix":                    {"openapi: v3.1.2\n" + rest, []string{"openapi.yaml:1 /openapi"}},
		"a Swagger document":          {"swagger: '2.0'\n" + rest, []string{"openapi.yaml:1"}},
		"no version, behind the info": {rest + "paths: {}\n", []string{"openapi.yaml:1"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", c.entrypoint), file("schemas/other.yaml", "openapi: 3.0.3\n"))
			if got := said.of("001"); !reflect.DeepEqual(got, c.want) || !said.decided("001") {
				t.Fatalf("the findings are %v, want %v", got, c.want)
			}
		})
	}
	json := evaluate(t, file("openapi.json", `{"info": {"title": "Shipments", "version": "1"}, "openapi": "3.1.2"}`))
	if got := json.of("001"); got != nil {
		t.Errorf("an entrypoint in JSON: %v, want no finding", got)
	}
}

// F04-GOV-002: the document names the API and its version, each as a text
// that is not empty.
func TestTheDocumentNamesTheAPIAndItsVersion(t *testing.T) {
	cases := map[string]struct {
		info string
		want []string
	}{
		"a title and a version":        {"info: {title: Shipments, version: '1.0.0'}", nil},
		"a version that reads as text": {"info: {title: Shipments, version: 1.0.0}", nil},
		"more than is asked":           {"info: {title: Shipments, version: v1, description: x}", nil},
		"no info":                      {"paths: {}", []string{"openapi.yaml:1"}},
		"an info that is a text":       {"info: Shipments", []string{"openapi.yaml:2 /info"}},
		"an info that is a list":       {"info: [Shipments, '1']", []string{"openapi.yaml:2 /info"}},
		"no title":                     {"info: {version: '1'}", []string{"openapi.yaml:2 /info"}},
		"no version":                   {"info: {title: Shipments}", []string{"openapi.yaml:2 /info"}},
		"neither":                      {"info: {}", []string{"openapi.yaml:2 /info", "openapi.yaml:2 /info"}},
		"an empty title":               {"info: {title: '', version: '1'}", []string{"openapi.yaml:2 /info/title"}},
		"a title of white space":       {"info: {title: \"\\t \", version: '1'}", []string{"openapi.yaml:2 /info/title"}},
		"a title that is a number":     {"info: {title: 42, version: '1'}", []string{"openapi.yaml:2 /info/title"}},
		"a version that is a number":   {"info: {title: Shipments, version: 1.0}", []string{"openapi.yaml:2 /info/version"}},
		"a version that is null":       {"info: {title: Shipments, version: null}", []string{"openapi.yaml:2 /info/version"}},
		"a title that is a mapping":    {"info: {title: {en: Shipments}, version: '1'}", []string{"openapi.yaml:2 /info/title"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", "openapi: 3.1.2\n"+c.info+"\n"))
			if got := said.of("002"); !reflect.DeepEqual(got, c.want) || !said.decided("002") {
				t.Fatalf("the findings are %v, want %v", got, c.want)
			}
		})
	}
}

// F04-GOV-003: every operation under paths has an operationId that is a
// text and not empty. What a path item holds besides operations is not one,
// and an operation the API calls, under webhooks, is not asked for one.
func TestEveryOperationUnderPathsHasAnOperationID(t *testing.T) {
	const paths = head + `paths:
  /a:
    get: {operationId: a, responses: {}}
    put: {responses: {}}
    post: {operationId: '', responses: {}}
    delete: {operationId: ' ', responses: {}}
    patch: {operationId: 7, responses: {}}
    head: {operationId: [a], responses: {}}
    options: {operationId: 运单查询, responses: {}}
    trace:
      responses: {}
    parameters: []
    x-internal: {}
    GET: {}
  x-paths-extension: true
  /b: {$ref: 'paths/b.yaml'}
webhooks:
  shipped:
    post: {responses: {}}
`
	const referenced = "summary: b\nget:\n  responses: {}\nput: {operationId: b, responses: {}}\n"
	said := evaluate(t, file("openapi.yaml", paths), file("paths/b.yaml", referenced))
	want := []string{
		"openapi.yaml:7 /paths/~1a/put",
		"openapi.yaml:8 /paths/~1a/post/operationId",
		"openapi.yaml:9 /paths/~1a/delete/operationId",
		"openapi.yaml:10 /paths/~1a/patch/operationId",
		"openapi.yaml:11 /paths/~1a/head/operationId",
		"openapi.yaml:13 /paths/~1a/trace",
		// The operation of a path item that is a reference is found in the
		// file it is written in.
		"paths/b.yaml:2 /get",
	}
	if got := said.of("003"); !reflect.DeepEqual(got, want) || !said.decided("003") {
		t.Fatalf("the findings are\n %v\nwant\n %v", got, want)
	}
}

// F04-GOV-008: no two operations share an operationId, under paths and
// under webhooks alike. Identifiers are compared as they are written, and an
// operation that is reached by two paths is two operations.
func TestNoTwoOperationsShareAnOperationID(t *testing.T) {
	const paths = head + `paths:
  /a:
    get: {operationId: list, responses: {}}
    put: {operationId: List, responses: {}}
    post: {operationId: list, responses: {}}
    delete: {operationId: '', responses: {}}
    patch: {operationId: '', responses: {}}
  /b: {$ref: '#/components/pathItems/Shared'}
  /c: {$ref: '#/components/pathItems/Shared'}
webhooks:
  shipped:
    post: {operationId: List, responses: {}}
components:
  pathItems:
    Shared:
      get: {operationId: shared, responses: {}}
`
	said := evaluate(t, file("openapi.yaml", paths))
	want := []string{
		"openapi.yaml:8 /paths/~1a/post/operationId",
		"openapi.yaml:19 /components/pathItems/Shared/get/operationId",
		"openapi.yaml:15 /webhooks/shipped/post/operationId",
	}
	if got := said.of("008"); !reflect.DeepEqual(got, want) || !said.decided("008") {
		t.Fatalf("the findings are\n %v\nwant\n %v", got, want)
	}
	// The finding says where the identifier was used first, and not which
	// identifier it is.
	messages := said.messages("008")
	for i, first := range []string{"line 6 of openapi.yaml", "line 19 of openapi.yaml", "line 7 of openapi.yaml"} {
		if !strings.Contains(messages[i], first) || strings.Contains(strings.ToLower(messages[i]), "list") || strings.Contains(messages[i], "shared") {
			t.Errorf("finding %d says %q, want it to name %s and no identifier", i, messages[i], first)
		}
	}
}

// F04-GOV-006: a security requirement applies to every operation under
// paths: its own, or the one of the document when it states none. A
// requirement that is an empty list applies none, also when it overrides
// the one of the document.
func TestASecurityRequirementAppliesToEveryOperationUnderPaths(t *testing.T) {
	const operations = `paths:
  /a:
    get: {operationId: a, responses: {}}
    put: {operationId: b, security: [], responses: {}}
    post: {operationId: c, security: [{}], responses: {}}
    delete: {operationId: d, security: [{key: []}, {oauth: [read]}], responses: {}}
    patch: {operationId: e, security: {oauth: []}, responses: {}}
    head: {operationId: f, security: null, responses: {}}
webhooks:
  shipped:
    post: {operationId: g, security: [], responses: {}}
`
	const opening = "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\n"
	own := []string{"openapi.yaml:7 /paths/~1a/put/security", "openapi.yaml:10 /paths/~1a/patch/security", "openapi.yaml:11 /paths/~1a/head/security"}
	inherited := "openapi.yaml:6 /paths/~1a/get"
	cases := map[string]struct {
		document string
		want     []string
	}{
		"a requirement for the whole document":    {"security: [{oauth: []}]", own},
		"none for the whole document":             {"x-note: none", append([]string{inherited}, own...)},
		"an empty one for the whole document":     {"security: []", append([]string{inherited}, own...)},
		"one that is no list for the document":    {"security: {oauth: []}", append([]string{inherited}, own...)},
		"an anonymous one for the whole document": {"security: [{}]", own},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", opening+c.document+"\n"+operations))
			if got := said.of("006"); !reflect.DeepEqual(got, c.want) || !said.decided("006") {
				t.Fatalf("the findings are\n %v\nwant\n %v", got, c.want)
			}
		})
	}
}

// F04-GOV-007: an error response offers a problem document. An error
// response is one under a 4xx or 5xx status, a range of them, or default; a
// response that is a reference is followed to what it stands for.
func TestAnErrorResponseOffersAProblemDocument(t *testing.T) {
	const paths = head + `paths:
  /a:
    get:
      operationId: a
      responses:
        '200': {description: ok}
        '400': {description: x, content: {application/problem+json: {}}}
        '401': {description: x}
        '403': {description: x, content: {application/json: {}}}
        '404': {$ref: '#/components/responses/Problem'}
        '409': {$ref: '#/components/responses/Plain'}
        '4XX': {description: x, content: {'Application/Problem+JSON; charset=utf-8': {}, application/json: {}}}
        '5xx': {description: x}
        default: {description: x, content: {}}
        '600': {description: x}
        '2XX': {description: x}
        '40': {description: x}
        x-note: {description: x}
        '500': {$ref: 'responses.yaml#/Twice'}
webhooks:
  shipped:
    post: {operationId: g, responses: {'500': {description: x}}}
components:
  responses:
    Problem: {description: p, content: {application/problem+json: {schema: {type: object}}}}
    Plain: {description: p, content: {text/plain: {}}}
`
	const responses = "Twice: {$ref: '#/Once'}\nOnce: {$ref: 'openapi.yaml#/components/responses/Plain'}\n"
	said := evaluate(t, file("openapi.yaml", paths), file("responses.yaml", responses))
	want := []string{
		"openapi.yaml:11 /paths/~1a/get/responses/401",
		"openapi.yaml:12 /paths/~1a/get/responses/403",
		"openapi.yaml:14 /paths/~1a/get/responses/409",
		"openapi.yaml:16 /paths/~1a/get/responses/5xx",
		"openapi.yaml:17 /paths/~1a/get/responses/default",
		"openapi.yaml:22 /paths/~1a/get/responses/500",
	}
	if got := said.of("007"); !reflect.DeepEqual(got, want) || !said.decided("007") {
		t.Fatalf("the findings are\n %v\nwant\n %v", got, want)
	}

	// A response the rule cannot read leaves it undecided: what it found
	// elsewhere stands, and the response it could not read was not judged.
	unreadable := map[string]string{
		"responses that are a list":             "responses: []",
		"a response that is a text":             "responses: {'404': none, '401': {description: x}}",
		"a response that refers to nothing":     "responses: {'404': {$ref: '#/components/responses/Missing'}, '401': {description: x}}",
		"a response that refers to itself":      "responses: {'404': {$ref: '#/paths/~1a/get/responses/404'}, '401': {description: x}}",
		"a response whose content is a list":    "responses: {'404': {description: x, content: []}, '401': {description: x}}",
		"a response that refers to a text":      "responses: {'404': {$ref: '#/info/title'}, '401': {description: x}}",
		"a response whose reference is no text": "responses: {'404': {$ref: 7}, '401': {description: x}}",
	}
	for name, written := range unreadable {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", head+"paths:\n  /a:\n    get:\n      operationId: a\n      "+written+"\n"))
			found := said.of("007")
			if said.decided("007") || (strings.Contains(written, "401") != (len(found) == 1)) || !said.decided("003") {
				t.Fatalf("the rule is decided: %t, with the findings %v; want it undecided, with the finding of the response it could read", said.decided("007"), found)
			}
		})
	}
}

// F04-GOV-005: no reference leads outside the files of the source. A
// reference that stays inside them is not this rule's business, whether it
// names something or not.
func TestNoReferenceLeadsOutsideTheFilesOfTheSource(t *testing.T) {
	const entrypoint = head + `components:
  schemas:
    A: {type: object}
    Local: {$ref: '#/components/schemas/A'}
    File: {$ref: 'schemas/common.yaml#/Money'}
    Whole: {$ref: 'schemas/common.yaml'}
    Dot: {$ref: './schemas/common.yaml#/Money'}
    Dangling: {$ref: '#/components/schemas/Missing'}
    NoPointer: {$ref: '#components'}
    Web: {$ref: 'https://contracts.example/common.yaml#/Money'}
    Plain: {$ref: 'http://contracts.example/common.yaml'}
    Host: {$ref: '//contracts.example/common.yaml'}
    Disk: {$ref: 'file:///etc/passwd'}
    Absolute: {$ref: '/etc/passwd'}
    Above: {$ref: '../outside.yaml'}
    Sneaky: {$ref: 'schemas/../../outside.yaml'}
    Backslash: {$ref: 'schemas\common.yaml'}
    Missing: {$ref: 'schemas/missing.yaml'}
    Query: {$ref: 'schemas/common.yaml?v=2'}
    Urn: {$ref: 'urn:contracts:money'}
    Empty: {$ref: ''}
    NotText: {$ref: 7}
    WithProperty: {type: object, properties: {$ref: {type: string}}}
`
	const common = "Money: {type: object}\nBack: {$ref: '../openapi.yaml#/components/schemas/A'}\nUp: {$ref: '../../outside.yaml'}\nBeside: {$ref: 'money.yaml'}\n"
	said := evaluate(t, file("openapi.yaml", entrypoint), file("schemas/common.yaml", common))
	var want []string
	for line, name := range []string{"Web", "Plain", "Host", "Disk", "Absolute", "Above", "Sneaky", "Backslash", "Missing", "Query", "Urn"} {
		want = append(want, "openapi.yaml:"+strconv.Itoa(13+line)+" /components/schemas/"+name+"/$ref")
	}
	want = append(want, "schemas/common.yaml:3 /Up/$ref", "schemas/common.yaml:4 /Beside/$ref")
	if got := said.of("005"); !reflect.DeepEqual(got, want) || !said.decided("005") {
		t.Fatalf("the findings are\n %v\nwant\n %v", got, want)
	}
}

// The rules about every operation are judged on every operation or not at
// all. Where something between the entrypoint and an operation is not what
// OpenAPI says it is, an operation may be out of sight, and a rule that did
// not see it has not judged it: the rule is undecided, and what it found in
// the operations it did see stands.
func TestARuleAboutEveryOperationIsUndecidedWhereOperationsMayBeOutOfSight(t *testing.T) {
	const seen = "  /seen:\n    get: {responses: {'500': {description: x}}}\n"
	unclear := map[string]string{
		"paths that are a list":                 "paths: []\n",
		"paths that are a text":                 "paths: none\n",
		"a path item that is a list":            "paths:\n  /a: []\n" + seen,
		"a path item that is null":              "paths:\n  /a:\n" + seen,
		"an operation that is a list":           "paths:\n  /a: {get: []}\n" + seen,
		"an operation that is a text":           "paths:\n  /a: {post: later}\n" + seen,
		"a path item that refers to nothing":    "paths:\n  /a: {$ref: '#/components/pathItems/Missing'}\n" + seen,
		"a path item that refers outside":       "paths:\n  /a: {$ref: 'https://contracts.example/paths.yaml'}\n" + seen,
		"a path item that refers to a text":     "paths:\n  /a: {$ref: '#/info/title'}\n" + seen,
		"a path item whose reference is a list": "paths:\n  /a: {$ref: [a]}\n" + seen,
		"path items that refer to each other":   "paths:\n  /a: {$ref: '#/paths/~1b'}\n  /b: {$ref: '#/paths/~1a'}\n" + seen,
		"a path item that refers to itself":     "paths:\n  /a: {$ref: '#/paths/~1a'}\n" + seen,
		"webhooks that are a list":              "paths:\n" + seen + "webhooks: []\n",
		"a webhook that is a text":              "paths:\n" + seen + "webhooks: {shipped: later}\n",
	}
	for name, written := range unclear {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\n"+written))
			if got := strings.Join(said.undecided, " "); got != aboutOperations {
				t.Fatalf("the undecided rules are %q, want %q", got, aboutOperations)
			}
			// The operation that is in sight is judged all the same.
			wantFindings := 0
			if strings.Contains(written, "/seen") {
				wantFindings = 1
			}
			for _, rule := range []string{"003", "006", "007"} {
				if got := said.of(rule); len(got) != wantFindings {
					t.Errorf("rule %s finds %v, want %d findings in the operation it saw", rule, got, wantFindings)
				}
			}
		})
	}

	clear := map[string]string{
		"no paths":                               "components: {}\n",
		"empty paths":                            "paths: {}\n",
		"a path item without operations":         "paths:\n  /a: {summary: nothing yet, parameters: []}\n",
		"an extension under paths":               "paths:\n  x-generated: true\n  x-owners: [carol]\n",
		"a path item that is a reference to one": "paths:\n  /a: {$ref: '#/components/pathItems/A'}\ncomponents:\n  pathItems:\n    A: {summary: nothing yet}\n",
		"a reference beside operations":          "paths:\n  /a:\n    $ref: '#/components/pathItems/A'\n    get: {operationId: a, security: [{k: []}], responses: {'200': {description: OK}}}\ncomponents:\n  pathItems:\n    A: {put: {operationId: b, security: [{k: []}], responses: {'200': {description: OK}}}}\n",
	}
	for name, written := range clear {
		t.Run(name, func(t *testing.T) {
			evaluate(t, file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\n"+written)).expect(t, nil, "")
		})
	}
	// Both operations of the last one are seen: the referenced one and the
	// one beside the reference.
	both := evaluate(t, file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\npaths:\n  /a:\n    $ref: '#/components/pathItems/A'\n    get: {responses: {}}\n"+
		"components:\n  pathItems:\n    A: {put: {responses: {}}}\n"))
	if got, want := both.of("003"), []string{"openapi.yaml:9 /components/pathItems/A/put", "openapi.yaml:6 /paths/~1a/get"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a reference beside an operation: %v, want %v", got, want)
	}
}

// F04-GOV-009: every file is one document that reads one way. A file that
// is not leaves the rules about what the documents say undecided: they are
// not judged on a source that is half read. The scans of the bytes are.
func TestEveryFileIsOneDocumentThatReadsOneWay(t *testing.T) {
	const sound = "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\n"
	cases := map[string]struct {
		files []domain.SourceFile
		want  []string
	}{
		// What keeps a file from being parsed is a finding of the file: the
		// line a parser gives up at is often not the line of the mistake.
		"a flow mapping that is not closed":   {[]domain.SourceFile{file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: Shipments\n")}, []string{"openapi.yaml"}},
		"an indentation that does not add up": {[]domain.SourceFile{file("openapi.yaml", "openapi: 3.1.2\ninfo:\n  title: Shipments\n version: '1'\n")}, []string{"openapi.yaml"}},
		"an empty file":                       {[]domain.SourceFile{file("openapi.yaml", "")}, []string{"openapi.yaml"}},
		"a file of comments":                  {[]domain.SourceFile{file("openapi.yaml", "# nothing yet\n")}, []string{"openapi.yaml"}},
		"two documents":                       {[]domain.SourceFile{file("openapi.yaml", sound+"---\n"+sound)}, []string{"openapi.yaml"}},
		"bytes that are not UTF-8":            {[]domain.SourceFile{file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: \"\xff\xfe\", version: '1'}\n")}, []string{"openapi.yaml"}},
		"an alias of nothing":                 {[]domain.SourceFile{file("openapi.yaml", sound+"x-c: *nowhere\n")}, []string{"openapi.yaml"}},
		"a key that is repeated":              {[]domain.SourceFile{file("openapi.yaml", sound+"paths:\n  /a: {}\n  /b: {}\n  /a: {}\n")}, []string{"openapi.yaml:6 /paths"}},
		"a key repeated in a flow mapping":    {[]domain.SourceFile{file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: a, version: '1', title: b}\n")}, []string{"openapi.yaml:2 /info"}},
		"a status that is a number":           {[]domain.SourceFile{file("openapi.yaml", sound+"paths:\n  /a:\n    get:\n      responses:\n        200: {description: x}\n")}, []string{"openapi.yaml:7 /paths/~1a/get/responses"}},
		"a key that is a boolean":             {[]domain.SourceFile{file("openapi.yaml", sound+"x-flags:\n  true: yes\n")}, []string{"openapi.yaml:4 /x-flags"}},
		"a key that is null":                  {[]domain.SourceFile{file("openapi.yaml", sound+"x-flags:\n  ~: nothing\n")}, []string{"openapi.yaml:4 /x-flags"}},
		"a key that is a list":                {[]domain.SourceFile{file("openapi.yaml", sound+"x-flags:\n  ? [a, b]\n  : c\n")}, []string{"openapi.yaml:4 /x-flags"}},
		"an alias":                            {[]domain.SourceFile{file("openapi.yaml", sound+"x-a: &shared {b: 1}\nx-c: *shared\n")}, []string{"openapi.yaml:4 /x-c"}},
		"a merge key":                         {[]domain.SourceFile{file("openapi.yaml", sound+"x-a: &shared {b: 1}\nx-c:\n  <<: *shared\n")}, []string{"openapi.yaml:5 /x-c"}},
		"a tag of the application":            {[]domain.SourceFile{file("openapi.yaml", sound+"x-a: !secret abc\n")}, []string{"openapi.yaml:3 /x-a"}},
		"a binary value":                      {[]domain.SourceFile{file("openapi.yaml", sound+"x-a: !!binary aGVsbG8=\n")}, []string{"openapi.yaml:3 /x-a"}},
		"JSON that is not valid":              {[]domain.SourceFile{file("openapi.json", `{"openapi": "3.1.2", "info": {"title": "a", "version": "1"},}`)}, []string{"openapi.json"}},
		"JSON with a repeated member":         {[]domain.SourceFile{file("openapi.json", `{"openapi": "3.1.2", "info": {"title": "a", "version": "1"}, "openapi": "3.0.3"}`)}, []string{"openapi.json:1"}},
		"an entrypoint that is a list":        {[]domain.SourceFile{file("openapi.yaml", "- openapi: 3.1.2\n")}, []string{"openapi.yaml:1"}},
		"an entrypoint that is a text":        {[]domain.SourceFile{file("openapi.yaml", "openapi 3.1.2\n")}, []string{"openapi.yaml:1"}},
		"a referenced file that is malformed": {[]domain.SourceFile{file("openapi.yaml", sound), file("schemas/a.yaml", "type: [object\n")}, []string{"schemas/a.yaml"}},
		// Everything that is wrong with a file is found, in every file.
		"two files that are malformed": {[]domain.SourceFile{file("openapi.yaml", sound+"x: {a: 1, a: 2}\n"), file("schemas/a.yaml", "a: &c 1\na: 2\nb: *c\n")},
			[]string{"openapi.yaml:3 /x", "schemas/a.yaml:2", "schemas/a.yaml:3 /b"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, c.files...)
			if got := said.of("009"); !reflect.DeepEqual(got, c.want) || !said.decided("009") {
				t.Fatalf("the findings are %v, want %v", got, c.want)
			}
			if got := strings.Join(said.undecided, " "); got != readers {
				t.Errorf("the undecided rules are %q, want the rules that read the documents %q", got, readers)
			}
			for rule, found := range said.findings {
				if rule != "009" {
					t.Errorf("rule %s finds %v in a source that was not read", rule, found)
				}
			}
		})
	}

	const valid = sound + "paths: {}\n"
	wellFormed := map[string][]domain.SourceFile{
		"a document":                     {file("openapi.yaml", valid)},
		"a document behind a marker":     {file("openapi.yaml", "---\n"+valid)},
		"a document with an anchor":      {file("openapi.yaml", valid+"x-a: &unused {b: 1}\n")},
		"values of every plain kind":     {file("openapi.yaml", valid+"x-a: [1, 1.5, true, null, 2026-11-18, text, {}]\n")},
		"JSON":                           {file("openapi.json", `{"openapi": "3.1.2", "info": {"title": "a", "version": "1"}, "paths": {}}`)},
		"a referenced file of one value": {file("openapi.yaml", valid), file("schemas/any.yaml", "true\n")},
		"keys nobody else would write":   {file("openapi.yaml", valid+"x-a: {'': 1, ' ': 2, 'a/b': 3, 'a~b': 4, '200': 5, 'true': 6}\n")},
	}
	for name, files := range wellFormed {
		t.Run(name, func(t *testing.T) {
			evaluate(t, files...).expect(t, nil, "")
		})
	}

	// The scans read the bytes, so they find what is in a file that is no
	// document.
	scanned := evaluate(t, file("openapi.yaml", "openapi: 3.1.2\ninfo: {title: a, title: b}\n# see http://localhost:8080\n"))
	if got := scanned.of("010"); !reflect.DeepEqual(got, []string{"openapi.yaml:3"}) || !scanned.decided("010") || !scanned.decided("004") {
		t.Errorf("the scan of a file that is no document: %v, want its finding", got)
	}
}

// What is more than the tool reads is not judged, and not taken for sound
// either: the rules about the documents are undecided, the one about their
// form included.
func TestASourceBeyondTheBoundsOfTheToolIsUndecided(t *testing.T) {
	const sound = "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\npaths: {}\n"
	nested := func(levels int) string {
		var written strings.Builder
		written.WriteString(sound + "x-deep:\n")
		for level := 1; level <= levels; level++ {
			written.WriteString(strings.Repeat(" ", level) + "a:\n")
		}
		written.WriteString(strings.Repeat(" ", levels+1) + "b: 1\n")
		return written.String()
	}
	many := func(items int) string {
		return sound + "x-many: [" + strings.TrimSuffix(strings.Repeat("1,", items), ",") + "]\n"
	}
	for name, c := range map[string]struct {
		files  []domain.SourceFile
		within bool
	}{
		"nesting at the bound":     {[]domain.SourceFile{file("openapi.yaml", nested(62))}, true},
		"nesting beyond the bound": {[]domain.SourceFile{file("openapi.yaml", nested(63))}, false},
		// The document holds seven values besides the items of the list.
		"as many values as are read":    {[]domain.SourceFile{file("openapi.yaml", many(8192-7))}, true},
		"one value more":                {[]domain.SourceFile{file("openapi.yaml", many(8192-6))}, false},
		"values counted over the files": {[]domain.SourceFile{file("openapi.yaml", many(5000)), file("b.yaml", many(4000))}, false},
	} {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, c.files...)
			if c.within {
				said.expect(t, nil, "")
				return
			}
			said.expect(t, nil, "001 002 003 005 006 007 008 009 011")
		})
	}
}

// A finding names its place by a pointer when the pointer can be kept, and
// by its line alone when it cannot: a key may hold anything, and what a tool
// says of a hostile document must still be something a check can record.
func TestAPlaceThatCannotBePointedAtIsKnownByItsLine(t *testing.T) {
	// A key by itself is shorter than a pointer may be, since YAML bounds a
	// key on one line; two of them, one under the other, are longer.
	half := strings.Repeat("k", domain.MaxFindingPointer/2)
	for name, c := range map[string]struct {
		key  string
		want string
	}{
		"a key with a slash and a tilde":  {`'a/b~c'`, "/x-keys/a~1b~0c/$ref"},
		"a key in another script":         {`运单`, "/x-keys/运单/$ref"},
		"an empty key":                    {`''`, "/x-keys//$ref"},
		"a key as long as half a pointer": {half, "/x-keys/" + half + "/$ref"},
		"a key with a line break":         {`"a\nb"`, ""},
		"a key with a NUL":                {`"a\0b"`, ""},
		"a key with an escape character":  {`"a\eb"`, ""},
		"keys longer than a pointer":      {half + ": {" + half, ""},
	} {
		t.Run(name, func(t *testing.T) {
			// The reference is on line 5: behind the three lines of the
			// head and the key it is under.
			closing := strings.Repeat("}", strings.Count(c.key, "{"))
			said := evaluate(t, file("openapi.yaml", head+"x-keys:\n  "+c.key+": {$ref: 'https://contracts.example/a.yaml'}"+closing+"\n"))
			want := strings.TrimSpace("openapi.yaml:5 " + c.want)
			if got := said.of("005"); !reflect.DeepEqual(got, []string{want}) {
				t.Fatalf("the finding is at %q, want %q", got, want)
			}
		})
	}
}
