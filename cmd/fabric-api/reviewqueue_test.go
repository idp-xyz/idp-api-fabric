package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	checkports "go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
)

// queueWorld is a product of two workspaces, engineering and operations, and
// another product, Parcel, with a workspace of its own, and an asset in each of
// the three. In engineering two authors ask for reviews, a reviewer decides
// them, a publisher reads, and a maintainer and the owner of the product read
// none. The people of the other workspaces are named for them: ops-author,
// ops-reviewer and ops-maintainer, sort-author, sort-reviewer and
// sort-maintainer, and owner2 owns Parcel.
type queueWorld struct {
	*reviewFixture
	operations, parcel *reviewFixture
}

func queueFor(t *testing.T) *queueWorld {
	t.Helper()

	w := &queueWorld{reviewFixture: reviewsFor(t)}
	s := w.stack
	s.join(w.product, w.workspace, "owner", "publisher", "internal_publisher")
	operations := s.createWorkspace(w.product, "owner", "operations")
	s.join(w.product, operations, "owner", "ops-maintainer", "workspace_maintainer")
	s.join(w.product, operations, "owner", "ops-author", "internal_author")
	s.join(w.product, operations, "owner", "ops-reviewer", "internal_reviewer")
	w.operations = &reviewFixture{
		stack: s, product: w.product, workspace: operations,
		revisions: s.revisionAsset(w.product, operations, "ops-maintainer", "billing-api", "HTTP"),
	}
	parcel := s.register("platform-admin", "parcel", "owner2")
	sorting := s.createWorkspace(parcel, "owner2", "sorting")
	s.join(parcel, sorting, "owner2", "sort-maintainer", "workspace_maintainer")
	s.join(parcel, sorting, "owner2", "sort-author", "internal_author")
	s.join(parcel, sorting, "owner2", "sort-reviewer", "internal_reviewer")
	w.parcel = &reviewFixture{
		stack: s, product: parcel, workspace: sorting,
		revisions: s.revisionAsset(parcel, sorting, "sort-maintainer", "sorting-api", "HTTP"),
	}
	return w
}

// queueOf is where the reviews of the workspace are listed.
func (f *reviewFixture) queueOf() string {
	return "/v1/products/" + f.product + "/workspaces/" + f.workspace + "/reviews"
}

// queueItemPath is where the review that an item of a queue is, is read alone.
func queueItemPath(item map[string]any) string {
	return fmt.Sprintf("/v1/products/%v/assets/%v/revisions/%v/reviews/%v",
		item["product_id"], item["api_asset_id"], item["revision_id"], item["review_id"])
}

// queueMade is a review a test asked for: where it stands and who asked for it.
type queueMade struct {
	id, status, requester string
	supplementary         bool
}

func queueMadeIDs(reviews []queueMade) []string {
	listed := make([]string, 0, len(reviews))
	for _, review := range reviews {
		listed = append(listed, review.id)
	}
	return listed
}

// queueDecided decides the review as the subject, which has to be answered.
func (f *reviewFixture) queueDecided(review, subject, body string) answer {
	f.t.Helper()
	got := f.call(http.MethodPost, review+"/decision", subject, body)
	if got.status != http.StatusOK {
		f.t.Fatalf("decide %s as %s: %d %v", review, subject, got.status, got.body)
	}
	return got
}

// queueAskedAgain asks for another review of the revision, as the subject, on its
// check of today and for a scope its approval did not name: a supplementary
// review. It returns its identifier.
func (f *reviewFixture) queueAskedAgain(revision, subject string) string {
	f.t.Helper()
	got := f.call(http.MethodPost, revision+"/reviews", subject, asking(f.checkOf(revision), internalProduction))
	if got.status != http.StatusCreated || got.body["supplementary"] != true {
		f.t.Fatalf("ask for a supplementary review as %s: %d %v", subject, got.status, got.body)
	}
	return got.id("review_id")
}

// filled asks for the reviews of engineering, in each state a review stands
// in, in the order of the queue: open, approved, withdrawn, rejected, open
// again, approved with the waiver of the rule that did not pass, a
// supplementary review that is open, one that is approved with the same
// waiver, and open once more. After each of them a review is asked for in each
// of the other two workspaces, so that a page that was cut from the reviews of
// all three, and then relieved of those of the others, would not be full.
func (w *queueWorld) filled() []queueMade {
	w.t.Helper()

	until := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	sooner := until.Add(-30 * 24 * time.Hour)
	var made []queueMade
	asked := 0
	next := func() string { asked++; return fmt.Sprintf("%s# engineering %d\n", revisionSource, asked) }
	beside := 0
	keep := func(id, status, requester string, supplementary bool) {
		made = append(made, queueMade{id, status, requester, supplementary})
		beside++
		w.operations.inReview(fmt.Sprintf("%s# operations %d\n", revisionSource, beside), "ops-author", "ops-author", "ops-author")
		w.parcel.inReview(fmt.Sprintf("%s# parcel %d\n", revisionSource, beside), "sort-author", "sort-author", "sort-author")
	}

	open := w.inReview(next(), "author", "author", "author")
	keep(last(open.review), "IN_REVIEW", "author", false)

	approved := w.inReview(next(), "author", "author", "coauthor")
	w.queueDecided(approved.review, "reviewer", deciding("APPROVED", "reviewed with the partner team", until))
	keep(last(approved.review), "APPROVED", "coauthor", false)

	withdrawn := w.inReview(next(), "author", "author", "author")
	if got := w.call(http.MethodPost, withdrawn.review+"/withdrawal", "author", `{"reason":"the wrong baseline"}`); got.status != http.StatusOK {
		w.t.Fatalf("withdraw a review: %d %v", got.status, got.body)
	}
	keep(last(withdrawn.review), "WITHDRAWN", "author", false)

	rejected := w.inReview(next(), "author", "author", "author")
	w.queueDecided(rejected.review, "reviewer", deciding("REJECTED", "every operation needs its operationId", time.Time{}))
	keep(last(rejected.review), "REJECTED", "author", false)

	again := w.inReview(next(), "author", "author", "coauthor")
	keep(last(again.review), "IN_REVIEW", "coauthor", false)

	unnamed := w.inReview(unnamedSource, "author", "author", "author")
	w.queueDecided(unnamed.review, "reviewer", deciding("APPROVED", "", until, waiving("F04-GOV-003", sooner)))
	keep(last(unnamed.review), "APPROVED", "author", false)

	supplementary := w.queueAskedAgain(approved.revision, "author")
	keep(supplementary, "IN_REVIEW", "author", true)

	waived := w.queueAskedAgain(unnamed.revision, "coauthor")
	w.queueDecided(unnamed.revision+"/reviews/"+waived, "reviewer", deciding("APPROVED", "", until, waiving("F04-GOV-003", sooner)))
	keep(waived, "APPROVED", "coauthor", true)

	final := w.inReview(next(), "author", "author", "author")
	keep(last(final.review), "IN_REVIEW", "author", false)
	return made
}

