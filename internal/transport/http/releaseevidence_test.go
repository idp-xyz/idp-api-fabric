package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

const (
	policySnapshotID = "0a1b2c3d-4e5f-4a6b-9c7d-8e9f0a1b2c3d"
	// releasesOfAsset is where the releases of the asset are read, and the
	// artifact and the provenance of one under it.
	releasesOfAsset       = "/v1/products/" + productID + "/assets/" + apiAssetID + "/releases"
	releaseArtifactPath   = releasesOfAsset + "/" + releaseID + "/artifact"
	releaseProvenancePath = releasesOfAsset + "/" + releaseID + "/provenance"
)

// releaseReads stands in for the use cases that read a release by its
// identifier. It keeps what it was asked and answers what the test set. The
// real ones are tested in cmd/fabric-api, with the store behind them.
type releaseReads struct {
	ContractReleases

	download   registry.ReleaseDownload
	provenance registrydomain.ReleaseProvenance
	err        error

	asked []releaseRead
}

// releaseRead is one request for a release: what it asked for, of whom, and
// the identifier of the release.
type releaseRead struct {
	What    string
	Query   registryports.ScopeQuery
	Release string
}

func (r *releaseReads) GetReleaseArtifact(_ context.Context, query registryports.ScopeQuery, id string) (registry.ReleaseDownload, error) {
	r.asked = append(r.asked, releaseRead{"artifact", query, id})
	return r.download, r.err
}

func (r *releaseReads) GetReleaseProvenance(_ context.Context, query registryports.ScopeQuery, id string) (registrydomain.ReleaseProvenance, error) {
	r.asked = append(r.asked, releaseRead{"provenance", query, id})
	return r.provenance, r.err
}

func newReleaseReadService(t *testing.T, fake *releaseReads) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: unusedCatalog{}, Registry: unusedRegistry{}, PolicyReview: unusedPolicyReview{},
		Releases: fake, Publication: unusedPublications{}, Audit: unusedAudit{},
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

// preparedOn is a release of the revision, prepared under a review that
// rita concluded for the sandbox of the internal audience, which waived a
// rule, on a check of the baseline that the tool of its rules made: a release
// of before releases were prepared on a check alone.
func preparedOn(revision registrydomain.Revision) registrydomain.ReleaseProvenance {
	return registrydomain.ReleaseProvenance{
		Release: registrydomain.Release{
			ID: releaseID, Asset: revision.Asset, RevisionID: revision.ID, ReviewID: reviewID,
			Evidence: registrydomain.CheckEvidence{
				CheckRunID: checkRunID, PolicySnapshotID: policySnapshotID, PolicyDigest: "sha256:" + fmt.Sprintf("%064x", 3),
			},
			Version: "1.0.0", InputDigest: revision.RefClosureDigest, ArtifactDigest: "sha256:" + fmt.Sprintf("%064x", 4),
			PreparedAt: createdAt.Add(3 * time.Hour), PreparedBy: registrydomain.Principal(alice),
		},
		Revision: revision,
		Evidence: registrydomain.ReleaseEvidence{
			Check: registrydomain.ReleaseCheck{
				ID: checkRunID, PolicySetID: "fabric-baseline", PolicyDigest: "sha256:" + fmt.Sprintf("%064x", 3),
				Tools:  []registrydomain.CheckTool{{Name: "fabric-openapi-rules", Version: "3"}},
				Status: "FAIL", CompletedAt: createdAt.Add(time.Hour),
				Summary: registrydomain.CheckSummary{Pass: 11, Fail: 1, NotRun: 10, Findings: 2},
			},
			Approval: &registrydomain.ReleaseApproval{
				ReviewID: reviewID, Supplementary: true,
				Scope:       []registrydomain.ReviewTarget{{Audience: "internal", Environment: "sandbox"}},
				ConcludedBy: registrydomain.Principal{Issuer: issuer, Subject: "rita"}, ConcludedAt: createdAt.Add(2 * time.Hour),
				ValidUntil: createdAt.Add(90 * 24 * time.Hour),
				Waivers:    []registrydomain.ApprovalWaiver{{RuleID: "F04-GOV-003", ExpiresAt: createdAt.Add(30 * 24 * time.Hour)}},
			},
		},
	}
}

