package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	reviewdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	reviewports "go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

const (
	otherWorkspaceID = "d6e7f8a9-b0c1-4d2e-9f3a-4b5c6d7e8f90"
	// workspaceReviews is where the reviews of the workspace are listed as a
	// queue, and queueList the list a page token of it is of.
	workspaceReviews = "/v1/products/" + productID + "/workspaces/" + workspaceID + "/reviews"
	queueList        = "workspace-reviews/" + productID + "/" + workspaceID
)

// workspaceQueue stands in for the use case that reads the reviews of a
// workspace. It answers what the test set and keeps what it was asked. The
// other use cases of a review are not reached by these tests.
type workspaceQueue struct {
	PolicyReview

	page checks.ReviewPage
	err  error

	listed []checks.ListWorkspaceReviews
}

func (q *workspaceQueue) ListWorkspaceReviews(_ context.Context, query checks.ListWorkspaceReviews) (checks.ReviewPage, error) {
	q.listed = append(q.listed, query)
	return q.page, q.err
}

func newQueueService(t *testing.T, fake *workspaceQueue) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: unusedCatalog{}, Registry: unusedRegistry{}, PolicyReview: fake,
		Releases: unusedReleases{}, Publication: unusedPublications{}, Audit: unusedAudit{},
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

// A review as it is read alone: the answer of getRevisionReview for it.
func readAlone(t *testing.T, review reviewdomain.Review) map[string]any {
	t.Helper()

	recorder := newReviewService(t, &contractReviews{review: review}).do(t, call{Method: http.MethodGet, Target: reviews + "/" + reviewID, Token: alice.Subject})
	if recorder.Code != http.StatusOK {
		t.Fatalf("read a review alone: %d %s", recorder.Code, recorder.Body)
	}
	return members(t, recorder)
}

// What a caller asks of the queue reaches the use case as it was said: the
// workspace and the product of the path, the caller of the token, who asked
// for the reviews and what they stand at, how many a page holds and where it
// starts. The answer is the reviews as the contract states each of them, and
// each is the review that is read alone, whatever state it is in: the same
// function shows them. The token of the page that follows is handed out for as
// long as one does.
func TestAWorkspaceQueueHandsItsRequestToItsUseCaseAndAnswersAPage(t *testing.T) {
	states := []reviewdomain.Review{askedReview(), withdrawnReview(), approvedReview(), rejectedReview()}
	supplementary := askedReview()
	supplementary.Request.Supplementary = true
	states = append(states, supplementary)
	position := reviewports.Position{At: createdAt, ID: reviewID}
	fake := &workspaceQueue{page: checks.ReviewPage{Items: states, Next: &position}}
	s := newQueueService(t, fake)

	target := workspaceReviews + "?page_size=5&status=APPROVED&requested_by_issuer=" + url.QueryEscape(issuer) + "&requested_by_subject=alice"
	recorder := s.do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject})
	listed := members(t, recorder)
	token, _ := listed["next_page_token"].(string)
	if recorder.Code != http.StatusOK || token == "" || recorder.Header().Get("Cache-Control") != "no-store" || len(listed) != 2 {
		t.Fatalf("listed: %d %s %v", recorder.Code, recorder.Body, recorder.Header())
	}
	items, _ := listed["items"].([]any)
	if len(items) != len(states) {
		t.Fatalf("the page holds %d reviews, want %d", len(items), len(states))
	}
	for index, state := range states {
		if got, want := items[index], readAlone(t, state); !reflect.DeepEqual(got, want) {
			t.Errorf("review %d of the page is\n %v\nread alone it is\n %v", index, got, want)
		}
	}
	if !reflect.DeepEqual(items[0], shownReview(nil)) || !reflect.DeepEqual(items[2], shownReview(shownApproval())) ||
		!reflect.DeepEqual(items[3], shownReview(shownRejection())) {
		t.Errorf("the page shows\n %v\nwant the reviews as the contract states them", items)
	}

	if len(fake.listed) != 1 {
		t.Fatalf("the use case was asked %d times, want once", len(fake.listed))
	}
	asked := fake.listed[0]
	if asked.CorrelationID == "" || !uuid.MatchString(asked.CorrelationID) {
		t.Errorf("the use case was given the correlation %q, want the identifier of the request", asked.CorrelationID)
	}
	asked.CorrelationID = ""
	want := checks.ListWorkspaceReviews{
		Caller:    reviewdomain.Principal{Issuer: alice.Issuer, Subject: alice.Subject},
		ProductID: productID, WorkspaceID: workspaceID,
		Status: "APPROVED", RequestedByIssuer: issuer, RequestedBySubject: "alice", PageSize: 5,
	}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("the use case was asked\n %+v\nwant\n %+v", asked, want)
	}

	// The token is the position of the last review of the page, in the list of
	// this workspace, and brings the next page from there.
	if got, err := decodePageToken(queueList, token); err != nil || !got.At.Equal(createdAt) || got.ID != reviewID {
		t.Errorf("the token names %+v (%v), want the position the use case gave", got, err)
	}
	fake.page = checks.ReviewPage{}
	recorder = s.do(t, call{Method: http.MethodGet, Target: workspaceReviews + "?page_token=" + token, Token: alice.Subject})
	last := members(t, recorder)
	if _, more := last["next_page_token"]; recorder.Code != http.StatusOK || more || !reflect.DeepEqual(last["items"], []any{}) {
		t.Errorf("the page behind the token: %d %s, want an empty list and no token", recorder.Code, recorder.Body)
	}
	if second := fake.listed[1]; second.After == nil || !second.After.At.Equal(createdAt) || second.After.ID != reviewID ||
		second.PageSize != checks.DefaultQueuePage || second.Status != "" || second.RequestedByIssuer != "" || second.RequestedBySubject != "" {
		t.Errorf("the use case was asked %+v behind the token, want the default page, no filter and the position of the token", second)
	}
}