// queueRow is where a review stands in the tables, read where an operator would
// read it: who asked for it and whether it has a conclusion.
type queueRow struct{ id, status, subject string }

// queueRows lists the reviews of a workspace in the order of the queue, from the
// tables and not through the API: what the queue is checked against.
func (s *stack) queueRows(workspace string) []queueRow {
	s.t.Helper()

	rows, err := s.owner.Query(s.t.Context(), `
		SELECT q.review_id::text, coalesce(c.outcome, 'IN_REVIEW'), q.requested_by_subject
		FROM policyreview.review_request q LEFT JOIN policyreview.review_conclusion c USING (review_id)
		WHERE q.workspace_id = $1 ORDER BY q.requested_at, q.review_id`, workspace)
	if err != nil {
		s.t.Fatalf("read the reviews of a workspace: %v", err)
	}
	found, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (queueRow, error) {
		var review queueRow
		err := row.Scan(&review.id, &review.status, &review.subject)
		return review, err
	})
	if err != nil {
		s.t.Fatalf("read the reviews of a workspace: %v", err)
	}
	return found
}

// queueRowIDs are the identifiers of the reviews that keep says so of.
func queueRowIDs(reviews []queueRow, keep func(queueRow) bool) []string {
	listed := []string{}
	for _, review := range reviews {
		if keep(review) {
			listed = append(listed, review.id)
		}
	}
	return listed
}

func everyQueueRow(queueRow) bool { return true }

// queuePages reads a queue to its end as the subject, a page of size reviews at
// a time, kept to what the query says, each page behind the token the one
// before handed out. It fails the test for a page that is not the last and is
// not full.
func (s *stack) queuePages(path, query, subject string, size int) [][]map[string]any {
	s.t.Helper()

	var pages [][]map[string]any
	target := fmt.Sprintf("%s?%spage_size=%d", path, query, size)
	for len(pages) < 200 {
		items, next := s.page(target, subject)
		pages = append(pages, items)
		if next == "" {
			for index, page := range pages[:len(pages)-1] {
				if len(page) != size {
					s.t.Errorf("%s reads %s?%s in pages of %d: page %d holds %d reviews and is not the last", subject, path, query, size, index+1, len(page))
				}
			}
			return pages
		}
		target = fmt.Sprintf("%s?%spage_size=%d&page_token=%s", path, query, size, next)
	}
	s.t.Fatalf("%s lists %s?%s: the queue does not end", subject, path, query)
	return nil
}