// preparedOnCheck is a release of the revision prepared on its check alone:
// it names no review, has no approval, and waives the rule that failed in
// the check itself, for the reason whoever prepared it gave.
func preparedOnCheck(revision registrydomain.Revision) registrydomain.ReleaseProvenance {
	provenance := preparedOn(revision)
	provenance.Release.ReviewID = ""
	provenance.Release.Waivers = []registrydomain.Waiver{{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet"}}
	provenance.Evidence.Approval = nil
	return provenance
}

// The contract states what a release was prepared on as it is answered. What
// the service answers satisfies the schema the contract declares for it, and
// that schema is as closed as the answer: it accepts nothing with a member
// more or a member less, and none of what a review says in words.
func TestTheContractStatesTheProvenanceOfAReleaseAsItIsAnswered(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	schemas := contract.Components.Schemas
	declared := schemas["ContractReleaseProvenance"]
	if declared == nil {
		t.Fatal("the contract declares no provenance of a release")
	}
	operation := contract.Paths.Find("/v1/products/{product_id}/assets/{api_asset_id}/releases/{release_id}/provenance").Get
	if answer := operation.Responses.Status(http.StatusOK).Value.Content.Get("application/json"); answer == nil ||
		answer.Schema.Ref != "#/components/schemas/ContractReleaseProvenance" {
		t.Fatal("the provenance of a release is not answered as a ContractReleaseProvenance")
	}

	fake := &releaseReads{provenance: preparedOn(fromGit())}
	recorder := newReleaseReadService(t, fake).do(t, call{Method: http.MethodGet, Target: releaseProvenancePath, Token: alice.Subject})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	if err := declared.Value.VisitJSON(members(t, recorder)); err != nil {
		t.Fatalf("the contract refuses what the service answers: %v\n%s", err, recorder.Body)
	}

	section := func(name string) func(map[string]any) map[string]any {
		return func(body map[string]any) map[string]any {
			part, _ := body[name].(map[string]any)
			return part
		}
	}
	revision, check, approval := section("revision"), section("check"), section("approval")
	waiver := func(body map[string]any) map[string]any {
		waivers, _ := approval(body)["waivers"].([]any)
		first, _ := waivers[0].(map[string]any)
		return first
	}
	without := func(of func(map[string]any) map[string]any, member string) func(map[string]any) {
		return func(body map[string]any) { delete(of(body), member) }
	}
	set := func(of func(map[string]any) map[string]any, member string, value any) func(map[string]any) {
		return func(body map[string]any) { of(body)[member] = value }
	}
	// repeated is the first member of a list, n times: members that are
	// right each, and only too many.
	repeated := func(list any, n int) []any {
		first, _ := list.([]any)
		many := make([]any, n)
		for i := range many {
			many[i] = first[0]
		}
		return many
	}
	top := func(body map[string]any) map[string]any { return body }
	refused := map[string]func(map[string]any){
		"a member the contract does not declare": set(top, "approval_holds", true),
		"no release":                             without(top, "release"),
		"no revision":                            without(top, "revision"),
		"no check":                               without(top, "check"),

		"where the revision stands in its review": set(revision, "status", "APPROVED"),
		"a revision without its identifier":       without(revision, "revision_id"),
		"a revision without its sequence":         without(revision, "revision_sequence"),
		"a revision without its kind":             without(revision, "source_kind"),
		"a kind nobody declared":                  set(revision, "source_kind", "EDITOR"),
		"a revision without its entrypoint":       without(revision, "entrypoint"),
		"a revision without its source digest":    without(revision, "source_digest"),
		"a revision without its closure digest":   without(revision, "ref_closure_digest"),
		"an origin without its commit":            func(body map[string]any) { delete(revision(body)["git_origin"].(map[string]any), "commit") },
		"an origin with a member more":            func(body map[string]any) { revision(body)["git_origin"].(map[string]any)["branch"] = "main" },

		"a check without its identifier": without(check, "check_run_id"),
		"a check without its policy set": without(check, "policy_set_id"),
		"a check without its policy":     without(check, "policy_digest"),
		"a check without its tools":      without(check, "tool_manifest"),
		"a check with no tool":           set(check, "tool_manifest", []any{}),
		"a check without its conclusion": without(check, "status"),
		"a conclusion no check has":      set(check, "status", "NOT_RUN"),
		"a check without its summary":    without(check, "summary"),
		"a check without its time":       without(check, "completed_at"),

		"an approval without its review":                  without(approval, "review_id"),
		"an approval that is not said to be a second one": without(approval, "supplementary"),
		"an approval without its scope":                   without(approval, "scope"),
		"an approval for no target":                       set(approval, "scope", []any{}),
		"an approval without its approver":                without(approval, "concluded_by"),
		"an approval without its time":                    without(approval, "concluded_at"),
		"an approval without its validity":                without(approval, "valid_until"),
		"an approval without its waivers":                 without(approval, "waivers"),
		"a waiver without its rule":                       func(body map[string]any) { delete(waiver(body), "rule_id") },
		"a waiver without its expiry":                     func(body map[string]any) { delete(waiver(body), "expires_at") },
		"a waiver with the reason it was given":           func(body map[string]any) { waiver(body)["reason"] = "generated clients are not used yet" },
		"a waiver with what makes up for it":              func(body map[string]any) { waiver(body)["compensating_control"] = "the gateway names every route" },

		// What is said of a check is its counts and not its findings, and
		// of an approval its facts and not its reason.
		"a check with the findings it made":        set(check, "findings", []any{}),
		"an approval with the reason it was given": set(approval, "reason", "reviewed with the partner team"),
		"an approval for more targets than there are": func(body map[string]any) {
			approval(body)["scope"] = repeated(approval(body)["scope"], 7)
		},
		"an approval that waives more rules than a policy has": func(body map[string]any) {
			approval(body)["waivers"] = repeated(approval(body)["waivers"], 101)
		},
	}
	for name, change := range refused {
		t.Run(name, func(t *testing.T) {
			body := members(t, recorder)
			change(body)
			if err := declared.Value.VisitJSON(body); err == nil {
				t.Errorf("the contract accepts an answer with %s: %v", name, body)
			}
		})
	}
	// And it takes as many as an approval can hold: every target there is,
	// a waiver for every rule of a policy. A release prepared on its check
	// alone has no approval at all.
	for name, change := range map[string]func(map[string]any){
		"no approval": without(top, "approval"),
		"an approval for each audience in each environment": func(body map[string]any) {
			var scope []any
			for _, audience := range []string{"public", "partner", "internal"} {
				for _, environment := range []string{"sandbox", "production"} {
					scope = append(scope, map[string]any{"audience": audience, "environment": environment})
				}
			}
			approval(body)["scope"] = scope
		},
		"an approval that waives a hundred rules": func(body map[string]any) {
			approval(body)["waivers"] = repeated(approval(body)["waivers"], 100)
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := members(t, recorder)
			change(body)
			if err := declared.Value.VisitJSON(body); err != nil {
				t.Errorf("the contract refuses an answer with %s: %v", name, err)
			}
		})
	}

	// The parts are declared as what they are shown from.
	revisionSchema := schemas["ReleaseProvenanceRevision"]
	if revisionSchema == nil || schemas["ReleaseProvenanceCheck"] == nil || schemas["ReleaseProvenanceApproval"] == nil || schemas["ReleaseProvenanceWaiver"] == nil {
		t.Fatal("the contract declares no revision, check, approval or waiver of a provenance")
	}
	for member, schema := range map[string]string{
		"release": "ContractRelease", "revision": "ReleaseProvenanceRevision", "check": "ReleaseProvenanceCheck", "approval": "ReleaseProvenanceApproval",
	} {
		if property := declared.Value.Properties[member]; property == nil || property.Ref != "#/components/schemas/"+schema {
			t.Errorf("the %s of a provenance is not declared by %s", member, schema)
		}
	}
	for member, schema := range map[string]string{"git_origin": "GitOrigin", "entrypoint": "ContractSourcePath"} {
		if property := revisionSchema.Value.Properties[member]; property == nil || property.Ref != "#/components/schemas/"+schema {
			t.Errorf("the %s of a revision of a release is not declared by %s", member, schema)
		}
	}
	var kinds []string
	for _, value := range revisionSchema.Value.Properties["source_kind"].Value.Enum {
		kind, _ := value.(string)
		kinds = append(kinds, kind)
	}
	if !reflect.DeepEqual(kinds, []string{registrydomain.SourceUpload, registrydomain.SourceGit}) {
		t.Errorf("the contract declares the source kinds %v of a release, the model %s and %s", kinds, registrydomain.SourceUpload, registrydomain.SourceGit)
	}
	if got := schemas["ReleaseProvenanceWaiver"].Value.Required; !reflect.DeepEqual(got, []string{"rule_id", "expires_at"}) {
		t.Errorf("the contract requires %v of a waiver, the answer a rule and the time it lasts until", got)
	}

	// Each part is closed as the whole is: it carries what it names.
	for _, name := range []string{
		"ContractReleaseProvenance", "ReleaseProvenanceRevision", "ReleaseProvenanceCheck", "ReleaseProvenanceApproval", "ReleaseProvenanceWaiver",
	} {
		if extra := schemas[name].Value.AdditionalProperties; extra.Has == nil || *extra.Has || extra.Schema != nil {
			t.Errorf("%s may carry members the contract does not name: %+v", name, extra)
		}
	}

	// An approval is for no more targets than a review is asked for, which
	// is each audience in each environment, and it waives no more rules than
	// a review concludes with.
	atMost := func(schema, member string) uint64 {
		t.Helper()
		property := schemas[schema].Value.Properties[member]
		if property == nil || property.Value.MaxItems == nil {
			t.Fatalf("%s has no bound on its %s", schema, member)
		}
		return *property.Value.MaxItems
	}
	targets := uint64(len(schemas["Audience"].Value.Enum) * len(schemas["Environment"].Value.Enum))
	if got, want := atMost("ReleaseProvenanceApproval", "scope"), atMost("Review", "scope"); got != want || got != targets {
		t.Errorf("an approval in a provenance is for at most %d targets, a review for %d, and there are %d", got, want, targets)
	}
	if got, want := atMost("ReleaseProvenanceApproval", "waivers"), atMost("ReviewConclusion", "waivers"); got != want {
		t.Errorf("an approval in a provenance waives at most %d rules, the conclusion of a review %d", got, want)
	}
}

// The contract states how a release is read besides what it answers: it is
// named by its asset and by itself as the read of the release names it,
// answered as what was asked for or as a problem of one of the kinds a read
// has, and never kept. The artifact is octets under the digest of the
// bytes, a header it is never without; the provenance is JSON and has no
// header but the one that says it is not to be kept.
func TestTheContractStatesTheReadsOfAReleaseAsTheyAreAnswered(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	const release = "/v1/products/{product_id}/assets/{api_asset_id}/releases/{release_id}"
	refs := func(operation string) []string {
		var named []string
		for _, parameter := range contract.Paths.Find(operation).Get.Parameters {
			named = append(named, parameter.Ref)
		}
		return named
	}

	for name, c := range map[string]struct {
		path, content string
		headers       []string
		answers       []string
	}{
		// The artifact is refused besides while an access block of the
		// release stands (409, CR-0002); the provenance is not.
		"the artifact": {release + "/artifact", "application/octet-stream", []string{"Cache-Control", "X-Artifact-Digest"},
			[]string{"200", "400", "401", "403", "404", "409", "default"}},
		"the provenance": {release + "/provenance", "application/json", []string{"Cache-Control"},
			[]string{"200", "400", "401", "403", "404", "default"}},
	} {
		t.Run(name, func(t *testing.T) {
			operation := contract.Paths.Find(c.path).Get
			if got, want := refs(c.path), refs(release); len(want) != 3 || !reflect.DeepEqual(got, want) {
				t.Errorf("%s is named by %v, want %v as the release is", name, got, want)
			}

			// The answers a read has: a request that cannot be read, a caller
			// who is not known, one who may not read, and what is not there,
			// and for anything else a problem.
			if declared := slices.Sorted(maps.Keys(operation.Responses.Map())); !reflect.DeepEqual(declared, c.answers) {
				t.Errorf("%s declares the answers %v, want %v", name, declared, c.answers)
			}
			for _, status := range c.answers[1:] {
				ref := "#/components/responses/Problem"
				if status == "401" {
					ref = "#/components/responses/Unauthenticated"
				}
				if answer := operation.Responses.Value(status); answer == nil || answer.Ref != ref {
					t.Errorf("%s declares %s as %+v, want %s", name, status, answer, ref)
				}
			}

			// What it answers is said in the headers it has, each of them
			// always, and in the one content type.
			ok := operation.Responses.Value("200").Value
			if headers := slices.Sorted(maps.Keys(ok.Headers)); !reflect.DeepEqual(headers, c.headers) {
				t.Errorf("%s answers with the headers %v, want %v", name, headers, c.headers)
			}
			for header, declared := range ok.Headers {
				if !declared.Value.Required {
					t.Errorf("%s may answer without %s", name, header)
				}
			}
			if types := slices.Sorted(maps.Keys(ok.Content)); !reflect.DeepEqual(types, []string{c.content}) {
				t.Errorf("%s answers as %v, want %s only", name, types, c.content)
			}
			// Not to be kept is the one thing the contract lets it say of that.
			if header := ok.Headers["Cache-Control"]; header == nil || header.Value.Schema == nil ||
				!header.Value.Schema.Value.Type.Is("string") || !reflect.DeepEqual(header.Value.Schema.Value.Enum, []any{"no-store"}) {
				t.Errorf("%s lets the answer say of its keeping something other than no-store: %+v", name, header)
			}
		})
	}

	// The bytes are answered under the digest that was recorded of them, in
	// the form every digest has, and as what they are: any octets.
	ok := contract.Paths.Find(release + "/artifact").Get.Responses.Value("200").Value
	if digest := ok.Headers["X-Artifact-Digest"]; digest == nil || digest.Value.Schema == nil || digest.Value.Schema.Ref != "#/components/schemas/ContractDigest" {
		t.Errorf("the artifact does not say its digest as a ContractDigest: %+v", digest)
	}
	if octets := ok.Content.Get("application/octet-stream"); octets == nil || octets.Schema == nil ||
		!octets.Schema.Value.Type.Is("string") || octets.Schema.Value.Format != "binary" {
		t.Errorf("the artifact is not answered as binary: %+v", octets)
	}
}

// A provenance shows the check and the approval as they were recorded, and
// no number as another: a rule that failed is one rule however many
// findings it has, and every count of the check is its own. An approval for
// several targets names each of them, in the order of the review, and the
// waivers are rules and the times they last until, in the order they were
// given.
func TestAProvenanceShowsTheCheckAndTheApprovalAsTheyWereRecorded(t *testing.T) {
	provenance := preparedOn(fromGit())
	check, approval := &provenance.Evidence.Check, provenance.Evidence.Approval
	// No two counts are alike, so that none can be taken for another.
	check.Summary = registrydomain.CheckSummary{Pass: 11, Warn: 2, Fail: 3, Unknown: 4, NotRun: 5, Findings: 17}
	check.Tools = []registrydomain.CheckTool{{Name: "fabric-openapi-rules", Version: "3"}, {Name: "fabric-compatibility", Version: "1"}}
	approval.Scope = []registrydomain.ReviewTarget{
		{Audience: "internal", Environment: "production"}, {Audience: "internal", Environment: "sandbox"}, {Audience: "partner", Environment: "sandbox"},
	}
	approval.Waivers = []registrydomain.ApprovalWaiver{
		{RuleID: "F04-GOV-001", ExpiresAt: createdAt.Add(24 * time.Hour)}, {RuleID: "F04-GOV-003", ExpiresAt: createdAt.Add(30 * 24 * time.Hour)},
	}
	at := func(moment time.Time) string { return moment.Format(time.RFC3339Nano) }

	fake := &releaseReads{provenance: provenance}
	recorder := newReleaseReadService(t, fake).do(t, call{Method: http.MethodGet, Target: releaseProvenancePath, Token: alice.Subject})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	shown := members(t, recorder)
	want := map[string]any{
		"check": map[string]any{
			"check_run_id": checkRunID, "policy_set_id": "fabric-baseline", "policy_digest": check.PolicyDigest, "status": "FAIL",
			"tool_manifest": []any{
				map[string]any{"name": "fabric-openapi-rules", "version": "3"}, map[string]any{"name": "fabric-compatibility", "version": "1"},
			},
			"summary": map[string]any{
				"pass": float64(11), "warn": float64(2), "fail": float64(3), "unknown": float64(4), "not_run": float64(5), "finding_count": float64(17),
			},
			"completed_at": at(check.CompletedAt),
		},
		"approval": map[string]any{
			"review_id": reviewID, "supplementary": true,
			"scope": []any{
				map[string]any{"audience": "internal", "environment": "production"},
				map[string]any{"audience": "internal", "environment": "sandbox"},
				map[string]any{"audience": "partner", "environment": "sandbox"},
			},
			"concluded_by": map[string]any{"issuer": issuer, "subject": "rita"}, "concluded_at": at(approval.ConcludedAt), "valid_until": at(approval.ValidUntil),
			"waivers": []any{
				map[string]any{"rule_id": "F04-GOV-001", "expires_at": at(createdAt.Add(24 * time.Hour))},
				map[string]any{"rule_id": "F04-GOV-003", "expires_at": at(createdAt.Add(30 * 24 * time.Hour))},
			},
		},
	}
	for part, expected := range want {
		if !reflect.DeepEqual(shown[part], expected) {
			t.Errorf("the %s of the provenance is\n %v\nwant\n %v", part, shown[part], expected)
		}
	}
}

// A release prepared on its check alone has a provenance without an
// approval: it names no review, and the rules it let stand are the waivers of
// the release, each with the reason whoever prepared it gave. What the
// contract declares takes it as it is.
func TestTheProvenanceOfAReleasePreparedOnItsCheckHasNoApproval(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	fake := &releaseReads{provenance: preparedOnCheck(fromGit())}
	recorder := newReleaseReadService(t, fake).do(t, call{Method: http.MethodGet, Target: releaseProvenancePath, Token: alice.Subject})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	shown := members(t, recorder)
	if err := contract.Components.Schemas["ContractReleaseProvenance"].Value.VisitJSON(shown); err != nil {
		t.Fatalf("the contract refuses the provenance of a release prepared on its check: %v", err)
	}
	if _, approved := shown["approval"]; approved {
		t.Errorf("the provenance names an approval: %v", shown["approval"])
	}
	release, _ := shown["release"].(map[string]any)
	if _, named := release["review_id"]; named || !reflect.DeepEqual(release["waivers"], []any{
		map[string]any{"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet"},
	}) {
		t.Errorf("the release of the provenance is %v, want its waivers and no review", release)
	}
	if check, _ := shown["check"].(map[string]any); check["check_run_id"] != checkRunID {
		t.Errorf("the provenance names the check %v, want %s", check["check_run_id"], checkRunID)
	}
}

// A release shows the origin of its revision exactly when the revision has
// one, member for member as it was declared, and not where the revision
// stands. One whose kind and origin disagree is a failure of the service and
// is answered as one, not shown half.
func TestAReleaseShowsTheOriginOfItsRevisionExactlyWhenItHasOne(t *testing.T) {
	uploaded := fromGit()
	uploaded.SourceKind, uploaded.Origin = registrydomain.SourceUpload, registrydomain.GitOrigin{}
	unnamed := fromGit()
	unnamed.Origin.Ref = ""

	shown := map[string]struct {
		revision registrydomain.Revision
		kind     string
		origin   any
	}{
		"from a commit on a branch": {fromGit(), "GIT", map[string]any{
			"repository": contractsRepository, "commit": contractsCommit, "ref": "refs/heads/main"}},
		"from a commit, no reference said": {unnamed, "GIT", map[string]any{
			"repository": contractsRepository, "commit": contractsCommit}},
		"uploaded": {uploaded, "UPLOAD", nil},
	}
	for name, c := range shown {
		t.Run(name, func(t *testing.T) {
			revision := c.revision
			revision.Status = "APPROVED"
			fake := &releaseReads{provenance: preparedOn(revision)}
			recorder := newReleaseReadService(t, fake).do(t, call{Method: http.MethodGet, Target: releaseProvenancePath, Token: alice.Subject})
			if recorder.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
			}
			body, _ := members(t, recorder)["revision"].(map[string]any)
			origin, said := body["git_origin"]
			if body["source_kind"] != c.kind || said != (c.origin != nil) || (said && !reflect.DeepEqual(origin, c.origin)) {
				t.Errorf("source_kind %v and git_origin %v, want %s and %v", body["source_kind"], origin, c.kind, c.origin)
			}
			want := map[string]any{
				"revision_id": revisionID, "revision_sequence": float64(2), "source_kind": c.kind, "entrypoint": "api/openapi.yaml",
				"source_digest": revision.SourceDigest, "ref_closure_digest": revision.RefClosureDigest,
			}
			if c.origin != nil {
				want["git_origin"] = c.origin
			}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("the revision of the release is shown as\n %v\nwant\n %v", body, want)
			}
		})
	}

	headless := fromGit()
	headless.Origin = registrydomain.GitOrigin{}
	stray := fromGit()
	stray.SourceKind = registrydomain.SourceUpload
	unknown := fromGit()
	unknown.SourceKind, unknown.Origin = "EDITOR", registrydomain.GitOrigin{}
	for name, revision := range map[string]registrydomain.Revision{
		"GIT without an origin":  headless,
		"UPLOAD with an origin":  stray,
		"a kind nobody declared": unknown,
		"no kind and no origin":  {ID: revisionID},
		"an origin and no kind":  {ID: revisionID, Origin: fromGit().Origin},
	} {
		t.Run(name, func(t *testing.T) {
			s := newReleaseReadService(t, &releaseReads{provenance: preparedOn(revision)})
			wantProblem(t, s.do(t, call{Method: http.MethodGet, Target: releaseProvenancePath, Token: alice.Subject}),
				http.StatusInternalServerError, CodeInternalError, "")
		})
	}
}

