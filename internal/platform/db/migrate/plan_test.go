package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"
)

func TestPlanStartsWithTheFrameworkAssetsUnchanged(t *testing.T) {
	plan, err := Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	assets := bentopostgres.Migrations()
	if len(assets) == 0 {
		t.Fatal("the framework ships no migration; this test would pass vacuously")
	}
	if len(plan) < len(assets) {
		t.Fatalf("the plan has %d steps, fewer than the %d framework assets", len(plan), len(assets))
	}

	for index, asset := range assets {
		step := plan[index]
		want := Step{
			ID:               "framework/" + asset.ID,
			Origin:           OriginFramework,
			Schema:           SchemaBento,
			Checksum:         asset.Checksum,
			FrameworkVersion: FrameworkVersion,
			SQL:              step.SQL,
		}
		if step != want {
			t.Errorf("step %d is %+v, want the framework asset %s as it ships", index, step, asset.ID)
		}
		if !strings.Contains(step.SQL, `"bento".outbox`) {
			t.Errorf("step %s is not rendered for the %s schema", step.ID, SchemaBento)
		}
		if strings.Contains(step.SQL, "{{") {
			t.Errorf("step %s still carries a template token", step.ID)
		}
	}
}

var fingerprint = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func TestPlanContinuesWithTheMigrationsFabricOwns(t *testing.T) {
	plan, err := Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	fabric := plan[len(bentopostgres.Migrations()):]
	if len(fabric) == 0 {
		t.Fatal("the plan carries no Fabric migration; this test would pass vacuously")
	}

	ids := make([]string, 0, len(fabric))
	for _, step := range fabric {
		ids = append(ids, step.ID)

		context, _, _ := strings.Cut(step.ID, "/")
		switch {
		case step.Origin != OriginFabric || step.FrameworkVersion != "":
			t.Errorf("step %s is recorded as %s of framework version %q, want a Fabric step", step.ID, step.Origin, step.FrameworkVersion)
		case step.Schema != context:
			t.Errorf("step %s targets schema %q, want the schema of its context", step.ID, step.Schema)
		case !fingerprint.MatchString(step.Checksum):
			t.Errorf("step %s has the checksum %q, want a SHA-256", step.ID, step.Checksum)
		case !strings.Contains(step.SQL, context+"."):
			t.Errorf("step %s names no object of schema %s", step.ID, context)
		}
	}
	if !slices.IsSorted(ids) {
		t.Errorf("the Fabric steps come as %v, want them in the order of their paths", ids)
	}
	if !slices.Contains(ids, "platformoperations/001_audit_record") {
		t.Errorf("the Fabric steps %v lack the audit trail", ids)
	}
}

func TestFabricStepsComeInOrderEachWithItsFingerprint(t *testing.T) {
	target, slot, asset := "CREATE TABLE publication.target ();", "CREATE TABLE publication.slot ();", "CREATE TABLE apicatalog.asset ();"
	steps, err := fabricSteps(fstest.MapFS{
		"publication/002_slot.sql":   {Data: []byte(slot)},
		"publication/001_target.sql": {Data: []byte(target)},
		"apicatalog/001_asset.sql":   {Data: []byte(asset)},
	})
	if err != nil {
		t.Fatalf("read the steps: %v", err)
	}

	sum := func(content string) string {
		digest := sha256.Sum256([]byte(content))
		return "sha256:" + hex.EncodeToString(digest[:])
	}
	want := []Step{
		{ID: "apicatalog/001_asset", Origin: OriginFabric, Schema: "apicatalog", Checksum: sum(asset), SQL: asset},
		{ID: "publication/001_target", Origin: OriginFabric, Schema: "publication", Checksum: sum(target), SQL: target},
		{ID: "publication/002_slot", Origin: OriginFabric, Schema: "publication", Checksum: sum(slot), SQL: slot},
	}
	if !slices.Equal(steps, want) {
		t.Errorf("steps\n %+v\nwant\n %+v", steps, want)
	}
	if got, want := schemasOf(steps), []string{SchemaHistory, "apicatalog", "publication"}; !slices.Equal(got, want) {
		t.Errorf("schemas %v, want %v", got, want)
	}
}

// Whatever is in the tree and is not a migration is an error, never skipped:
// a file that silently stays out of the plan is a table a process expects and
// does not find.
func TestFabricStepsRefuseWhatIsNotAMigration(t *testing.T) {
	sql := &fstest.MapFile{Data: []byte("SELECT 1;")}
	cases := map[string]fstest.MapFS{
		"a file beside the contexts":         {"001_stray.sql": sql},
		"a context in upper case":            {"Publication/001_target.sql": sql},
		"a context with an underscore":       {"product_governance/001_product.sql": sql},
		"a context that starts with a digit": {"1st/001_first.sql": sql},
		"the schema of the framework":        {"bento/001_outbox.sql": sql},
		"the prefix of the framework steps":  {"framework/001_outbox.sql": sql},
		"the default schema":                 {"public/001_table.sql": sql},
		"a file that is not SQL":             {"publication/001_target.txt": sql},
		"a file without a number":            {"publication/target.sql": sql},
		"a number of two digits":             {"publication/01_target.sql": sql},
		"a name in upper case":               {"publication/001_Target.sql": sql},
		"a directory inside a context":       {"publication/nested/001_target.sql": sql},
		"two files with one number":          {"publication/001_target.sql": sql, "publication/001_slot.sql": sql},
		"an empty file":                      {"publication/001_target.sql": {Data: []byte(" \n")}},
	}
	for name, tree := range cases {
		t.Run(name, func(t *testing.T) {
			if steps, err := fabricSteps(tree); err == nil {
				t.Fatalf("the tree was read as %+v, want it refused", steps)
			}
		})
	}
}

func TestPlanIDsAreUnique(t *testing.T) {
	plan, err := Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	seen := make(map[string]bool)
	for _, step := range plan {
		if seen[step.ID] {
			t.Errorf("migration %s appears twice; a step is identified by its ID", step.ID)
		}
		seen[step.ID] = true
	}
}

func TestVerifyNoDrift(t *testing.T) {
	plan := []Step{{ID: "a", Checksum: "sha256:1"}, {ID: "b", Checksum: "sha256:2"}}

	cases := map[string]struct {
		recorded map[string]string
		want     error
	}{
		"nothing applied yet":            {map[string]string{}, nil},
		"some steps applied":             {map[string]string{"a": "sha256:1"}, nil},
		"every step applied":             {map[string]string{"a": "sha256:1", "b": "sha256:2"}, nil},
		"an applied artifact changed":    {map[string]string{"a": "sha256:rewritten"}, ErrChecksumDrift},
		"a step this build lacks":        {map[string]string{"a": "sha256:1", "z": "sha256:9"}, ErrUnknownMigration},
		"the earliest ID is reported":    {map[string]string{"0": "sha256:0", "a": "sha256:rewritten"}, ErrUnknownMigration},
		"drift before a later unknown":   {map[string]string{"a": "sha256:rewritten", "z": "sha256:9"}, ErrChecksumDrift},
		"an unknown step with no others": {map[string]string{"z": "sha256:9"}, ErrUnknownMigration},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := verifyNoDrift(c.recorded, plan); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}