// The queue of a workspace is the reviews asked for in that workspace and no
// others, oldest first, and it is read to its end a page at a time: every page
// but the last is full, whatever the other workspaces and the other product
// hold between its reviews; every review is on exactly one page; the last page
// hands out no token, also when it is full; and the queue is the same however
// it is cut and whoever reads it. A workspace in which nothing was asked for
// has an empty queue.
func TestTheQueueIsTheReviewsOfThatWorkspaceAndReadToItsEnd(t *testing.T) {
	w := queueFor(t)
	made := w.filled()
	s := w.stack
	engineering := queueMadeIDs(made)
	if len(made) != 9 {
		t.Fatalf("%d reviews were asked for in engineering, want the 9 the cases were made of", len(made))
	}

	// Oldest first is the order they were asked for in, and the order of the
	// tables.
	stored := s.queueRows(w.workspace)
	if got := queueRowIDs(stored, everyQueueRow); !slices.Equal(got, engineering) {
		t.Fatalf("the tables hold the reviews of engineering as %v, want them in the order they were asked for, %v", got, engineering)
	}
	for _, size := range []int{1, 2, 4, 8, 9, 10, 100} {
		pages := s.queuePages(w.queueOf(), "", "reviewer", size)
		if got := told(listed(pages), "review_id"); !slices.Equal(got, engineering) {
			t.Errorf("in pages of %d the queue is %v, want %v", size, got, engineering)
		}
		if wantPages := (len(engineering) + size - 1) / size; len(pages) != wantPages {
			t.Errorf("in pages of %d the queue is %d pages, want %d: a page that is full and last hands out no token", size, len(pages), wantPages)
		}
	}
	// Whoever reads it reads the same queue, and it is read the same twice.
	first := s.queuePages(w.queueOf(), "", "reviewer", 3)
	for _, reader := range []string{"author", "coauthor", "publisher", "reviewer"} {
		if got := s.queuePages(w.queueOf(), "", reader, 3); !reflect.DeepEqual(got, first) {
			t.Errorf("%s reads the queue as\n %v\nwant\n %v", reader, got, first)
		}
	}

	// The reviews of the other two workspaces are in their own queues and in
	// no other.
	operations, parcel := s.queueRows(w.operations.workspace), s.queueRows(w.parcel.workspace)
	if len(operations) != len(made) || len(parcel) != len(made) || s.count("policyreview.review_request") != len(made)+len(operations)+len(parcel) {
		t.Fatalf("the tables hold %d reviews of engineering, %d of operations and %d of Parcel, and %d in all",
			len(made), len(operations), len(parcel), s.count("policyreview.review_request"))
	}
	for name, c := range map[string]struct {
		queue, reader string
		own           []queueRow
	}{
		"operations": {w.operations.queueOf(), "ops-author", operations},
		"Parcel":     {w.parcel.queueOf(), "sort-reviewer", parcel},
	} {
		got := told(listed(s.queuePages(c.queue, "", c.reader, 2)), "review_id")
		if own := queueRowIDs(c.own, everyQueueRow); !slices.Equal(got, own) {
			t.Errorf("the queue of %s is %v, want %v", name, got, own)
		}
		for _, id := range got {
			if slices.Contains(engineering, id) {
				t.Errorf("the queue of %s holds %s, a review of engineering", name, id)
			}
		}
	}

	// A workspace nobody asked a review in has an empty queue, and no token.
	empty := s.createWorkspace(w.product, "owner", "empty")
	s.join(w.product, empty, "owner", "empty-author", "internal_author")
	got := s.call(http.MethodGet, "/v1/products/"+w.product+"/workspaces/"+empty+"/reviews", "empty-author", "")
	if got.status != http.StatusOK || !reflect.DeepEqual(got.body, map[string]any{"items": []any{}}) || got.header.Get("Cache-Control") != "no-store" {
		t.Errorf("the queue of a workspace without reviews: %d %v %v, want 200 and an empty list that is not to be kept", got.status, got.body, got.header)
	}
}

// A review of the queue is the review that is read alone, whatever state it is
// in: open, withdrawn, approved, approved with the waiver of a rule, rejected,
// and a supplementary review, open and approved. The two are shown by one
// function, so what is listed has the members of what is read.
func TestAReviewOfTheQueueIsTheReviewAsItIsRead(t *testing.T) {
	w := queueFor(t)
	made := w.filled()
	s := w.stack

	items := listed(s.queuePages(w.queueOf(), "", "reviewer", 100))
	if len(items) != len(made) {
		t.Fatalf("the queue holds %d reviews, want %d", len(items), len(made))
	}
	states := map[string]int{}
	for index, item := range items {
		single := s.call(http.MethodGet, queueItemPath(item), "reviewer", "")
		if single.status != http.StatusOK || !reflect.DeepEqual(single.body, item) {
			t.Errorf("review %d of the queue is\n %v\nread alone it is %d\n %v", index, item, single.status, single.body)
		}
		if item["review_id"] != made[index].id || item["status"] != made[index].status || item["supplementary"] != made[index].supplementary ||
			item["requested_by"].(map[string]any)["subject"] != made[index].requester {
			t.Errorf("review %d of the queue is %v, want %+v", index, item, made[index])
		}
		kind := fmt.Sprintf("%v, supplementary %v", item["status"], item["supplementary"])
		if conclusion, concluded := item["conclusion"].(map[string]any); concluded {
			kind += ", concluded by " + conclusion["concluded_by"].(map[string]any)["subject"].(string)
			if waivers, _ := conclusion["waivers"].([]any); len(waivers) > 0 {
				kind += ", with waivers"
			}
		}
		states[kind]++
	}
	// Every state there is, with the conclusion that says how it ended.
	wantStates := map[string]int{
		"IN_REVIEW, supplementary false": 3, "IN_REVIEW, supplementary true": 1,
		"APPROVED, supplementary false, concluded by reviewer":               1,
		"APPROVED, supplementary false, concluded by reviewer, with waivers": 1,
		"APPROVED, supplementary true, concluded by reviewer, with waivers":  1,
		"WITHDRAWN, supplementary false, concluded by author":                1,
		"REJECTED, supplementary false, concluded by reviewer":               1,
	}
	if !reflect.DeepEqual(states, wantStates) {
		t.Errorf("the queue holds the states\n %v\nwant\n %v", states, wantStates)
	}

	// What the answer of a decision said of the review is what the queue says
	// of it afterwards.
	extra := w.inReview(revisionSource+"# decided last\n", "author", "author", "author")
	decision := w.queueDecided(extra.review, "reviewer", deciding("APPROVED", "as it is", time.Now().UTC().Add(48*time.Hour).Truncate(time.Second)))
	items = listed(s.queuePages(w.queueOf(), "", "reviewer", 100))
	if got := items[len(items)-1]; !reflect.DeepEqual(got, decision.body) {
		t.Errorf("the queue shows the review that was decided last as\n %v\nthe decision answered\n %v", got, decision.body)
	}
}

// revisionOf is where the review of engineering is read alone: the asset and
// the revision it is of are found where the tables keep them.
func (w *queueWorld) revisionOf(review string) string {
	w.t.Helper()

	var asset, revision string
	if err := w.owner.QueryRow(w.t.Context(),
		`SELECT api_asset_id::text, revision_id::text FROM policyreview.review_request WHERE review_id = $1`, review).Scan(&asset, &revision); err != nil {
		w.t.Fatalf("find the review %s: %v", review, err)
	}
	return "/v1/products/" + w.product + "/assets/" + asset + "/revisions/" + revision + "/reviews/" + review
}