// Each filter reaches the use case by itself and with the others, and none
// that was not said does.
func TestTheFiltersOfAWorkspaceQueueReachItsUseCaseAsTheyWereSaid(t *testing.T) {
	for name, c := range map[string]struct {
		query                   string
		status, issuer, subject string
	}{
		"none":                     {"", "", "", ""},
		"IN_REVIEW alone":          {"?status=IN_REVIEW", "IN_REVIEW", "", ""},
		"WITHDRAWN alone":          {"?status=WITHDRAWN", "WITHDRAWN", "", ""},
		"APPROVED alone":           {"?status=APPROVED", "APPROVED", "", ""},
		"REJECTED alone":           {"?status=REJECTED", "REJECTED", "", ""},
		"an issuer alone":          {"?requested_by_issuer=" + url.QueryEscape(issuer), "", issuer, ""},
		"a subject alone":          {"?requested_by_subject=alice", "", "", "alice"},
		"an issuer and a subject":  {"?requested_by_issuer=" + url.QueryEscape(issuer) + "&requested_by_subject=alice", "", issuer, "alice"},
		"a subject with a space":   {"?requested_by_subject=" + url.QueryEscape("alice of the team"), "", "", "alice of the team"},
		"every filter, any order":  {"?requested_by_subject=alice&status=REJECTED&requested_by_issuer=" + url.QueryEscape(issuer), "REJECTED", issuer, "alice"},
		"a subject that is a name": {"?requested_by_subject=" + url.QueryEscape("卡罗尔"), "", "", "卡罗尔"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &workspaceQueue{}
			recorder := newQueueService(t, fake).do(t, call{Method: http.MethodGet, Target: workspaceReviews + c.query, Token: alice.Subject})
			if recorder.Code != http.StatusOK || len(fake.listed) != 1 {
				t.Fatalf("%d %s, the use case was asked %d times", recorder.Code, recorder.Body, len(fake.listed))
			}
			if got := fake.listed[0]; got.Status != c.status || got.RequestedByIssuer != c.issuer || got.RequestedBySubject != c.subject ||
				got.PageSize != checks.DefaultQueuePage || got.After != nil {
				t.Errorf("the use case was asked for %+v, want the status %q, the issuer %q, the subject %q, the default page from the start",
					got, c.status, c.issuer, c.subject)
			}
		})
	}
}

