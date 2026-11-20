package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var (
	comparator = domain.Tool{Name: "fabric-openapi-compatibility", Version: "1"}
	current    = []domain.Tool{linter, comparator}
	carol      = domain.Principal{Issuer: "https://issuer.example", Subject: "carol"}
	erin       = domain.Principal{Issuer: "https://issuer.example", Subject: "erin"}
	asked      = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	partner    = domain.Target{Audience: "partner", Environment: "sandbox"}
)

// The policy of the review tests: rule 1 is one nothing waives, rule 2
// blocks and can be waived, rule 3 warns, and rule 4 is one no tool of
// these tests evaluates.
func reviewPolicy(t *testing.T) domain.Policy {
	t.Helper()
	mandatory := rule("R-RULE-001", domain.SeverityBlock)
	mandatory.Category, mandatory.Waivable = domain.CategorySecurityMandatory, false
	policy, err := domain.NewPolicy("review-rules", []domain.Rule{
		mandatory, rule("R-RULE-002", domain.SeverityBlock), rule("R-RULE-003", domain.SeverityWarn), rule("R-RULE-004", domain.SeverityBlock),
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// checked is a check of the revision of these tests under the review policy.
// A rule that is given no observation passed, and rule 4 is never run.
func checked(t *testing.T, observed ...domain.Observation) domain.CheckRun {
	t.Helper()
	said := map[string]domain.Observation{"R-RULE-001": {RuleID: "R-RULE-001"}, "R-RULE-002": {RuleID: "R-RULE-002"}, "R-RULE-003": {RuleID: "R-RULE-003"}}
	for _, observation := range observed {
		said[observation.RuleID] = observation
	}
	run, err := domain.Complete(draft(), reviewPolicy(t), shipments(),
		linter, []domain.Observation{said["R-RULE-001"], said["R-RULE-002"], said["R-RULE-003"]}, completed)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func found(ruleID string) domain.Observation {
	return domain.Observation{RuleID: ruleID, Findings: []domain.Finding{{
		RuleID: ruleID, Path: "api/openapi.yaml", Line: 3, Message: "the rule is broken here; mend it",
	}}}
}

func undecided(ruleID string) domain.Observation {
	return domain.Observation{RuleID: ruleID, Undecided: true}
}

// The rules that stand between a revision and an approval are the ones that
// failed or could not be judged, apart by whether anything waives them. A
// rule that warns stands in no way, and neither does one that was not run.
func TestTheBlockersOfACheckAreTheRulesThatDidNotPass(t *testing.T) {
	cases := map[string]struct {
		observed             []domain.Observation
		unwaivable, waivable []string
	}{
		"every evaluated rule passed":                {},
		"a rule that warns":                          {observed: []domain.Observation{found("R-RULE-003")}},
		"a waivable rule that failed":                {observed: []domain.Observation{found("R-RULE-002")}, waivable: []string{"R-RULE-002"}},
		"a waivable rule that could not be judged":   {observed: []domain.Observation{undecided("R-RULE-002")}, waivable: []string{"R-RULE-002"}},
		"a rule nothing waives that failed":          {observed: []domain.Observation{found("R-RULE-001")}, unwaivable: []string{"R-RULE-001"}},
		"a rule nothing waives that was not judged":  {observed: []domain.Observation{undecided("R-RULE-001")}, unwaivable: []string{"R-RULE-001"}},
		"one of each, and a warning":                 {observed: []domain.Observation{found("R-RULE-001"), undecided("R-RULE-002"), found("R-RULE-003")}, unwaivable: []string{"R-RULE-001"}, waivable: []string{"R-RULE-002"}},
		"a warning rule that could not be judged":    {observed: []domain.Observation{undecided("R-RULE-003")}, waivable: []string{"R-RULE-003"}},
		"a warning rule undecided with its findings": {observed: []domain.Observation{{RuleID: "R-RULE-003", Undecided: true, Findings: found("R-RULE-003").Findings}}, waivable: []string{"R-RULE-003"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			run := checked(t, c.observed...)
			if run.Outcomes[3].Status != domain.StatusNotRun {
				t.Fatalf("rule 4 is %s in these tests, want it not run", run.Outcomes[3].Status)
			}
			unwaivable, waivable := domain.Blockers(run)
			if !reflect.DeepEqual(unwaivable, c.unwaivable) || !reflect.DeepEqual(waivable, c.waivable) {
				t.Errorf("unwaivable %v and waivable %v, want %v and %v", unwaivable, waivable, c.unwaivable, c.waivable)
			}
		})
	}
}

// A review is asked on a check a decision can rest on now: one made under
// the policy and by the tools of today, in which no rule that nothing waives
// failed or went unjudged. It says what it is of, on which check, for which
// scope, who asked and when, and it keeps its scope in one order.
func TestAReviewIsAskedOnACheckADecisionCanRestOn(t *testing.T) {
	policy := reviewPolicy(t)
	scope := []domain.Target{
		{Audience: "public", Environment: "sandbox"}, partner, {Audience: "internal", Environment: "production"},
		{Audience: "partner", Environment: "production"},
	}
	ordered := []domain.Target{
		{Audience: "internal", Environment: "production"}, {Audience: "partner", Environment: "production"}, partner,
		{Audience: "public", Environment: "sandbox"},
	}

	accepted := map[string]domain.CheckRun{
		"a check in which every evaluated rule passed":      checked(t),
		"a check with a waivable failure and a warning":     checked(t, found("R-RULE-002"), found("R-RULE-003")),
		"a check with a waivable rule that was not judged":  checked(t, undecided("R-RULE-002")),
		"a check with rules no tool evaluates and no other": checked(t),
	}
	for name, run := range accepted {
		t.Run(name, func(t *testing.T) {
			at := asked.In(time.FixedZone("UTC+8", 8*60*60)).Add(789 * time.Nanosecond)
			request, err := domain.RequestReview("review-1", run, policy, current, scope, "ready for partners", carol, at)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.ReviewRequest{
				ID: "review-1", Revision: run.Revision, CheckRunID: run.ID, InputDigest: run.InputDigest, Scope: ordered,
				Note: "ready for partners", RequestedBy: carol, RequestedAt: asked,
			}
			if !reflect.DeepEqual(request, want) || request.RequestedAt.Location() != time.UTC {
				t.Errorf("asked as %+v, want %+v in UTC", request, want)
			}
			if scope[0].Audience != "public" {
				t.Error("the scope that was handed over was reordered")
			}
		})
	}
	if request, err := domain.RequestReview("review-1", checked(t), policy, current, []domain.Target{partner}, "", carol, asked); err != nil || request.Note != "" {
		t.Errorf("a review without a note: %+v, %v", request, err)
	}

	stale, blocked, invalid := domain.ErrCheckStale, domain.ErrReviewBlocked, domain.ErrInvalidReview
	one := []domain.Target{partner}
	every := []domain.Target{
		{Audience: "public", Environment: "sandbox"}, {Audience: "public", Environment: "production"},
		{Audience: "partner", Environment: "sandbox"}, {Audience: "partner", Environment: "production"},
		{Audience: "internal", Environment: "sandbox"}, {Audience: "internal", Environment: "production"},
	}
	if _, err := domain.RequestReview("review-1", checked(t), policy, current, every, "", carol, asked); err != nil {
		t.Errorf("every target there is: %v", err)
	}
	otherPolicy := teamPolicy(t)
	refused := map[string]struct {
		run    domain.CheckRun
		policy domain.Policy
		tools  []domain.Tool
		scope  []domain.Target
		id     string
		note   string
		by     domain.Principal
		want   error
		names  string
	}{
		"a check under another policy":             {run: checked(t), policy: otherPolicy, tools: current, scope: one, want: stale},
		"a check by another version of the tool":   {run: checked(t), policy: policy, tools: []domain.Tool{{Name: linter.Name, Version: "2"}, comparator}, scope: one, want: stale},
		"a check by a tool there is no more":       {run: checked(t), policy: policy, tools: []domain.Tool{comparator}, scope: one, want: stale},
		"no tool of today at all":                  {run: checked(t), policy: policy, scope: one, want: stale},
		"a stale check that is blocked as well":    {run: checked(t, found("R-RULE-001")), policy: otherPolicy, tools: current, scope: one, want: stale},
		"a rule nothing waives failed":             {run: checked(t, found("R-RULE-001")), policy: policy, tools: current, scope: one, want: blocked, names: "R-RULE-001"},
		"a rule nothing waives was not judged":     {run: checked(t, undecided("R-RULE-001"), found("R-RULE-002")), policy: policy, tools: current, scope: one, want: blocked, names: "R-RULE-001"},
		"no scope":                                 {run: checked(t), policy: policy, tools: current, want: invalid},
		"a target named twice":                     {run: checked(t), policy: policy, tools: current, scope: []domain.Target{partner, partner}, want: invalid},
		"an audience there is not":                 {run: checked(t), policy: policy, tools: current, scope: []domain.Target{{Audience: "customers", Environment: "sandbox"}}, want: invalid},
		"an environment there is not":              {run: checked(t), policy: policy, tools: current, scope: []domain.Target{{Audience: "partner", Environment: "staging"}}, want: invalid},
		"an audience in upper case":                {run: checked(t), policy: policy, tools: current, scope: []domain.Target{{Audience: "Partner", Environment: "sandbox"}}, want: invalid},
		"more targets than there are":              {run: checked(t), policy: policy, tools: current, scope: append(every, partner), want: invalid},
		"a scope that is invalid on a stale check": {run: checked(t), policy: otherPolicy, tools: current, want: invalid},
		"a note of two lines":                      {run: checked(t), policy: policy, tools: current, scope: one, note: "ready\nfor partners", want: invalid},
		"a note with space around it":              {run: checked(t), policy: policy, tools: current, scope: one, note: " ready", want: invalid},
		"a note longer than allowed":               {run: checked(t), policy: policy, tools: current, scope: one, note: strings.Repeat("评", domain.MaxReviewNote+1), want: invalid},
		"no identifier":                            {run: checked(t), policy: policy, tools: current, scope: one, id: "-", want: invalid},
		"nobody who asks":                          {run: checked(t), policy: policy, tools: current, scope: one, by: domain.Principal{Issuer: "https://issuer.example"}, want: invalid},
		"a check that names no revision":           {run: domain.CheckRun{ID: "check-1"}, policy: policy, tools: current, scope: one, want: invalid},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			id, by := "review-1", carol
			if c.id == "-" {
				id = ""
			}
			if c.by != (domain.Principal{}) {
				by = c.by
			}
			request, err := domain.RequestReview(id, c.run, c.policy, c.tools, c.scope, c.note, by, asked)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(request, domain.ReviewRequest{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", request, err, c.want)
			}
			if c.names != "" && !strings.HasSuffix(err.Error(), ": "+c.names) {
				t.Errorf("the refusal is %q, want it to name %s and no other rule", err, c.names)
			}
		})
	}
	if _, err := domain.RequestReview("review-1", checked(t), policy, current, one, strings.Repeat("评", domain.MaxReviewNote), carol, asked); err != nil {
		t.Errorf("a note of the longest length: %v", err)
	}
}

// Whoever asked for a review takes the request back, while it is open. The
// conclusion says who, when and why, and it never precedes the request.
func TestAReviewIsWithdrawnByWhoAskedForItWhileItIsOpen(t *testing.T) {
	request, err := domain.RequestReview("review-1", checked(t), reviewPolicy(t), current, []domain.Target{partner}, "", carol, asked)
	if err != nil {
		t.Fatal(err)
	}
	open := domain.Review{Request: request}
	if open.Status() != domain.ReviewOpen || domain.ReviewOpen != "IN_REVIEW" {
		t.Fatalf("a review without a conclusion is %s, want IN_REVIEW", open.Status())
	}

	later := asked.Add(time.Hour).In(time.FixedZone("UTC+8", 8*60*60)).Add(321 * time.Nanosecond)
	conclusion, err := domain.Withdraw(open, carol, "the baseline was the wrong one", later)
	want := domain.Conclusion{
		ReviewID: "review-1", Outcome: domain.ReviewWithdrawn, Reason: "the baseline was the wrong one",
		ConcludedBy: carol, ConcludedAt: asked.Add(time.Hour),
	}
	if err != nil || !reflect.DeepEqual(conclusion, want) || conclusion.ConcludedAt.Location() != time.UTC {
		t.Fatalf("withdrawn as %+v, want %+v in UTC: %v", conclusion, want, err)
	}
	concluded := domain.Review{Request: request, Conclusion: &conclusion}
	if concluded.Status() != "WITHDRAWN" {
		t.Errorf("a withdrawn review is %s", concluded.Status())
	}
	if silent, err := domain.Withdraw(open, carol, "", later); err != nil || silent.Reason != "" {
		t.Errorf("a withdrawal without a reason: %+v, %v", silent, err)
	}
	// A clock that was set back does not make a review end before it began.
	if early, err := domain.Withdraw(open, carol, "", asked.Add(-time.Hour)); err != nil || !early.ConcludedAt.Equal(asked) {
		t.Errorf("withdrawn at %v with the clock set back, want the instant it was asked for %v: %v", early.ConcludedAt, asked, err)
	}

	refused := map[string]struct {
		review domain.Review
		by     domain.Principal
		reason string
		want   error
	}{
		"another author":                        {open, erin, "", domain.ErrNotRequester},
		"the same subject under another issuer": {open, domain.Principal{Issuer: "https://other.example", Subject: "carol"}, "", domain.ErrNotRequester},
		"a review that is concluded":            {concluded, carol, "", domain.ErrReviewConcluded},
		"a concluded review, by someone else":   {concluded, erin, "", domain.ErrReviewConcluded},
		"a reason of two lines":                 {open, carol, "wrong\nbaseline", domain.ErrInvalidReview},
		"a reason longer than allowed":          {open, carol, strings.Repeat("r", domain.MaxReviewReason+1), domain.ErrInvalidReview},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			if got, err := domain.Withdraw(c.review, c.by, c.reason, later); !errors.Is(err, c.want) || !reflect.DeepEqual(got, domain.Conclusion{}) {
				t.Errorf("got %+v and %v, want nothing and %v", got, err, c.want)
			}
		})
	}
}

// A review is read back from storage only as one the model could have
// recorded: a request that says what it is of, with a scope in its order,
// and a conclusion there is that belongs to it and does not precede it.
func TestAStoredReviewIsReadBackOnlyAsItWasRecorded(t *testing.T) {
	request, err := domain.RequestReview("review-1", checked(t), reviewPolicy(t), current,
		[]domain.Target{partner, {Audience: "internal", Environment: "sandbox"}}, "", carol, asked)
	if err != nil {
		t.Fatal(err)
	}
	conclusion, err := domain.Withdraw(domain.Review{Request: request}, carol, "", asked.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for name, review := range map[string]domain.Review{
		"a request":                    {Request: request},
		"a request and its conclusion": {Request: request, Conclusion: &conclusion},
	} {
		if got, err := domain.RecordedReview(review); err != nil || !reflect.DeepEqual(got, review) {
			t.Errorf("%s: read back as %+v, want %+v: %v", name, got, review, err)
		}
	}

	damaged := map[string]func(*domain.Review){
		"no identifier":            func(r *domain.Review) { r.Request.ID = "" },
		"no check":                 func(r *domain.Review) { r.Request.CheckRunID = "" },
		"no revision":              func(r *domain.Review) { r.Request.Revision.RevisionID = "" },
		"a digest that is none":    func(r *domain.Review) { r.Request.InputDigest = "sha256:abc" },
		"nobody who asked":         func(r *domain.Review) { r.Request.RequestedBy.Subject = "" },
		"no scope":                 func(r *domain.Review) { r.Request.Scope = nil },
		"a scope out of its order": func(r *domain.Review) { r.Request.Scope = []domain.Target{r.Request.Scope[1], r.Request.Scope[0]} },
		"a target twice":           func(r *domain.Review) { r.Request.Scope = []domain.Target{partner, partner} },
		"a target there is not": func(r *domain.Review) {
			r.Request.Scope = []domain.Target{{Audience: "partner", Environment: "staging"}}
		},
		"an outcome there is not":         func(r *domain.Review) { r.Conclusion.Outcome = "ESCALATED" },
		"a conclusion of another review":  func(r *domain.Review) { r.Conclusion.ReviewID = "review-2" },
		"a conclusion before the request": func(r *domain.Review) { r.Conclusion.ConcludedAt = asked.Add(-time.Second) },
		"nobody who concluded":            func(r *domain.Review) { r.Conclusion.ConcludedBy.Issuer = "" },
		"a withdrawal that holds for a time": func(r *domain.Review) {
			r.Conclusion.ValidUntil = asked.Add(time.Hour)
		},
		"a withdrawal that waives a rule": func(r *domain.Review) {
			r.Conclusion.Waivers = []domain.Waiver{{RuleID: "R-RULE-002", Reason: "accepted", ExpiresAt: asked.Add(time.Hour)}}
		},
		"an approval that holds for no time": func(r *domain.Review) { r.Conclusion.Outcome = domain.ReviewApproved },
		"an approval that ended before it was given": func(r *domain.Review) {
			r.Conclusion.Outcome, r.Conclusion.ValidUntil = domain.ReviewApproved, asked
		},
		"a rejection without a reason": func(r *domain.Review) { r.Conclusion.Outcome = domain.ReviewRejected },
		"a rejection that holds for a time": func(r *domain.Review) {
			r.Conclusion.Outcome, r.Conclusion.Reason, r.Conclusion.ValidUntil = domain.ReviewRejected, "not yet", asked.Add(time.Hour)
		},
	}
	for name, damage := range damaged {
		t.Run(name, func(t *testing.T) {
			copied := conclusion
			review := domain.Review{Request: request, Conclusion: &copied}
			review.Request.Scope = append([]domain.Target(nil), request.Scope...)
			damage(&review)
			if got, err := domain.RecordedReview(review); !errors.Is(err, domain.ErrReviewDamaged) || !reflect.DeepEqual(got, domain.Review{}) {
				t.Errorf("read back as %+v and %v, want nothing and %v", got, err, domain.ErrReviewDamaged)
			}
		})
	}
}