// Whoever reads one review of a workspace reads the queue of it, and whoever
// reads none is refused it, as it is refused each review: the queue lists what a
// read of one review answers, caller by caller. An author, a reviewer and a
// publisher of the workspace read every review of it; a maintainer and the owner
// of the product, who see the workspace and hold none of the three grants, are
// refused both; a member of another workspace, the administrator of the platform
// and a stranger are told as little of the workspace as of one of its reviews.
// What is listed is what is read, one review at a time, for each.
func TestTheQueueShowsEachCallerExactlyWhatItsSingleReadsAnswer(t *testing.T) {
	w := queueFor(t)
	engineering := queueMadeIDs(w.filled())
	s := w.stack

	for _, c := range []struct {
		subject    string
		queue      int
		queueCode  string
		single     int
		singleCode string
	}{
		{"author", http.StatusOK, "", http.StatusOK, ""},
		{"coauthor", http.StatusOK, "", http.StatusOK, ""},
		{"reviewer", http.StatusOK, "", http.StatusOK, ""},
		{"publisher", http.StatusOK, "", http.StatusOK, ""},
		{"maintainer", http.StatusForbidden, "FORBIDDEN", http.StatusForbidden, "FORBIDDEN"},
		{"owner", http.StatusOK, "", http.StatusOK, ""},
		{"ops-author", http.StatusNotFound, "WORKSPACE_NOT_FOUND", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{"ops-reviewer", http.StatusNotFound, "WORKSPACE_NOT_FOUND", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{"ops-maintainer", http.StatusNotFound, "WORKSPACE_NOT_FOUND", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{"platform-admin", http.StatusNotFound, "WORKSPACE_NOT_FOUND", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{"owner2", http.StatusNotFound, "PRODUCT_NOT_FOUND", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{"sort-author", http.StatusNotFound, "PRODUCT_NOT_FOUND", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{"stranger", http.StatusNotFound, "PRODUCT_NOT_FOUND", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		t.Run(c.subject, func(t *testing.T) {
			// What a read of each review answers.
			var readable []string
			for _, id := range engineering {
				got := s.call(http.MethodGet, w.revisionOf(id), c.subject, "")
				if got.status == http.StatusOK {
					readable = append(readable, id)
				}
				if got.status != c.single || got.code() != c.singleCode {
					t.Errorf("%s reads the review %s alone: %d %v, want %d %s", c.subject, id, got.status, got.body, c.single, c.singleCode)
				}
			}
			// What the queue lists.
			got := s.call(http.MethodGet, w.queueOf(), c.subject, "")
			if got.status != c.queue || got.code() != c.queueCode {
				t.Fatalf("%s reads the queue: %d %v, want %d %s", c.subject, got.status, got.body, c.queue, c.queueCode)
			}
			if got.status != http.StatusOK {
				if got.body["items"] != nil || len(readable) != 0 {
					t.Errorf("%s is refused the queue and reads %v of its reviews, and the refusal says %v", c.subject, readable, got.body)
				}
				return
			}
			if lists := told(listed(s.queuePages(w.queueOf(), "", c.subject, 4)), "review_id"); !slices.Equal(lists, readable) || !slices.Equal(readable, engineering) {
				t.Errorf("%s reads the queue %v and the reviews %v alone, want both to be %v", c.subject, lists, readable, engineering)
			}
		})
	}
}

// A queue is kept to a status, and to who asked for its reviews, before it is
// cut into pages: the reviews of each status, in the order of the queue, in
// pages that are full; the reviews a principal asked for, by its issuer, by its
// subject under any issuer or by both; and the two together. What is asked of
// an issuer that is not the one that was, or of a subject that is nobody's, is
// an empty list and not a refusal. A token names a position and not a filter:
// it brings the page behind it under another one.
func TestTheQueueCanBeKeptToOneStatusAndToWhoAskedForTheReview(t *testing.T) {
	w := queueFor(t)
	w.filled()
	s := w.stack
	stored := s.queueRows(w.workspace)
	issuer := url.QueryEscape(s.issuer.URL())

	for _, status := range []string{"IN_REVIEW", "WITHDRAWN", "APPROVED", "REJECTED"} {
		want := queueRowIDs(stored, func(review queueRow) bool { return review.status == status })
		if len(want) == 0 {
			t.Fatalf("no review stands at %s: the cases were not made of every status", status)
		}
		for _, size := range []int{1, 2, 3, 100} {
			if got := told(listed(s.queuePages(w.queueOf(), "status="+status+"&", "reviewer", size)), "review_id"); !slices.Equal(got, want) {
				t.Errorf("the reviews that stand at %s, in pages of %d: %v, want %v", status, size, got, want)
			}
		}
	}

	nobody := url.QueryEscape("https://nobody.example")
	for name, c := range map[string]struct {
		query string
		keep  func(queueRow) bool
	}{
		"the issuer of everyone": {"requested_by_issuer=" + issuer + "&", everyQueueRow},
		"an issuer nobody has":   {"requested_by_issuer=" + nobody + "&", func(queueRow) bool { return false }},
		"a subject":              {"requested_by_subject=coauthor&", func(review queueRow) bool { return review.subject == "coauthor" }},
		"another subject":        {"requested_by_subject=author&", func(review queueRow) bool { return review.subject == "author" }},
		"a subject nobody has":   {"requested_by_subject=nobody&", func(queueRow) bool { return false }},
		"the issuer and the subject": {"requested_by_issuer=" + issuer + "&requested_by_subject=coauthor&",
			func(review queueRow) bool { return review.subject == "coauthor" }},
		"the issuer and the subject swapped": {"requested_by_issuer=coauthor&requested_by_subject=" + issuer + "&", func(queueRow) bool { return false }},
		"another issuer and the subject":     {"requested_by_issuer=" + nobody + "&requested_by_subject=coauthor&", func(queueRow) bool { return false }},
		"a subject, kept to a status": {"status=APPROVED&requested_by_subject=coauthor&",
			func(review queueRow) bool { return review.subject == "coauthor" && review.status == "APPROVED" }},
		"every filter": {"status=IN_REVIEW&requested_by_issuer=" + issuer + "&requested_by_subject=author&",
			func(review queueRow) bool { return review.subject == "author" && review.status == "IN_REVIEW" }},
	} {
		want := queueRowIDs(stored, c.keep)
		for _, size := range []int{1, 2, 100} {
			if got := told(listed(s.queuePages(w.queueOf(), c.query, "reviewer", size)), "review_id"); !slices.Equal(got, want) {
				t.Errorf("%s, in pages of %d: %v, want %v", name, size, got, want)
			}
		}
	}

	// A token names where in the queue a page starts and nothing it is kept to.
	items, token := s.page(w.queueOf()+"?status=APPROVED&page_size=1", "reviewer")
	if len(items) != 1 || token == "" {
		t.Fatalf("the first approved review: %v and the token %q", items, token)
	}
	var behind []string
	for index, review := range stored {
		if review.id == told(items, "review_id")[0] {
			behind = queueRowIDs(stored[index+1:], func(review queueRow) bool { return review.status == "REJECTED" })
		}
	}
	got, _ := s.page(w.queueOf()+"?status=REJECTED&page_size=100&page_token="+token, "reviewer")
	if !slices.Equal(told(got, "review_id"), behind) || len(behind) != 1 {
		t.Errorf("behind the position of an approved review, the rejected reviews are %v, want %v", told(got, "review_id"), behind)
	}
}

// A review that is asked for, or decided, while the queue is read repeats
// nothing and skips nothing. A page starts behind a position, not at an offset:
// what was in the queue before the first page was read and is in it after the
// last is returned exactly once, in the order it was in; a review asked for
// meanwhile comes after every position that was handed out, and a review that is
// decided meanwhile leaves the list of the open reviews, when it was not read
// yet, without moving any other.
func TestAReviewAskedOrConcludedWhileTheQueueIsReadRepeatsNothingAndSkipsNothing(t *testing.T) {
	w := queueFor(t)
	made := w.filled()
	s := w.stack

	// The open reviews, a page of two at a time: the supplementary review that
	// is open is decided and a new review is asked between the pages.
	open := []string{}
	for _, review := range made {
		if review.status == "IN_REVIEW" {
			open = append(open, review.id)
		}
	}
	if len(open) != 4 {
		t.Fatalf("%d reviews are open, want the 4 the cases were made of", len(open))
	}
	items, token := s.page(w.queueOf()+"?status=IN_REVIEW&page_size=2", "reviewer")
	if got := told(items, "review_id"); !slices.Equal(got, open[:2]) || token == "" {
		t.Fatalf("the first page of the open reviews is %v and the token %q, want %v", got, token, open[:2])
	}
	read := told(items, "review_id")

	// The third open review, the supplementary one, is decided before its
	// page is read; and a review is asked for.
	var path string
	for _, item := range listed(s.queuePages(w.queueOf(), "status=IN_REVIEW&", "reviewer", 100)) {
		if item["review_id"] == open[2] {
			path = queueItemPath(item)
		}
	}
	w.queueDecided(path, "reviewer", deciding("APPROVED", "approved while the queue was read", time.Now().UTC().Add(48*time.Hour).Truncate(time.Second)))
	added := w.inReview(revisionSource+"# asked while the queue was read\n", "author", "author", "author")

	rest, next := s.page(w.queueOf()+"?status=IN_REVIEW&page_size=2&page_token="+token, "reviewer")
	read = append(read, told(rest, "review_id")...)
	for next != "" {
		rest, next = s.page(w.queueOf()+"?status=IN_REVIEW&page_size=2&page_token="+next, "reviewer")
		read = append(read, told(rest, "review_id")...)
	}
	// What was open before and is open after is returned once, in its order;
	// what was decided is not on a page behind where the list was read to; what
	// was asked for comes last.
	if want := []string{open[0], open[1], open[3], last(added.review)}; !slices.Equal(read, want) {
		t.Errorf("the open reviews that were read are %v, want %v", read, want)
	}

	// The whole queue, read in pages of three while a review is asked for
	// after the first page: what was read is not read again, and nothing before
	// the position is skipped.
	all := told(listed(s.queuePages(w.queueOf(), "", "reviewer", 100)), "review_id")
	items, token = s.page(w.queueOf()+"?page_size=3", "reviewer")
	seen := told(items, "review_id")
	more := w.inReview(revisionSource+"# asked after the first page\n", "author", "author", "author")
	for token != "" {
		items, token = s.page(w.queueOf()+"?page_size=3&page_token="+token, "reviewer")
		seen = append(seen, told(items, "review_id")...)
	}
	if want := append(slices.Clone(all), last(more.review)); !slices.Equal(seen, want) {
		t.Errorf("the queue read while a review was asked for is %v, want what it was and the review that was asked for, last: %v", seen, want)
	}
}

// Whoever may not read the queue is told as little as it may know, and each
// refusal that has a product to be recorded under is on record: the action, the
// rule, the kind of resource, the product and who asked, and no resource, since
// a queue names none; and none else. The refusals that are not on record are
// those of a product that is not there, and of a workspace that the owner of the
// product asks for and the product has not. A queue that is answered leaves no
// record and no event, and a product that is suspended is read as it was.
func TestWhoMayNotReadTheQueueIsRefusedAsFarAsItMayKnow(t *testing.T) {
	w := queueFor(t)
	w.filled()
	s := w.stack
	queue := w.queueOf()
	missing := ids.NewUUID()

	refused := func(subject, path string, status int, code string, recorded bool) {
		t.Helper()
		before, all := s.on(subject, "review.read", "DENY-03", false), len(s.audit())
		got := s.call(http.MethodGet, path, subject, "")
		if got.status != status || got.code() != code || got.body["items"] != nil || got.header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s lists %s: %d %v, want %d %s and no review", subject, path, got.status, got.body, status, code)
		}
		added := 0
		if recorded {
			added = 1
		}
		if after, records := s.on(subject, "review.read", "DENY-03", false), len(s.audit()); after != before+added || records != all+added {
			t.Errorf("%s lists %s: %d refusals of review.read are on record and %d records in all, want %d and %d",
				subject, path, after, records, before+added, all+added)
		}
		// A refusal is on record for the kind of resource a review is, and for
		// no resource of its own, against the product it was asked in.
		for record := range s.recorded(func(row audited) bool { return row.Subject == subject && row.Action == "review.read" }) {
			if record.ResourceType != "Review" || record.ResourceID != "" || record.Outcome != "DENIED" || record.Rule != "DENY-03" || record.ProductID != w.product {
				t.Errorf("%s lists %s: the refusal is on record as %+v, want DENIED under DENY-03 for a Review, no identifier, in the product %s",
					subject, path, record, w.product)
			}
		}
		// The record names the request that was refused: the identifier that
		// the answer carries as its trace.
		if recorded {
			trail := s.trail(w.product)
			var named string
			if newest := slices.IndexFunc(trail, func(row trailed) bool { return row.Subject == subject && row.Action == "review.read" }); newest >= 0 {
				named = trail[newest].Correlation
			}
			if named == "" || named != got.body["trace_id"] {
				t.Errorf("%s lists %s: the answer carries the trace %v, the newest record of the refusal names the request %q",
					subject, path, got.body["trace_id"], named)
			}
		}
	}
	for _, c := range []struct {
		subject string
		status  int
		code    string
	}{
		// Seeing the workspace is not reading what is reviewed in it.
		{"maintainer", http.StatusForbidden, "FORBIDDEN"},
		// A member of another workspace is told what an asset of this one is.
		{"ops-author", http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
		{"ops-maintainer", http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
		// The administrator of the platform knows the product and sees nothing in it.
		{"platform-admin", http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
		{"stranger", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{"sort-author", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{"owner2", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		refused(c.subject, queue, c.status, c.code, true)
	}
	// A workspace that is not there is told of by whoever sees workspaces, the
	// owner, and it is no authorization that is refused.
	absent := "/v1/products/" + w.product + "/workspaces/" + missing + "/reviews"
	refused("owner", absent, http.StatusNotFound, "WORKSPACE_NOT_FOUND", false)
	refused("owner", "/v1/products/"+w.product+"/workspaces/"+w.parcel.workspace+"/reviews", http.StatusNotFound, "WORKSPACE_NOT_FOUND", false)
	// Whoever does not see workspaces is told the same, of one there is and of
	// one there is not, and it is on record.
	refused("author", "/v1/products/"+w.product+"/workspaces/"+w.operations.workspace+"/reviews", http.StatusNotFound, "WORKSPACE_NOT_FOUND", true)
	refused("author", absent, http.StatusNotFound, "WORKSPACE_NOT_FOUND", true)
	refused("stranger", absent, http.StatusNotFound, "PRODUCT_NOT_FOUND", true)
	// A product that is not there is not there for anyone, and there is no
	// product to record it under.
	gone := "/v1/products/" + missing + "/workspaces/" + w.workspace + "/reviews"
	for _, subject := range []string{"owner", "author", "platform-admin", "stranger"} {
		refused(subject, gone, http.StatusNotFound, "PRODUCT_NOT_FOUND", false)
	}
	// Without a token it is refused before anything is asked.
	records := len(s.audit())
	if got := s.call(http.MethodGet, queue, "", ""); got.status != http.StatusUnauthorized || got.code() != "CREDENTIALS_MISSING" || len(s.audit()) != records {
		t.Errorf("without credentials: %d %v, %d records more", got.status, got.body, len(s.audit())-records)
	}

	// A queue that is answered leaves no record and no event.
	records, events := s.count("platformoperations.audit_record"), s.count("bento.outbox")
	for _, reader := range []string{"author", "reviewer", "publisher"} {
		if items := listed(s.queuePages(queue, "", reader, 4)); len(items) != 9 {
			t.Errorf("%s reads %d reviews, want 9", reader, len(items))
		}
		s.queuePages(queue, "status=APPROVED&", reader, 2)
	}
	if after, sent := s.count("platformoperations.audit_record"), s.count("bento.outbox"); after != records || sent != events {
		t.Errorf("the queues that were answered left %d audit records and %d events, want none", after-records, sent-events)
	}

	// A product that is suspended takes no new work and its queue is read as it
	// was, by those who read it, and refused to those who do not.
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'SUSPENDED' WHERE product_id = $1`, w.product); err != nil {
		t.Fatal(err)
	}
	if items := listed(s.queuePages(queue, "", "reviewer", 4)); len(items) != 9 {
		t.Errorf("a suspended product: the reviewer reads %d reviews, want 9", len(items))
	}
	refused("maintainer", queue, http.StatusForbidden, "FORBIDDEN", true)
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'ACTIVE' WHERE product_id = $1`, w.product); err != nil {
		t.Fatal(err)
	}

	// The refusals are read as any record of the trail is, by an auditor of the
	// product: what the contract says of a record takes the new action, the
	// kind of resource, and a record that names no resource.
	s.auditorOf(w.product, "queue-auditor")
	want := 0
	for _, row := range s.trail(w.product) {
		if row.Action == "review.read" {
			want++
		}
	}
	trail := listed(s.auditPagesOf(auditPath(w.product)+"?action=review.read", "queue-auditor", 4))
	if want == 0 || len(trail) != want {
		t.Fatalf("the auditor reads %d records of review.read, the trail of the product holds %d", len(trail), want)
	}
	for _, record := range trail {
		if _, named := record["resource_id"]; named || record["action"] != "review.read" || record["resource_type"] != "Review" ||
			record["outcome"] != "DENIED" || record["rule_id"] != "DENY-03" || record["product_id"] != w.product {
			t.Errorf("the auditor reads the record %v of a refusal, want DENIED under DENY-03 on a Review that names none", record)
		}
	}
}

// What another product has is no more to be found through this one than what is
// not there: its workspace, asked for under this product, is answered as a
// workspace that does not exist, member for member but the identifier of the
// request, to whoever may be told of workspaces and to whoever may not; and the
// reviews of the other product are in no queue of this one, nor are the reviews
// of this one in a queue of the other.
func TestAnotherProductsWorkspaceIsAsMissingAsOneThatIsNot(t *testing.T) {
	w := queueFor(t)
	made := w.filled()
	s := w.stack
	missing := ids.NewUUID()

	here := "/v1/products/" + w.product + "/workspaces/"
	for _, subject := range []string{"owner", "author", "reviewer", "ops-author", "platform-admin", "stranger", "owner2", "sort-author"} {
		withheld := s.call(http.MethodGet, here+w.parcel.workspace+"/reviews", subject, "")
		absent := s.call(http.MethodGet, here+missing+"/reviews", subject, "")
		if !sameAnswer(withheld, absent) {
			t.Errorf("%s asks for the queue of a workspace of Parcel under CargoPai: %d %v, and for one that is not there: %d %v; want the same",
				subject, withheld.status, withheld.body, absent.status, absent.body)
		}
	}
	// And the other way: a workspace of CargoPai asked for under Parcel.
	there := "/v1/products/" + w.parcel.product + "/workspaces/"
	for _, subject := range []string{"owner2", "sort-author", "author", "owner", "platform-admin", "stranger"} {
		withheld := s.call(http.MethodGet, there+w.workspace+"/reviews", subject, "")
		absent := s.call(http.MethodGet, there+missing+"/reviews", subject, "")
		if !sameAnswer(withheld, absent) {
			t.Errorf("%s asks for the queue of a workspace of CargoPai under Parcel: %d %v, and for one that is not there: %d %v; want the same",
				subject, withheld.status, withheld.body, absent.status, absent.body)
		}
	}
	// Each queue holds the reviews of its own.
	ours := told(listed(s.queuePages(w.queueOf(), "", "reviewer", 100)), "review_id")
	theirs := told(listed(s.queuePages(w.parcel.queueOf(), "", "sort-reviewer", 100)), "review_id")
	if !slices.Equal(ours, queueMadeIDs(made)) || len(theirs) != len(made) {
		t.Fatalf("the queues hold %d and %d reviews, want %d each", len(ours), len(theirs), len(made))
	}
	for _, id := range theirs {
		if slices.Contains(ours, id) {
			t.Errorf("the queue of CargoPai holds %s, a review of Parcel", id)
		}
	}
	// A token of one queue is no token of another.
	_, token := s.page(w.queueOf()+"?page_size=1", "author")
	s.expect("a token of one queue in the queue of another product", "sort-author", []exchange{
		{http.MethodGet, w.parcel.queueOf() + "?page_token=" + token, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
	})
	s.expect("a token of one queue in the queue of another workspace", "ops-author", []exchange{
		{http.MethodGet, w.operations.queueOf() + "?page_token=" + token, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
	})
}

// A page token grants nothing. It names a position, and what a caller may read
// is decided with every page, from what it holds then: the token of a reader in
// the hands of someone who reads none of the workspace is refused as that
// someone is, one who reads another workspace reads nothing of this, a reader
// that holds the grant still reads from the position, the token of another list
// is none, and a grant that was revoked since the page before is the grant of a
// stranger on the next.
func TestAPageTokenOfTheQueueGrantsNothing(t *testing.T) {
	w := queueFor(t)
	made := w.filled()
	s := w.stack
	queue := w.queueOf()

	items, token := s.page(queue+"?page_size=2", "author")
	if len(items) != 2 || token == "" {
		t.Fatalf("the first page holds %v and the token %q", told(items, "review_id"), token)
	}
	// In the hands of others.
	for subject, c := range map[string]struct {
		status int
		code   string
	}{
		"maintainer":     {http.StatusForbidden, "FORBIDDEN"},
		"stranger":       {http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		"ops-author":     {http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
		"platform-admin": {http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
	} {
		s.expect("the token of an author", subject, []exchange{{http.MethodGet, queue + "?page_token=" + token, "", c.status, c.code}})
	}
	// A reader that holds a grant reads from the position, the same queue.
	for _, reader := range []string{"reviewer", "owner"} {
		got, _ := s.page(queue+"?page_size=100&page_token="+token, reader)
		if want := queueMadeIDs(made)[2:]; !slices.Equal(told(got, "review_id"), want) {
			t.Errorf("%s with the token of an author reads %v, want what is behind its position, %v", reader, told(got, "review_id"), want)
		}
	}
	// The token of another list is none.
	_, ofRevisions := s.page(w.revisions+"?page_size=1", "author")
	_, ofOperations := s.page(w.operations.queueOf()+"?page_size=1", "ops-author")
	for name, other := range map[string]string{"the revisions of an asset": ofRevisions, "the queue of another workspace": ofOperations} {
		if other == "" {
			t.Fatalf("%s has no second page", name)
		}
		s.expect(name, "author", []exchange{{http.MethodGet, queue + "?page_token=" + other, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"}})
	}

	// The grant of an author that holds nothing else in the product is
	// revoked: the next page is the answer to a stranger.
	var membership string
	if err := s.owner.QueryRow(t.Context(), `SELECT membership_id::text FROM productgovernance.workspace_membership
WHERE product_id = $1 AND workspace_id = $2 AND principal_subject = 'author' AND role = 'internal_author' AND revoked_at IS NULL`,
		w.product, w.workspace).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	s.revoke("/v1/products/"+w.product+"/workspaces/"+w.workspace+"/memberships/"+membership, "owner")
	s.expect("the grant was revoked between two pages", "author", []exchange{
		{http.MethodGet, queue + "?page_size=2&page_token=" + token, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{http.MethodGet, queue, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})
	// Those who hold their grants read on.
	if got, _ := s.page(queue+"?page_size=100&page_token="+token, "coauthor"); len(got) != len(made)-2 {
		t.Errorf("the second author reads %d reviews behind the position, want %d", len(got), len(made)-2)
	}

	// Someone holds two grants of the workspace: when it loses one it reads on
	// by the other, and when it loses both it is a stranger.
	authoring := s.join(w.product, w.workspace, "owner", "both", "internal_author")
	reviewing := s.join(w.product, w.workspace, "owner", "both", "internal_reviewer")
	_, bothToken := s.page(queue+"?page_size=2", "both")
	s.revoke(reviewing, "owner")
	if got, _ := s.page(queue+"?page_size=100&page_token="+bothToken, "both"); len(got) != len(made)-2 {
		t.Errorf("one grant is revoked and the other reads %d reviews behind the position, want %d", len(got), len(made)-2)
	}
	s.revoke(authoring, "owner")
	s.expect("both grants were revoked", "both", []exchange{
		{http.MethodGet, queue + "?page_size=100&page_token=" + bothToken, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})
}

// What Product Governance cannot read the reviews of is refused to whoever
// lists them, as any other who may not read is, and is not failed: a request
// that names no caller is refused, and a product that is no identifier is a
// product that is not there. Product Governance answers both before it reads
// anything, and the scope turns that into the facts the use case decides from.
// The transport authenticates every caller and takes the identifier of a
// product from a path that names one, so no request through it comes this far,
// and the scope is asked directly.
func TestWhatTheGovernanceCannotReadTheReviewsOfIsRefusedAndNotFailed(t *testing.T) {
	scopes := reviewScopes{governance: &governance.ProductGovernance{}}
	caller := checkdomain.Principal{Issuer: "https://issuer.example", Subject: "reader"}
	const product, workspace = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6", "4f2d7b0e-85a3-4c1b-a9d4-c3f6a5b7d8e9"

	for name, c := range map[string]struct {
		caller  checkdomain.Principal
		product string
		refused bool
	}{
		"no caller":                       {checkdomain.Principal{}, product, true},
		"a caller without a subject":      {checkdomain.Principal{Issuer: caller.Issuer}, product, true},
		"a caller without an issuer":      {checkdomain.Principal{Subject: caller.Subject}, product, true},
		"no caller and no product at all": {checkdomain.Principal{}, "", true},
		"a product that is a key":         {caller, "cargopai", false},
		"no product":                      {caller, "", false},
		"words that are no identifier":    {caller, "not-an-identifier", false},
	} {
		scope, err := scopes.ReviewScope(t.Context(), c.caller, c.product, workspace)
		if scope != (checkports.WorkspaceScope{}) {
			t.Errorf("%s: read as %+v, want no facts", name, scope)
		}
		if c.refused {
			if !errors.Is(err, governance.ErrForbidden) || !errors.Is(err, checks.ErrReviewsForbidden) {
				t.Errorf("%s: %v, want a refusal that says %v and %v", name, err, governance.ErrForbidden, checks.ErrReviewsForbidden)
			}
		} else if err != nil {
			t.Errorf("%s: %v, want no failure: the product is not there", name, err)
		}
	}

	// Through the use case, whoever the scope refuses is refused and is not
	// failed.
	queue, err := checks.NewReviewQueue(checks.QueueDependencies{
		Reviews: nobodyReads{}, Scopes: scopes, Audit: func(context.Context, checkports.AuditEntry) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"cargopai", "", "not-an-identifier"} {
		if _, err := queue.ListWorkspaceReviews(t.Context(), checks.ListWorkspaceReviews{
			Caller: caller, ProductID: product, WorkspaceID: workspace, PageSize: 10,
		}); !errors.Is(err, checks.ErrProductNotFound) {
			t.Errorf("the product %q: %v, want %v", product, err, checks.ErrProductNotFound)
		}
	}
}

// nobodyReads is the reviews of a queue that must not be asked.
type nobodyReads struct{}

func (nobodyReads) ListInWorkspace(context.Context, string, string, checkports.ReviewFilter, *checkports.Position, int) ([]checkdomain.Review, error) {
	return nil, errors.New("the reviews were asked for a caller that does not read")
}