// What the contract does not state is refused before the use case is asked:
// an identifier that is none, a status no review has, a page of none or of
// more than the largest, a principal that is no reference, a parameter that
// is said twice, that is not declared or that has no value.
func TestWhatTheContractDoesNotStateOfAWorkspaceQueueIsRefusedBeforeItsUseCase(t *testing.T) {
	longer := strings.Repeat("a", 256)
	for name, target := range map[string]string{
		"a workspace that is no identifier":      "/v1/products/" + productID + "/workspaces/latest/reviews",
		"a workspace in capitals":                "/v1/products/" + productID + "/workspaces/" + strings.ToUpper(workspaceID) + "/reviews",
		"a product that is no identifier":        "/v1/products/cargopai/workspaces/" + workspaceID + "/reviews",
		"a status in lower case":                 workspaceReviews + "?status=in_review",
		"a status no review has":                 workspaceReviews + "?status=ESCALATED",
		"the status of a revision":               workspaceReviews + "?status=DRAFT",
		"a status that is said twice":            workspaceReviews + "?status=APPROVED&status=REJECTED",
		"a status without a value":               workspaceReviews + "?status=",
		"an issuer without a value":              workspaceReviews + "?requested_by_issuer=",
		"a subject without a value":              workspaceReviews + "?requested_by_subject=",
		"a subject of a control character":       workspaceReviews + "?requested_by_subject=alice%0A",
		"a subject that is too long":             workspaceReviews + "?requested_by_subject=" + longer,
		"an issuer that is too long":             workspaceReviews + "?requested_by_issuer=" + longer,
		"a page of none":                         workspaceReviews + "?page_size=0",
		"a page of more than the largest":        workspaceReviews + "?page_size=101",
		"a page of less than none":               workspaceReviews + "?page_size=-1",
		"a page that is no number":               workspaceReviews + "?page_size=many",
		"a parameter nobody declared":            workspaceReviews + "?workspace_id=" + otherWorkspaceID,
		"a filter that is named another way":     workspaceReviews + "?requestedBy=alice",
		"an offset":                              workspaceReviews + "?offset=10",
		"a status of a revision, and a good one": workspaceReviews + "?status=APPROVED&state=APPROVED",
	} {
		t.Run(name, func(t *testing.T) {
			fake := &workspaceQueue{}
			wantRefusal(t, newQueueService(t, fake).do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject}), http.StatusBadRequest, CodeRequestInvalid)
			if len(fake.listed) != 0 {
				t.Errorf("the use case was asked %+v", fake.listed)
			}
		})
	}
	// The smallest and the largest page, and the longest reference, are the
	// contract's, and are answered.
	for _, target := range []string{
		workspaceReviews + "?page_size=1", workspaceReviews + "?page_size=100",
		workspaceReviews + "?requested_by_subject=" + strings.Repeat("a", 255),
	} {
		fake := &workspaceQueue{}
		if recorder := newQueueService(t, fake).do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject}); recorder.Code != http.StatusOK || len(fake.listed) != 1 {
			t.Errorf("%s: %d %s, the use case was asked %d times", target, recorder.Code, recorder.Body, len(fake.listed))
		}
	}
}

// A request that names no size is asked of the use case for the default page of
// the contract: the validator does not fill it in for a handler that is called
// as if it had not run, and the use case does not choose. The size is the one
// constant the use case and the handler share, and the contract declares it as
// the default, the least and the most of the size of a page: the three are held
// together, so that none can be changed without the others.
func TestAWorkspaceQueueThatNamesNoPageSizeIsAskedForTheDefaultPageOfTheContract(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	ctx := context.WithValue(context.Background(), stateKey{}, &requestState{correlationID: "correlation-1", principal: alice, authenticated: true})
	fake := &workspaceQueue{}
	if _, err := (operations{policyReview: fake}).ListWorkspaceReviews(ctx, ListWorkspaceReviewsRequestObject{ProductID: productID, WorkspaceID: workspaceID}); err != nil {
		t.Fatalf("list the reviews without a page size: %v", err)
	}
	if len(fake.listed) != 1 || fake.listed[0].PageSize != checks.DefaultQueuePage || fake.listed[0].After != nil {
		t.Fatalf("the use case was asked %+v, want the default page of %d from the start", fake.listed, checks.DefaultQueuePage)
	}

	var size *struct{ min, max, fallback string }
	for _, parameter := range contract.Paths.Find("/v1/products/{product_id}/workspaces/{workspace_id}/reviews").Get.Parameters {
		if parameter.Value.Name == "page_size" {
			schema := parameter.Value.Schema.Value
			size = &struct{ min, max, fallback string }{fmt.Sprint(*schema.Min), fmt.Sprint(*schema.Max), fmt.Sprint(schema.Default)}
		}
	}
	if size == nil || size.fallback != fmt.Sprint(checks.DefaultQueuePage) || size.min != "1" || size.max != fmt.Sprint(checks.MaxQueuePage) {
		t.Errorf("the contract declares the size of a page of the queue as %+v, the use case takes 1 to %d and the handler %d by default",
			size, checks.MaxQueuePage, checks.DefaultQueuePage)
	}
}