// What the contract has no word for is not shown: a check that concluded
// what no check does, and an approval for a target nobody has, are failures
// of the service and are answered as one.
func TestAProvenanceThatTheContractHasNoWordForIsAFailureOfTheService(t *testing.T) {
	cases := map[string]func(*registrydomain.ReleaseProvenance){
		"a check that concluded NOT_RUN": func(p *registrydomain.ReleaseProvenance) { p.Evidence.Check.Status = "NOT_RUN" },
		"a check that concluded nothing": func(p *registrydomain.ReleaseProvenance) { p.Evidence.Check.Status = "" },
		"an approval for an audience nobody has": func(p *registrydomain.ReleaseProvenance) {
			p.Evidence.Approval.Scope = []registrydomain.ReviewTarget{{Audience: "everyone", Environment: "sandbox"}}
		},
		"an approval for an environment nobody has": func(p *registrydomain.ReleaseProvenance) {
			p.Evidence.Approval.Scope = []registrydomain.ReviewTarget{{Audience: "internal", Environment: "staging"}}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			provenance := preparedOn(fromGit())
			change(&provenance)
			s := newReleaseReadService(t, &releaseReads{provenance: provenance})
			wantProblem(t, s.do(t, call{Method: http.MethodGet, Target: releaseProvenancePath, Token: alice.Subject}),
				http.StatusInternalServerError, CodeInternalError, "")
			if !strings.Contains(s.log.String(), "which the contract does not declare") {
				t.Errorf("the log does not say what the contract has no word for: %s", s.log)
			}
		})
	}
}

