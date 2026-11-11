package migrate

import (
	"errors"
	"strings"
	"testing"

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