// A handler that is called without a principal, as when the operation is wired
// around the validator, fails and does not ask the use case.
func TestAWorkspaceQueueIsNotReadForNobody(t *testing.T) {
	fake := &workspaceQueue{}
	for name, ctx := range map[string]context.Context{
		"no state at all": context.Background(),
		"a request that was not authenticated": context.WithValue(context.Background(), stateKey{},
			&requestState{correlationID: "correlation-1", principal: alice}),
		"a state that is empty": context.WithValue(context.Background(), stateKey{}, &requestState{}),
	} {
		response, err := (operations{policyReview: fake}).ListWorkspaceReviews(ctx, ListWorkspaceReviewsRequestObject{ProductID: productID, WorkspaceID: workspaceID})
		if err == nil || response != nil || len(fake.listed) != 0 {
			t.Errorf("%s: got %v and %v, the use case was asked %+v, want a failure and no question", name, response, err, fake.listed)
		}
	}
}

// A token names a position in the queue of one workspace of one product and in
// no other list: the queue of another workspace, of another product, the
// reviews of a revision, any other list is none. It names no filter: what a page
// is kept to is said with the request, and a token that was handed out under one
// filter brings the page behind its position under another. The token of the
// latest position there can be fits the contract.
func TestThePageTokenOfAWorkspaceQueueIsOfThatQueue(t *testing.T) {
	position := governanceports.Position{At: createdAt, ID: reviewID}
	for name, list := range map[string]string{
		"the queue of another workspace":                  listOfWorkspaceReviews(productID, otherWorkspaceID),
		"the queue of this workspace, of another product": listOfWorkspaceReviews(otherProduct, workspaceID),
		"the reviews of a revision":                       "reviews/" + productID + "/" + apiAssetID + "/" + revisionID,
		"the checks of a revision":                        "check-runs/" + productID + "/" + apiAssetID + "/" + revisionID,
		"the audit records of the product":                listOfAuditRecords(productID),
		"the workspaces of the product":                   listOfWorkspaces,
		"the memberships of the workspace":                listOfMemberships,
		"the assets":                                      listOfAssets,
		"the slots of a target":                           "publication-slots/" + productID + "/" + publicationTargetID,
		"a list that has no name":                         "",
		"the name of the queue with a suffix":             queueList + "/",
		"the name of the queue in capitals":               strings.ToUpper(queueList),
	} {
		t.Run(name, func(t *testing.T) {
			fake := &workspaceQueue{}
			wantProblem(t, newQueueService(t, fake).do(t, call{Method: http.MethodGet, Token: alice.Subject,
				Target: workspaceReviews + "?page_token=" + encodePageToken(list, position)}), http.StatusBadRequest, CodePageTokenInvalid,
				"The page token is not one of this list. Read the list again from its start.")
			if len(fake.listed) != 0 {
				t.Errorf("the use case was asked %+v", fake.listed)
			}
		})
	}
	// And the queue of this workspace is the one a token of it brings.
	fake := &workspaceQueue{}
	s := newQueueService(t, fake)
	for _, query := range []string{"?page_token=", "?status=APPROVED&page_token=", "?requested_by_subject=alice&page_token="} {
		target := workspaceReviews + query + encodePageToken(queueList, position)
		if recorder := s.do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject}); recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, recorder.Code, recorder.Body)
		}
	}
	if len(fake.listed) != 3 {
		t.Fatalf("the use case was asked %d times, want 3", len(fake.listed))
	}
	for index, status := range []string{"", "APPROVED", ""} {
		if got := fake.listed[index]; got.After == nil || !got.After.At.Equal(createdAt) || got.After.ID != reviewID || got.Status != status {
			t.Errorf("the page behind the token under the filter %d: %+v, want its position and the status %q", index, got, status)
		}
	}

	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	taken := contract.Components.Parameters["PageToken"].Value.Schema.Value
	declared := regexp.MustCompile(taken.Pattern)
	latest := governanceports.Position{At: time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC), ID: highestIdentifier}
	token := encodePageToken(queueList, latest)
	if !declared.MatchString(token) || uint64(len(token)) < taken.MinLength || uint64(len(token)) > *taken.MaxLength {
		t.Errorf("the token %q of %d characters is not one the contract takes", token, len(token))
	}
	if got, err := decodePageToken(queueList, token); err != nil || !got.At.Equal(latest.At) || got.ID != latest.ID {
		t.Errorf("the token names %+v (%v), want %+v", got, err, latest)
	}
}

