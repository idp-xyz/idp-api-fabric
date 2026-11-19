package domain

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The statuses Contract Registry gives a revision as its review goes on
// (F03 §8.1). The registry owns the status and decides which moves there
// are; a review asks for the move that belongs to what it records.
const (
	RevisionInReview  = "IN_REVIEW"
	RevisionWithdrawn = "WITHDRAWN"
)

// Target is one audience in one environment. The scope of a review is the
// targets a revision is asked to be approved for: an approval holds for the
// content, the policy and the scope it was given for, and for nothing wider
// (F03 §4, INV-07).
type Target struct {
	Audience    string
	Environment string
}

// The audiences and the environments there are, as Product Governance names
// them.
var (
	audiences    = []string{"public", "partner", "internal"}
	environments = []string{"sandbox", "production"}
)

const (
	// MaxReviewNote and MaxReviewReason bound what a review says in words,
	// in characters.
	MaxReviewNote   = 500
	MaxReviewReason = 1000
)

var (
	// ErrInvalidReview reports a review the model refuses for what it says.
	ErrInvalidReview = errors.New("the review is invalid")
	// ErrCheckStale reports a check that was not made under the policy and
	// by the tools a check is made with now. What it concluded is evidence
	// of then, and a review is of now (F04 §7.2).
	ErrCheckStale = errors.New("the check was not made under the current policy and tools")
	// ErrReviewBlocked reports a check with a rule nothing waives that
	// failed or could not be judged. No decision approves such a revision,
	// so none is asked for.
	ErrReviewBlocked = errors.New("rules that nothing waives did not pass")
	// ErrReviewConcluded reports a review that has its conclusion: it is
	// concluded once.
	ErrReviewConcluded = errors.New("the review was concluded already")
	// ErrNotRequester reports a withdrawal by someone who did not ask for
	// the review.
	ErrNotRequester = errors.New("a review is withdrawn by whoever asked for it")
	// ErrReviewDamaged reports a stored review that is not one this model
	// recorded.
	ErrReviewDamaged = errors.New("the stored review is not the one that was recorded")
)

func invalidReview(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidReview, reason)
}

// Current reports whether a check was made under the policy and by tools a
// check is made with now.
func Current(run CheckRun, policy Policy, tools []Tool) bool {
	if run.PolicyDigest != policy.Digest() || len(run.Tools) == 0 {
		return false
	}
	for _, tool := range run.Tools {
		if !slices.Contains(tools, tool) {
			return false
		}
	}
	return true
}

// Blockers returns the rules of a check that stand between its revision and
// an approval: the ones that failed or could not be judged. unwaivable are
// those nothing waives, and no decision approves a revision that has one;
// waivable are those a reviewer may waive, each for a reason and a time.
//
// A rule that warns blocks nothing, and neither does one no tool evaluates:
// the check says of each that it was not run, a decision is bound to that
// check, and what is still unknown when a release is made is for the release
// to refuse (F04 §7.2).
func Blockers(run CheckRun) (unwaivable, waivable []string) {
	for _, outcome := range run.Outcomes {
		if outcome.Status != StatusFail && outcome.Status != StatusUnknown {
			continue
		}
		if outcome.Rule.Waivable {
			waivable = append(waivable, outcome.Rule.ID)
		} else {
			unwaivable = append(unwaivable, outcome.Rule.ID)
		}
	}
	return unwaivable, waivable
}

// ReviewRequest asks for a decision on one revision: on the content one
// check read, for the scope it names. It is written once and never changed.
type ReviewRequest struct {
	ID       string
	Revision RevisionRef
	// CheckRunID names the check the decision is asked on, and InputDigest
	// is what that check read: the closure of the revision.
	CheckRunID  string
	InputDigest string
	// Scope is in the order of its audiences, then of its environments.
	Scope       []Target
	Note        string
	RequestedBy Principal
	RequestedAt time.Time
}

// RequestReview asks for a decision on the revision a check is of.
//
// The check has to be one a decision can rest on now: made under the policy
// and by the tools of today (ErrCheckStale), and without a rule nothing
// waives that failed or could not be judged (ErrReviewBlocked, naming the
// rules).
func RequestReview(id string, run CheckRun, policy Policy, tools []Tool, scope []Target, note string, by Principal, at time.Time) (ReviewRequest, error) {
	switch {
	case id == "" || run.ID == "" || run.Revision.RevisionID == "" || by.Issuer == "" || by.Subject == "":
		return ReviewRequest{}, invalidReview("the review does not say what it is of or who asked for it")
	case note != "" && !plainText(note, MaxReviewNote):
		return ReviewRequest{}, invalidReview("the note is not one line of at most " + fmt.Sprint(MaxReviewNote) + " characters")
	}
	ordered, err := scopeOf(scope)
	if err != nil {
		return ReviewRequest{}, err
	}
	if !Current(run, policy, tools) {
		return ReviewRequest{}, ErrCheckStale
	}
	if unwaivable, _ := Blockers(run); len(unwaivable) > 0 {
		return ReviewRequest{}, fmt.Errorf("%w: %s", ErrReviewBlocked, strings.Join(unwaivable, ", "))
	}
	return ReviewRequest{
		ID: id, Revision: run.Revision, CheckRunID: run.ID, InputDigest: run.InputDigest, Scope: ordered, Note: note,
		RequestedBy: by, RequestedAt: at.UTC().Truncate(time.Microsecond),
	}, nil
}

