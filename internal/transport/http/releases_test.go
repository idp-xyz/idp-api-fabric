package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	reviewdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// The contract states what a release is in the words of the model, so what
// the validator lets through is what the model accepts: the form of a
// version, where it is asked and where it is answered, the one format an
// artifact has and the one status a release has.
func TestTheContractStatesAReleaseAsTheModelDoes(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	schemas := contract.Components.Schemas
	for _, name := range []string{"ReleasePreparation", "ContractRelease"} {
		if schemas[name] == nil {
			t.Errorf("the contract declares no %s", name)
			continue
		}
		version := schemas[name].Value.Properties["version"]
		if version == nil || version.Value.Pattern != registrydomain.ReleaseVersionPattern || version.Value.MaxLength != nil {
			t.Errorf("the version of %s is not stated by the pattern of the model, %q, alone", name, registrydomain.ReleaseVersionPattern)
		}
	}
	if got := schemas["ReleasePreparation"].Value.Required; !reflect.DeepEqual(got, []string{"revision_id", "check_run_id", "version"}) {
		t.Errorf("the contract requires %v of a preparation, the model a revision, a check and a version", got)
	}
	if _, named := schemas["ReleasePreparation"].Value.Properties["review_id"]; named {
		t.Error("a preparation still names a review: a release is prepared on a check")
	}
	release := schemas["ContractRelease"].Value
	for member, want := range map[string]string{"artifact_format": registrydomain.ArtifactFormat, "status": "READY"} {
		property := release.Properties[member]
		if property == nil || !reflect.DeepEqual(property.Value.Enum, []any{want}) {
			t.Errorf("the contract does not state the %s of a release as %q and nothing else", member, want)
		}
	}
	// A release always says what it waived, and names a review only when it
	// was prepared under an approval.
	required := release.Required
	if !slices.Contains(required, "waivers") || !slices.Contains(required, "check_run_id") || slices.Contains(required, "review_id") ||
		release.Properties["review_id"] == nil {
		t.Errorf("a release requires %v, want its waivers and its check and not a review, which it may name", required)
	}

	// A waiver is the rule and the reason, as the model keeps it, and the
	// same waiver is sent and answered: a rule in the form of the policy and
	// a reason of one line as long as the model takes, as many as a review
	// waived before.
	waiver := schemas["ReleaseWaiver"]
	if waiver == nil {
		t.Fatal("the contract declares no waiver of a release")
	}
	if got := waiver.Value.Required; !reflect.DeepEqual(got, []string{"rule_id", "reason"}) || len(waiver.Value.Properties) != 2 {
		t.Errorf("a waiver of a release requires %v of %d members, want a rule and a reason and nothing else", got, len(waiver.Value.Properties))
	}
	if extra := waiver.Value.AdditionalProperties; extra.Has == nil || *extra.Has || extra.Schema != nil {
		t.Errorf("a waiver of a release may carry members the contract does not name: %+v", extra)
	}
	if got := waiver.Value.Properties["rule_id"].Ref; got != "#/components/schemas/ReviewRuleId" {
		t.Errorf("the rule of a waiver is declared by %q, want the rule identifier of a policy", got)
	}
	reason := waiver.Value.Properties["reason"]
	if reason.Ref != "#/components/schemas/ReviewReason" || reason.Value.MaxLength == nil ||
		*reason.Value.MaxLength != reviewdomain.MaxReviewReason || reason.Value.MinLength != 1 {
		t.Errorf("the reason of a waiver is declared by %q as 1 to %v characters, the model takes 1 to %d", reason.Ref, reason.Value.MaxLength, reviewdomain.MaxReviewReason)
	}
	for _, name := range []string{"ReleasePreparation", "ContractRelease"} {
		waivers := schemas[name].Value.Properties["waivers"]
		if waivers == nil || waivers.Value.Items == nil || waivers.Value.Items.Ref != "#/components/schemas/ReleaseWaiver" ||
			waivers.Value.MaxItems == nil || *waivers.Value.MaxItems != *schemas["ReviewConclusion"].Value.Properties["waivers"].Value.MaxItems {
			t.Errorf("the waivers of %s are not at most as many ReleaseWaiver as a review waived: %+v", name, waivers)
		}
	}
}

// releasePreparations stands in for the use case that prepares a release. It
// keeps what it was asked and answers what the test set.
type releasePreparations struct {
	ContractReleases

	preparation registry.Preparation
	err         error

	asked []registry.PrepareRelease
}

func (r *releasePreparations) Prepare(_ context.Context, command registry.PrepareRelease) (registry.Preparation, error) {
	r.asked = append(r.asked, command)
	return r.preparation, r.err
}