// The queue is read by a caller that has been told apart from the others:
// without one, or with a token nobody accepts, it is refused before any use
// case is asked.
func TestTheWorkspaceQueueNeedsAVerifiedCaller(t *testing.T) {
	for name, c := range map[string]struct {
		token, challenge, detail string
		status                   int
		code                     string
	}{
		"no credentials":         {"", "Bearer", "", http.StatusUnauthorized, CodeCredentialsMissing},
		"a token nobody accepts": {"mallory", `Bearer error="invalid_token"`, "", http.StatusUnauthorized, CodeTokenInvalid},
		"a token that cannot be judged now": {"down", "", "The token could not be verified at the moment. It may be valid.",
			http.StatusServiceUnavailable, CodeIdentityProviderUnavailable},
	} {
		for _, target := range []string{workspaceReviews, workspaceReviews + "?status=APPROVED", workspaceReviews + "?page_size=0"} {
			fake := &workspaceQueue{}
			recorder := newQueueService(t, fake).do(t, call{Method: http.MethodGet, Token: c.token, Target: target})
			wantProblem(t, recorder, c.status, c.code, c.detail)
			if got := recorder.Header().Get("WWW-Authenticate"); got != c.challenge {
				t.Errorf("%s, %s: WWW-Authenticate %q, want %q", name, target, got, c.challenge)
			}
			if len(fake.listed) != 0 {
				t.Errorf("%s, %s: the request reached the use case", name, target)
			}
		}
	}
}

// What the use case refuses is answered as far as the caller may know, with
// the problem of the contract: there is no such product, there is no such
// workspace, the caller may not read. A page that cannot be is a request that
// is invalid. What it cannot read back is a failure of the service, and is not
// explained: the cause is in the log under the identifier of the request.
// A refusal that could not be recorded is answered as it was, and logged.
func TestTheRefusalsOfTheQueueAreAnsweredWithTheirProblems(t *testing.T) {
	forbidden := "The caller holds no role in the workspace that allows this."
	damaged := fmt.Errorf("review %s: %w", reviewID, reviewdomain.ErrReviewDamaged)
	for name, c := range map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"no such product":                {checks.ErrProductNotFound, http.StatusNotFound, CodeProductNotFound, ""},
		"no such product, wrapped":       {fmt.Errorf("read: %w", checks.ErrProductNotFound), http.StatusNotFound, CodeProductNotFound, ""},
		"no such workspace":              {checks.ErrWorkspaceNotFound, http.StatusNotFound, CodeWorkspaceNotFound, ""},
		"no such workspace, wrapped":     {fmt.Errorf("read: %w", checks.ErrWorkspaceNotFound), http.StatusNotFound, CodeWorkspaceNotFound, ""},
		"no right to read":               {checks.ErrReviewsForbidden, http.StatusForbidden, CodeForbidden, forbidden},
		"no right to read, wrapped":      {fmt.Errorf("%w: the request carries no principal", checks.ErrReviewsForbidden), http.StatusForbidden, CodeForbidden, forbidden},
		"a page that cannot be":          {checks.ErrInvalidReviewPage, http.StatusBadRequest, CodeRequestInvalid, ""},
		"a page that cannot be, wrapped": {fmt.Errorf("%w: a page holds 1 to 100 reviews", checks.ErrInvalidReviewPage), http.StatusBadRequest, CodeRequestInvalid, ""},
		"a review that is damaged":       {damaged, http.StatusInternalServerError, CodeInternalError, ""},
		"a failure nobody expected":      {errors.New("the database is gone"), http.StatusInternalServerError, CodeInternalError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &workspaceQueue{err: c.err}
			s := newQueueService(t, fake)
			recorder := s.do(t, call{Method: http.MethodGet, Target: workspaceReviews, Token: alice.Subject})
			traceID := wantProblem(t, recorder, c.status, c.code, c.detail)
			if strings.Contains(recorder.Body.String(), "damaged") || strings.Contains(recorder.Body.String(), "the database is gone") ||
				strings.Contains(recorder.Body.String(), reviewID) {
				t.Errorf("the answer says what failed: %s", recorder.Body)
			}
			if c.status == http.StatusInternalServerError && (!strings.Contains(s.log.String(), traceID) || !strings.Contains(s.log.String(), c.err.Error())) {
				t.Errorf("the log does not say what failed under the identifier of the request %s:\n%s", traceID, s.log)
			}
		})
	}

	// A refusal that could not be recorded is answered as the refusal it is,
	// and the log says that the trail has a gap.
	for _, refusal := range []error{checks.ErrProductNotFound, checks.ErrWorkspaceNotFound, checks.ErrReviewsForbidden} {
		fake := &workspaceQueue{err: fmt.Errorf("%w: %w: the audit trail is unavailable", refusal, checks.ErrReviewNotAudited)}
		s := newQueueService(t, fake)
		recorder := s.do(t, call{Method: http.MethodGet, Target: workspaceReviews, Token: alice.Subject})
		if recorder.Code != http.StatusNotFound && recorder.Code != http.StatusForbidden {
			t.Errorf("%v that could not be recorded: answered %d %s", refusal, recorder.Code, recorder.Body)
		}
		if !strings.Contains(s.log.String(), "a refusal could not be audited") {
			t.Errorf("%v that could not be recorded: the log does not say so:\n%s", refusal, s.log)
		}
	}
}

