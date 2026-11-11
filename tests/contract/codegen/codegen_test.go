// Package codegen_test keeps the generated Go code of the repository tied to
// the contracts it comes from: the transport of the management API, and the
// packages under tests/contract/oas31 that record what the pinned generator
// makes of OpenAPI 3.1.2 (F07 §4.2, ENG-17).
//
// Those packages are generated from sample contracts with the options the
// management API is generated with. Their tests state, feature by feature,
// what the generated code enforces by itself and what only holds behind a
// request validator, so a generator upgrade that changes any of it is seen
// there before it reaches the API.
//
// Regenerate with `make generate`.
package codegen_test

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the generated files instead of comparing them")

// The generator and its options. The version is pinned by tools/go.mod.
const (
	toolsModule   = "tools/go.mod"
	generatorName = "oapi-codegen"
	baseConfig    = "api/oapi-codegen.yaml"
)

// A target is one generated file. Paths are relative to the repository root.
type target struct {
	contract string
	pkg      string
	output   string
}

// The management API, as its configuration file names it.
var managementAPI = target{
	contract: "api/openapi.yaml",
	pkg:      "http",
	output:   "internal/transport/http/api.gen.go",
}

// The sample contracts the generator handles. Each has a package of its own
// under tests/contract/oas31.
var samples = []string{"nullability", "composition", "anyof", "httpsemantics", "security"}

// The sample contracts the generator refuses, with a fragment of its reason.
// A sample leaves this list the day the generator learns the feature; the
// test fails then, and the feature gets a package and tests like the others.
var refused = map[string]string{
	// type: [string, integer]: a union of two types that are not null.
	"uniontypes": "unhandled Schema type",
}

// Contracts the generator must accept although their code is not kept. The
// F04 design example is the documented shape of the operations the management
// API will grow, so the generator has to be able to take it.
var accepted = []string{
	"contracts/examples/openapi/cargopai-contract-control.openapi.yaml",
}

func sampleTarget(name string) target {
	return target{
		contract: "tests/contract/oas31/samples/" + name + ".yaml",
		pkg:      name,
		output:   "tests/contract/oas31/" + name + "/api.gen.go",
	}
}

func TestGeneratedCodeIsWhatTheContractsProduce(t *testing.T) {
	root := repositoryRoot(t)
	config := readConfig(t, root)

	if config.pkg != managementAPI.pkg || config.output != managementAPI.output {
		t.Fatalf("%s generates package %q into %q, want package %q into %q",
			baseConfig, config.pkg, config.output, managementAPI.pkg, managementAPI.output)
	}

	targets := []target{managementAPI}
	for _, name := range samples {
		targets = append(targets, sampleTarget(name))
	}
	for _, target := range targets {
		t.Run(target.output, func(t *testing.T) {
			generated, output, err := generate(t, root, config, target)
			if err != nil {
				t.Fatalf("generate from %s: %v\n%s", target.contract, err, output)
			}

			committed := filepath.Join(root, filepath.FromSlash(target.output))
			if *update {
				if err := os.MkdirAll(filepath.Dir(committed), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(committed, generated, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			current, err := os.ReadFile(committed)
			if err != nil {
				t.Fatalf("read the committed file: %v (run `make generate`)", err)
			}
			if !bytes.Equal(current, generated) {
				t.Fatalf("%s is not what %s produces from %s: it was edited by hand, or the contract, "+
					"the options or the generator changed without regenerating (run `make generate`)",
					target.output, generatorName, target.contract)
			}
		})
	}
}

func TestTheGeneratorAcceptsTheDesignExamples(t *testing.T) {
	root := repositoryRoot(t)
	config := readConfig(t, root)

	for _, contract := range accepted {
		t.Run(contract, func(t *testing.T) {
			generated, output, err := generate(t, root, config, target{contract: contract, pkg: "example"})
			if err != nil {
				t.Fatalf("generate from %s: %v\n%s", contract, err, output)
			}
			if !bytes.Contains(generated, []byte("type StrictServerInterface interface")) {
				t.Fatalf("the code generated from %s declares no operations", contract)
			}
		})
	}
}

func TestTheGeneratorRefusesWhatItCannotRepresent(t *testing.T) {
	root := repositoryRoot(t)
	config := readConfig(t, root)

	for name, reason := range refused {
		t.Run(name, func(t *testing.T) {
			_, output, err := generate(t, root, config, sampleTarget(name))
			if err == nil {
				t.Fatalf("the generator now accepts samples/%s.yaml: give the sample a package and tests, "+
					"and take it off the refused list", name)
			}
			if !strings.Contains(output, reason) {
				t.Fatalf("the generator refuses samples/%s.yaml for another reason than %q:\n%s", name, reason, output)
			}
		})
	}
}

// config is the generator configuration with the two lines that differ from
// one target to the next taken out.
type config struct {
	pkg     string
	output  string
	options string
}

func readConfig(t *testing.T, root string) config {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(baseConfig)))
	if err != nil {
		t.Fatalf("read the generator configuration: %v", err)
	}
	var parsed config
	var options strings.Builder
	for line := range strings.Lines(string(content)) {
		if value, found := strings.CutPrefix(line, "package:"); found {
			parsed.pkg = strings.TrimSpace(value)
		} else if value, found := strings.CutPrefix(line, "output:"); found {
			parsed.output = strings.TrimSpace(value)
		} else {
			options.WriteString(line)
		}
	}
	parsed.options = options.String()
	return parsed
}

// generate runs the pinned generator on the target's contract with the
// options of the management API, and returns the file it writes. On failure
// it returns what the generator printed.
func generate(t *testing.T, root string, config config, target target) (generated []byte, output string, err error) {
	t.Helper()

	scratch := t.TempDir()
	written := filepath.Join(scratch, "api.gen.go")
	derived := filepath.Join(scratch, "oapi-codegen.yaml")
	content := "package: " + target.pkg + "\noutput: " + written + "\n" + config.options
	if err := os.WriteFile(derived, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("go", "tool", "-modfile="+toolsModule, generatorName, "-config", derived, target.contract)
	command.Dir = root
	printed, err := command.CombinedOutput()
	if err != nil {
		return nil, string(printed), err
	}
	generated, err = os.ReadFile(written)
	return generated, string(printed), err
}

// repositoryRoot is the directory of the go.mod of this module. The other
// modules of the repository are never above a test of this one.
func repositoryRoot(t *testing.T) string {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("go.mod not found above the test working directory")
		}
		directory = parent
	}
}