// The artifact of a release is answered as what it is: the bytes the release
// was prepared with, as an octet stream that is not cached, under the digest
// the release records, which is the digest of the bytes. What the use case
// refuses is answered as it is for the read of the release, and what is not
// a release identifier does not reach it.
func TestTheArtifactOfAReleaseIsAnsweredAsOctetsUnderItsDigest(t *testing.T) {
	payload := []byte("{\"format\":\"fabric-openapi-source/v1\",\"files\":[]}\n\x00\xff")
	release := preparedOn(fromGit()).Release
	release.ArtifactDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(payload))
	read := call{Method: http.MethodGet, Target: releaseArtifactPath, Token: alice.Subject}

	fake := &releaseReads{download: registry.ReleaseDownload{Release: release, Payload: payload}}
	s := newReleaseReadService(t, fake)
	recorder := s.do(t, read)
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), payload) ||
		recorder.Header().Get("Content-Type") != "application/octet-stream" ||
		recorder.Header().Get("Content-Length") != fmt.Sprint(len(payload)) ||
		recorder.Header().Get("Cache-Control") != "no-store" ||
		recorder.Header().Get("X-Artifact-Digest") != release.ArtifactDigest {
		t.Fatalf("the artifact: %d %q %v, want the bytes of the release as an octet stream under %s", recorder.Code, recorder.Body, recorder.Header(), release.ArtifactDigest)
	}
	if len(fake.asked) != 1 || fake.asked[0].What != "artifact" || fake.asked[0].Release != releaseID || fake.asked[0].Query.CorrelationID == "" {
		t.Fatalf("the use case was asked %+v, want one read of the release under the identifier of the request", fake.asked)
	}
	asked := fake.asked[0].Query
	asked.CorrelationID = ""
	if want := (registryports.ScopeQuery{Caller: registrydomain.Principal(alice), ProductID: productID, AssetID: apiAssetID}); asked != want {
		t.Errorf("the use case was asked for %+v, want %+v", asked, want)
	}

	for name, c := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"a release of no asset here":                   {registry.ErrReleaseNotFound, http.StatusNotFound, "RELEASE_NOT_FOUND"},
		"a caller who reads no release":                {registry.ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		"a release an access block names":              {registry.ErrReleaseBlocked, http.StatusConflict, "RELEASE_ACCESS_BLOCKED"},
		"bytes that are not what the release recorded": {fmt.Errorf("release %s: %w", releaseID, registrydomain.ErrArtifactDamaged), http.StatusInternalServerError, CodeInternalError},
		"anything else":                                {errors.New("the connection is gone"), http.StatusInternalServerError, CodeInternalError},
	} {
		t.Run(name, func(t *testing.T) {
			s := newReleaseReadService(t, &releaseReads{download: registry.ReleaseDownload{Release: release, Payload: payload}, err: c.err})
			recorder := s.do(t, read)
			wantRefusal(t, recorder, c.status, c.code)
			if bytes.Contains(recorder.Body.Bytes(), payload) || (c.status >= 500 && strings.Contains(recorder.Body.String(), c.err.Error())) {
				t.Errorf("the answer carries the bytes or the cause: %s", recorder.Body)
			}
			if c.status >= 500 && !strings.Contains(s.log.String(), c.err.Error()) {
				t.Errorf("the log does not say what failed: %s", s.log)
			}
		})
	}

	for name, c := range map[string]struct {
		call call
		code string
	}{
		"a release identifier that is none": {call{Method: http.MethodGet, Target: releasesOfAsset + "/latest/artifact", Token: alice.Subject}, CodeRequestInvalid},
		"a query nothing declares":          {call{Method: http.MethodGet, Target: releaseArtifactPath + "?digest=sha256", Token: alice.Subject}, CodeRequestInvalid},
		"no credentials":                    {call{Method: http.MethodGet, Target: releaseArtifactPath}, CodeCredentialsMissing},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &releaseReads{download: registry.ReleaseDownload{Release: release, Payload: payload}}
			status := http.StatusBadRequest
			if c.code == CodeCredentialsMissing {
				status = http.StatusUnauthorized
			}
			wantRefusal(t, newReleaseReadService(t, fake).do(t, c.call), status, c.code)
			if len(fake.asked) != 0 {
				t.Errorf("the use case was asked %+v", fake.asked)
			}
		})
	}
}