// The contract lists the reviews of a workspace as it shows a review: every
// item is a Review, whatever else a review says is said in every place it is
// read. The list takes a page, a status and who asked, and nothing else; it
// answers everything a read answers, and it is private to whoever reads it.
func TestTheContractListsWorkspaceReviewsAsReviews(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	const path = "/v1/products/{product_id}/workspaces/{workspace_id}/reviews"
	item := contract.Paths.Find(path)
	if item == nil || item.Get == nil || len(item.Operations()) != 1 {
		t.Fatalf("the contract has %+v at %s, want one operation: a read", item, path)
	}
	operation := item.Get
	if operation.OperationID != "listWorkspaceReviews" {
		t.Errorf("the operation is %q, want listWorkspaceReviews", operation.OperationID)
	}
	if operation.Security != nil || len(contract.Security) == 0 {
		t.Errorf("the operation declares its own security %v and the contract %v: it is to need a verified caller, as the contract says for all", operation.Security, contract.Security)
	}

	list := contract.Components.Schemas["ReviewList"].Value
	items := list.Properties["items"]
	if items == nil || items.Value.Items.Ref != "#/components/schemas/Review" {
		t.Errorf("the items of a list of reviews are not Review: %+v", items)
	}
	if next := list.Properties["next_page_token"]; next == nil || next.Ref != "#/components/schemas/NextPageToken" {
		t.Errorf("a list of reviews does not hand out the token of the page that follows as NextPageToken: %+v", next)
	}

	// A page, a status and who asked, in this order, and nothing else.
	var names []string
	for _, parameter := range operation.Parameters {
		names = append(names, parameter.Value.Name)
		switch parameter.Value.Name {
		case "product_id", "workspace_id":
			if parameter.Value.In != "path" || !parameter.Value.Required || parameter.Value.Schema.Ref != "#/components/schemas/Identifier" {
				t.Errorf("the parameter %s is %+v, want a path parameter that is an Identifier", parameter.Value.Name, parameter.Value)
			}
		case "status":
			if parameter.Value.In != "query" || parameter.Value.Required || parameter.Value.Schema.Ref != "#/components/schemas/ReviewStatus" {
				t.Errorf("the parameter status is %+v, want an optional query parameter that is a ReviewStatus", parameter.Value)
			}
		case "requested_by_issuer", "requested_by_subject":
			if parameter.Value.In != "query" || parameter.Value.Required || parameter.Value.Schema.Ref != "#/components/schemas/Reference" {
				t.Errorf("the parameter %s is %+v, want an optional query parameter that is a Reference", parameter.Value.Name, parameter.Value)
			}
		case "page_size", "page_token":
			if parameter.Value.In != "query" || parameter.Value.Required {
				t.Errorf("the parameter %s is %+v, want an optional query parameter", parameter.Value.Name, parameter.Value)
			}
		}
	}
	if want := []string{"product_id", "workspace_id", "page_size", "page_token", "status", "requested_by_issuer", "requested_by_subject"}; !reflect.DeepEqual(names, want) {
		t.Errorf("the operation declares %v, want %v", names, want)
	}
	// The statuses to keep the list to are those a review stands at, in the
	// order the contract names them, and those of the model.
	status := contract.Components.Schemas["ReviewStatus"].Value
	var statuses []string
	for _, value := range status.Enum {
		statuses = append(statuses, fmt.Sprint(value))
	}
	if !reflect.DeepEqual(statuses, reviewdomain.ReviewStatuses()) || !status.Type.Is("string") {
		t.Errorf("the contract names the statuses of a review %v, the model %v", statuses, reviewdomain.ReviewStatuses())
	}
	// Who asked is a reference and an identifier is one, as the contract
	// declares them, and the model holds both to the same: the bounds of a
	// reference, and the form of an identifier, character by character.
	reference := contract.Components.Schemas["Reference"].Value
	var longest uint64
	if reference.MaxLength != nil {
		longest = *reference.MaxLength
	}
	if reference.MinLength != 1 || longest != reviewdomain.MaxReference {
		t.Errorf("the contract bounds a reference to %d to %d characters, the model to 1 to %d", reference.MinLength, longest, reviewdomain.MaxReference)
	}
	form := regexp.MustCompile(contract.Components.Schemas["Identifier"].Value.Pattern)
	const identifier = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	samples := []string{identifier, strings.ToUpper(identifier), "", "cargopai", identifier + "\n", identifier[:35], identifier + "0"}
	for position := range len(identifier) {
		for _, change := range "gG- _a0" {
			samples = append(samples, identifier[:position]+string(change)+identifier[position+1:])
		}
	}
	for _, sample := range samples {
		if form.MatchString(sample) != reviewdomain.ValidIdentifier(sample) {
			t.Errorf("%q is an identifier for the contract: %t, for the model: %t", sample, form.MatchString(sample), reviewdomain.ValidIdentifier(sample))
		}
	}

	ok := operation.Responses.Value("200").Value
	if ok.Content.Get("application/json").Schema.Ref != "#/components/schemas/ReviewList" || ok.Headers["Cache-Control"] == nil || !ok.Headers["Cache-Control"].Value.Required {
		t.Errorf("the operation does not answer a ReviewList that is not to be kept")
	}
	// Not to be kept is the one thing the contract lets the page say of it.
	if header := ok.Headers["Cache-Control"]; header != nil {
		schema := header.Value.Schema.Value
		onlyNoStore := schema.Const == "no-store" && len(schema.Enum) == 0 || schema.Const == nil && len(schema.Enum) == 1 && schema.Enum[0] == "no-store"
		if !onlyNoStore || !schema.Type.Is("string") {
			t.Errorf("the page may say of its keeping something other than no-store: %v, const %v, enum %v", schema.Type, schema.Const, schema.Enum)
		}
	}
	// What else is answered: a request that cannot be read, a caller who is not
	// known, one who may not read, what is not there and a provider that does
	// not answer.
	for status, ref := range map[string]string{
		"400": "#/components/responses/Problem", "401": "#/components/responses/Unauthenticated",
		"403": "#/components/responses/Problem", "404": "#/components/responses/Problem", "503": "#/components/responses/Problem",
	} {
		if answer := operation.Responses.Value(status); answer == nil || answer.Ref != ref {
			t.Errorf("the operation declares %s as %+v, want %s", status, answer, ref)
		}
	}
	if answer := operation.Responses.Default(); answer == nil || answer.Ref != "#/components/responses/Problem" {
		t.Errorf("the operation declares anything else as %+v, want #/components/responses/Problem", answer)
	}
	for _, status := range []string{"201", "204", "409", "412", "413", "415", "422", "428"} {
		if operation.Responses.Value(status) != nil {
			t.Errorf("the operation declares %s, which a read does not answer", status)
		}
	}
	if got := len(operation.Responses.Map()); got != 7 {
		t.Errorf("the operation declares %d answers, want 200, 400, 401, 403, 404, 503 and the default", got)
	}
	// It says what it answers and to whom, and which it is.
	for _, said := range []string{"PRODUCT_NOT_FOUND", "WORKSPACE_NOT_FOUND", "FORBIDDEN", "IN_REVIEW", "getRevisionReview", "review.read", "input_digest"} {
		if !strings.Contains(operation.Description, said) {
			t.Errorf("the description does not say %s", said)
		}
	}
}
