package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// schemaReferences find, for each context, a table or a function of its
// schema named in a text: the name of the schema, a dot and a name, where the
// name of the schema does not itself follow a dot or a word, so that an event
// type such as fabric.apicatalog.asset.registered is not one.
var schemaReferences = func() map[string]*regexp.Regexp {
	found := make(map[string]*regexp.Regexp, len(contexts))
	for _, context := range contexts {
		found[context] = regexp.MustCompile(`(^|[^A-Za-z0-9_.])` + context + `\.[a-z_]`)
	}
	return found
}()

// foreignSchema is the context whose schema the text names, if it is not the
// context that owns the text; "" if it names none.
func foreignSchema(text, own string) string {
	for _, context := range contexts {
		if context != own && schemaReferences[context].MatchString(text) {
			return context
		}
	}
	return ""
}

func TestRulesFindTheSchemaOfAnotherContextInAText(t *testing.T) {
	for name, c := range map[string]struct {
		text, own, want string
	}{
		"a table of another context":         {"SELECT status FROM policyreview.check_run WHERE check_run_id = $1", "apicatalog", "policyreview"},
		"in a join":                          {"SELECT 1 FROM apicatalog.api_asset a JOIN contractregistry.contract_revision r USING (api_asset_id)", "apicatalog", "contractregistry"},
		"at the start of the text":           {"publication.slot", "contractregistry", "publication"},
		"after a bracket":                    {"INSERT INTO t (SELECT * FROM(platformoperations.audit_record))", "apicatalog", "platformoperations"},
		"a table of its own context":         {"SELECT 1 FROM apicatalog.api_asset", "apicatalog", ""},
		"a table of the framework":           {"SELECT 1 FROM bento.outbox", "apicatalog", ""},
		"an event type of another context":   {"fabric.policyreview.review.requested", "contractregistry", ""},
		"the name of a context in a message": {"the policyreview context is down", "contractregistry", ""},
		"a column that ends in the name":     {"SELECT mypolicyreview.x FROM t", "apicatalog", ""},
		"nothing":                            {"", "apicatalog", ""},
	} {
		if got := foreignSchema(c.text, c.own); got != c.want {
			t.Errorf("%s: %q of %s names the schema of %q, want %q", name, c.text, c.own, got, c.want)
		}
	}
}

// A context is the only owner of its tables (F03 §3.2), and the only one that
// reads them: what another context needs of them it asks through a port that
// is wired in cmd, and that the context that owns the tables implements. So
// no SQL in the adapters of a context names the schema of another.
func TestNoAdapterNamesTheSchemaOfAnotherContext(t *testing.T) {
	root := repositoryRoot(t)
	scanned, queries := 0, 0

	for _, context := range contexts {
		adapters := filepath.Join(root, "internal", context, "adapters")
		err := filepath.WalkDir(adapters, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			scanned++
			files := token.NewFileSet()
			parsed, err := parser.ParseFile(files, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				literal, isLiteral := node.(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(literal.Value)
				if err != nil {
					return true
				}
				if strings.Contains(text, "SELECT ") || strings.Contains(text, "INSERT INTO ") {
					queries++
				}
				if other := foreignSchema(text, context); other != "" {
					relative, _ := filepath.Rel(root, path)
					t.Errorf("%s:%d: the adapters of %s name the schema of %s in %q; ask the context that owns it through a port wired in cmd",
						filepath.ToSlash(relative), files.Position(literal.Pos()).Line, context, other, firstLine(text))
				}
				return true
			})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("scan the adapters of %s: %v", context, err)
		}
	}
	if scanned == 0 || queries == 0 {
		t.Fatalf("%d files and %d queries were scanned; this test would pass vacuously", scanned, queries)
	}
}

// firstLine is what to show of a text that may be a long query.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}