func newReleaseService(t *testing.T, fake *releasePreparations) *service {
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

// preparedRelease is a release prepared on a check, with the two waivers it
// was prepared with.
func preparedRelease() registrydomain.Release {
	release := preparedOnCheck(fromGit()).Release
	release.Waivers = []registrydomain.Waiver{
		{RuleID: "F04-GOV-002", Reason: "the title is settled next sprint"},
		{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet"},
	}
	return release
}

// preparation is what a publisher sends to prepare preparedRelease, changed
// by the test.
func preparation(change func(map[string]any)) string {
	body := map[string]any{
		"revision_id": revisionID, "check_run_id": checkRunID, "version": "1.0.0",
		"waivers": []any{
			map[string]any{"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet"},
			map[string]any{"rule_id": "F04-GOV-002", "reason": "the title is settled next sprint"},
		},
	}
	if change != nil {
		change(body)
	}
	return jsonOf(body)
}

func jsonOf(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// What a publisher prepares a release with reaches the use case as it was
// said: the revision, the check the release rests on and the waivers in the
// order they were named, for the asset of the path and the caller of the
// token. The answer is the release with its waivers, which names no review,
// and where to read it; one that was recorded already is answered as it is.
func TestAReleaseIsPreparedOnACheckWithItsWaivers(t *testing.T) {
	prepare := call{Method: http.MethodPost, Target: releasesOfAsset, Token: alice.Subject, Body: preparation(nil)}
	release := preparedRelease()
	fake := &releasePreparations{preparation: registry.Preparation{Release: release, Created: true}}
	recorder := newReleaseService(t, fake).do(t, prepare)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", recorder.Code, recorder.Body)
	}
	if len(fake.asked) != 1 || fake.asked[0].CorrelationID == "" {
		t.Fatalf("the use case was asked %+v, want one preparation under the identifier of the request", fake.asked)
	}
	asked := fake.asked[0]
	asked.CorrelationID = ""
	want := registry.PrepareRelease{
		ScopeQuery: registryports.ScopeQuery{Caller: registrydomain.Principal(alice), ProductID: productID, AssetID: apiAssetID},
		RevisionID: revisionID, CheckRunID: checkRunID, Version: "1.0.0",
		Waivers: []registrydomain.Waiver{
			{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet"},
			{RuleID: "F04-GOV-002", Reason: "the title is settled next sprint"},
		},
	}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("the use case was asked\n %+v\nwant\n %+v", asked, want)
	}
	shown := members(t, recorder)
	if _, named := shown["review_id"]; named || shown["check_run_id"] != checkRunID || !reflect.DeepEqual(shown["waivers"], []any{
		map[string]any{"rule_id": "F04-GOV-002", "reason": "the title is settled next sprint"},
		map[string]any{"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet"},
	}) {
		t.Errorf("the release is answered as %v, want its check and its waivers and no review", shown)
	}
	if got := recorder.Header().Get("Location"); got != releasesOfAsset+"/"+releaseID || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the headers of a prepared release: %v", recorder.Header())
	}

	// A release that waives nothing is prepared without waivers and says it
	// waives nothing, and one prepared already is answered as it is.
	plain := preparedRelease()
	plain.Waivers = nil
	fake = &releasePreparations{preparation: registry.Preparation{Release: plain}}
	recorder = newReleaseService(t, fake).do(t, call{Method: http.MethodPost, Target: releasesOfAsset, Token: alice.Subject,
		Body: preparation(func(body map[string]any) { delete(body, "waivers") })})
	if got := members(t, recorder); recorder.Code != http.StatusOK || !reflect.DeepEqual(got["waivers"], []any{}) ||
		len(fake.asked) != 1 || fake.asked[0].Waivers != nil || recorder.Header().Get("Location") != "" {
		t.Errorf("a release that waives nothing: %d %s, asked %+v", recorder.Code, recorder.Body, fake.asked)
	}

	// What the contract does not state is refused before the use case.
	firstWaiver := func(change func(map[string]any)) func(map[string]any) {
		return func(body map[string]any) { change(body["waivers"].([]any)[0].(map[string]any)) }
	}
	many := make([]any, 101)
	for i := range many {
		many[i] = map[string]any{"rule_id": fmt.Sprintf("F04-GOV-%03d", i), "reason": "accepted"}
	}
	refused := map[string]string{
		"no check":                          preparation(func(body map[string]any) { delete(body, "check_run_id") }),
		"a check that is no identifier":     preparation(func(body map[string]any) { body["check_run_id"] = "latest" }),
		"a review instead of a check":       preparation(func(body map[string]any) { delete(body, "check_run_id"); body["review_id"] = reviewID }),
		"a review with the check":           preparation(func(body map[string]any) { body["review_id"] = reviewID }),
		"waivers that are no list":          preparation(func(body map[string]any) { body["waivers"] = "F04-GOV-003" }),
		"more waivers than there are rules": preparation(func(body map[string]any) { body["waivers"] = many }),
		"a waiver of no rule":               preparation(firstWaiver(func(w map[string]any) { delete(w, "rule_id") })),
		"a waiver of what is no rule":       preparation(firstWaiver(func(w map[string]any) { w["rule_id"] = "every rule" })),
		"a waiver without a reason":         preparation(firstWaiver(func(w map[string]any) { delete(w, "reason") })),
		"a waiver whose reason is nothing":  preparation(firstWaiver(func(w map[string]any) { w["reason"] = "" })),
		"a reason longer than allowed":      preparation(firstWaiver(func(w map[string]any) { w["reason"] = strings.Repeat("r", 1001) })),
		"a reason of two lines":             preparation(firstWaiver(func(w map[string]any) { w["reason"] = "settled\nnext sprint" })),
		"a waiver that runs out":            preparation(firstWaiver(func(w map[string]any) { w["expires_at"] = createdAt.Format(time.RFC3339) })),
		"a waiver that says what makes up for it": preparation(firstWaiver(func(w map[string]any) {
			w["compensating_control"] = "the portal shows the asset name"
		})),
	}
	for name, body := range refused {
		t.Run(name, func(t *testing.T) {
			fake := &releasePreparations{preparation: registry.Preparation{Release: preparedRelease(), Created: true}}
			wantRefusal(t, newReleaseService(t, fake).do(t, call{Method: http.MethodPost, Target: releasesOfAsset, Token: alice.Subject, Body: body}),
				http.StatusBadRequest, CodeRequestInvalid)
			if len(fake.asked) != 0 {
				t.Errorf("the use case was asked %+v", fake.asked)
			}
		})
	}
}

// A release prepared under an approval, before releases were prepared on a
// check alone, names the review that gave it, and says it waives nothing of
// its own: its waivers are the approval's.
func TestAReleasePreparedUnderAnApprovalNamesItsReview(t *testing.T) {
	release := preparedOn(fromGit()).Release
	fake := &releasePreparations{preparation: registry.Preparation{Release: release}}
	recorder := newReleaseService(t, fake).do(t, call{Method: http.MethodPost, Target: releasesOfAsset, Token: alice.Subject, Body: preparation(nil)})
	if got := members(t, recorder); recorder.Code != http.StatusOK || got["review_id"] != reviewID || !reflect.DeepEqual(got["waivers"], []any{}) {
		t.Errorf("a release prepared under an approval: %d %s", recorder.Code, recorder.Body)
	}
}

// What preparing a release refuses is a problem of its own code: the check
// is not one a release rests on now, or the waivers do not cover exactly the
// rules that need one. Each says what to do about it.
func TestWhatAPreparationRefusesIsAProblemOfItsOwnCode(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"a check nobody recorded": {checks.ErrCheckRunNotFound, http.StatusNotFound, "CHECK_RUN_NOT_FOUND", ""},
		"a check of another policy": {reviewdomain.ErrCheckStale, http.StatusConflict, "POLICY_STALE",
			"The check was not made under the policy and by the tool versions a check is made with now. Run the check again."},
		"rules nothing waives that failed": {fmt.Errorf("%w: F04-GOV-004, F04-GOV-010", reviewdomain.ErrReleaseBlocked), http.StatusConflict, "RELEASE_BLOCKED",
			"In the check, rules that nothing waives failed or could not be judged. No release is prepared on such a check."},
		"rules that are not waived": {fmt.Errorf("%w: F04-GOV-002, F04-GOV-003", reviewdomain.ErrWaiverMissing), http.StatusConflict, "WAIVER_REQUIRED",
			"Rules failed or could not be judged in the check. A release waives each of them by name."},
		"a waiver of a rule nothing waives": {fmt.Errorf("%w: F04-GOV-004", reviewdomain.ErrWaiverForbidden), http.StatusUnprocessableEntity, "WAIVER_FORBIDDEN",
			"A rule that nothing waives cannot be waived."},
		"a waiver of a rule that passed": {fmt.Errorf("%w: rule F04-GOV-002 did not fail and is not waived", reviewdomain.ErrInvalidWaiver),
			http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""},
		"a rule that blocks and was not run": {fmt.Errorf("%w: F04-GOV-011", reviewdomain.ErrReleaseCheckMissing), http.StatusConflict, "RELEASE_CHECK_INCOMPLETE",
			"A mandatory contract check lacks evidence."},
		"a version that names another release": {registry.ErrReleaseConflict, http.StatusConflict, "RELEASE_VERSION_CONFLICT", "That version already names another release."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newReleaseService(t, &releasePreparations{err: c.err})
			wantProblem(t, s.do(t, call{Method: http.MethodPost, Target: releasesOfAsset, Token: alice.Subject, Body: preparation(nil)}),
				c.status, c.code, c.detail)
		})
	}
	// A check that is not what was recorded is a failure of the service.
	s := newReleaseService(t, &releasePreparations{err: fmt.Errorf("check run %s: %w", checkRunID, reviewdomain.ErrCheckRunDamaged)})
	wantProblem(t, s.do(t, call{Method: http.MethodPost, Target: releasesOfAsset, Token: alice.Subject, Body: preparation(nil)}),
		http.StatusInternalServerError, CodeInternalError, "")
}