// scopeOf holds a scope to what one is: at least one target, each an
// audience in an environment there is, none of them twice.
func scopeOf(scope []Target) ([]Target, error) {
	if len(scope) < 1 || len(scope) > len(audiences)*len(environments) {
		return nil, invalidReview("a review names between one target and every one there is")
	}
	ordered := slices.Clone(scope)
	slices.SortFunc(ordered, func(a, b Target) int {
		return cmp.Or(strings.Compare(a.Audience, b.Audience), strings.Compare(a.Environment, b.Environment))
	})
	for i, target := range ordered {
		if !slices.Contains(audiences, target.Audience) || !slices.Contains(environments, target.Environment) {
			return nil, invalidReview("a target is an audience in an environment there is")
		}
		if i > 0 && ordered[i-1] == target {
			return nil, invalidReview("a target is named twice")
		}
	}
	return ordered, nil
}

// ReviewOutcome is how a review ended.
type ReviewOutcome string

// ReviewWithdrawn: whoever asked for the review took the request back.
const ReviewWithdrawn ReviewOutcome = "WITHDRAWN"

// Conclusion is the end of a review: how it ended, who ended it and when. It
// is written once and never changed.
type Conclusion struct {
	ReviewID    string
	Outcome     ReviewOutcome
	Reason      string
	ConcludedBy Principal
	ConcludedAt time.Time
}

// Review is a request with its conclusion, once it has one.
type Review struct {
	Request    ReviewRequest
	Conclusion *Conclusion
}

// ReviewOpen is the status of a review that has no conclusion yet.
const ReviewOpen = "IN_REVIEW"

// Status is where the review stands: open, or the way it ended.
func (r Review) Status() string {
	if r.Conclusion == nil {
		return ReviewOpen
	}
	return string(r.Conclusion.Outcome)
}

// Withdraw takes a request back. Only whoever asked for the review does, and
// only while it is open.
func Withdraw(review Review, by Principal, reason string, at time.Time) (Conclusion, error) {
	switch {
	case review.Conclusion != nil:
		return Conclusion{}, ErrReviewConcluded
	case by != review.Request.RequestedBy:
		return Conclusion{}, ErrNotRequester
	case reason != "" && !plainText(reason, MaxReviewReason):
		return Conclusion{}, invalidReview("the reason is not one line of at most " + fmt.Sprint(MaxReviewReason) + " characters")
	}
	return Conclusion{
		ReviewID: review.Request.ID, Outcome: ReviewWithdrawn, Reason: reason, ConcludedBy: by,
		ConcludedAt: notBefore(at, review.Request.RequestedAt),
	}, nil
}

// notBefore is the instant at, or earliest when a clock that was set back
// would make something end before it began.
func notBefore(at, earliest time.Time) time.Time {
	at = at.UTC().Truncate(time.Microsecond)
	if at.Before(earliest) {
		return earliest
	}
	return at
}

// RecordedReview reads a review back from what storage holds of it. It fails
// with ErrReviewDamaged unless the review is one this model could have
// recorded: it says what it is of and who asked for it, its scope is one,
// and its conclusion, if it has one, is one there is and does not precede
// the request.
func RecordedReview(review Review) (Review, error) {
	request := review.Request
	if request.ID == "" || request.CheckRunID == "" || request.Revision.RevisionID == "" ||
		!digestForm.MatchString(request.InputDigest) || request.RequestedBy.Issuer == "" || request.RequestedBy.Subject == "" {
		return Review{}, ErrReviewDamaged
	}
	if ordered, err := scopeOf(request.Scope); err != nil || !slices.Equal(ordered, request.Scope) {
		return Review{}, ErrReviewDamaged
	}
	if conclusion := review.Conclusion; conclusion != nil {
		if conclusion.ReviewID != request.ID || !conclusion.Outcome.known() || conclusion.ConcludedBy.Issuer == "" ||
			conclusion.ConcludedBy.Subject == "" || conclusion.ConcludedAt.Before(request.RequestedAt) {
			return Review{}, ErrReviewDamaged
		}
	}
	return review, nil
}

func (o ReviewOutcome) known() bool { return o == ReviewWithdrawn }
