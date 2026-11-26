package domain

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"time"
)

// A review asked for a decision on a revision and was concluded once. No
// review is asked for or decided any more (CR-0001): a release is prepared
// on a check (GateRelease). The reviews that were recorded stay as they
// were, and are read back as evidence of then.

// Target is one audience in one environment. The scope of a review is the
// targets a revision was asked to be approved for: an approval held for the
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

// MaxReviewReason bounds what a review says in words, and what a waiver
// says of why, in characters.
const MaxReviewReason = 1000

// ErrReviewDamaged reports a stored review that is not one this model
// recorded.
var ErrReviewDamaged = errors.New("the stored review is not the one that was recorded")

// ReviewRequest asked for a decision on one revision: on the content one
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
	// Supplementary says that the revision was approved already when the
	// decision was asked for: the request asks for another approval of the
	// same content, on a check of today and for the scope it names (F03
	// §8.1). Such a review moves nothing. The revision stays approved while
	// it is open and however it ends, and what was approved before holds
	// or lapses on its own terms.
	Supplementary bool
}

// scopeOf holds a scope to what one is: at least one target, each an
// audience in an environment there is, none of them twice. It returns the
// scope in the order a review keeps it in.
func scopeOf(scope []Target) ([]Target, bool) {
	if len(scope) < 1 || len(scope) > len(audiences)*len(environments) {
		return nil, false
	}
	ordered := slices.Clone(scope)
	slices.SortFunc(ordered, func(a, b Target) int {
		return cmp.Or(strings.Compare(a.Audience, b.Audience), strings.Compare(a.Environment, b.Environment))
	})
	for i, target := range ordered {
		if !slices.Contains(audiences, target.Audience) || !slices.Contains(environments, target.Environment) ||
			(i > 0 && ordered[i-1] == target) {
			return nil, false
		}
	}
	return ordered, true
}

// ReviewOutcome is how a review ended.
type ReviewOutcome string

const (
	// ReviewWithdrawn: whoever asked for the review took the request back.
	ReviewWithdrawn ReviewOutcome = "WITHDRAWN"
	// ReviewApproved: a reviewer approved the revision, for the content the
	// check read, the scope of the request and until a time.
	ReviewApproved ReviewOutcome = "APPROVED"
	// ReviewRejected: a reviewer rejected the revision, and said why.
	ReviewRejected ReviewOutcome = "REJECTED"
)

// Waiver lets one rule that did not pass stand: for a reason, with what
// makes up for it if anything does, and until a time (F04 §7.2). It is part
// of the approval it was given with, so it holds for the content, the scope
// and the reviewer of that approval and for nothing else.
type Waiver struct {
	RuleID              string
	Reason              string
	CompensatingControl string
	ExpiresAt           time.Time
}

// Conclusion is the end of a review: how it ended, who ended it and when. It
// is written once and never changed.
type Conclusion struct {
	ReviewID    string
	Outcome     ReviewOutcome
	Reason      string
	ConcludedBy Principal
	ConcludedAt time.Time
	// ValidUntil is until when an approval holds. It is the zero time for
	// every other outcome.
	ValidUntil time.Time
	// Waivers are the waivers of an approval, in the order of their rules.
	Waivers []Waiver
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
	if ordered, ok := scopeOf(request.Scope); !ok || !slices.Equal(ordered, request.Scope) {
		return Review{}, ErrReviewDamaged
	}
	if conclusion := review.Conclusion; conclusion != nil {
		if conclusion.ReviewID != request.ID || conclusion.ConcludedBy.Issuer == "" ||
			conclusion.ConcludedBy.Subject == "" || conclusion.ConcludedAt.Before(request.RequestedAt) || !conclusion.whole() {
			return Review{}, ErrReviewDamaged
		}
	}
	return review, nil
}

// whole reports whether a conclusion says what its outcome asks of it and
// nothing else: an approval holds until a time after it was given and has
// its waivers in order, each for a reason and until a time the approval
// still holds; a rejection says why; and neither a rejection nor a
// withdrawal holds for a time or waives a rule.
func (c Conclusion) whole() bool {
	switch c.Outcome {
	case ReviewApproved:
		if !c.ValidUntil.After(c.ConcludedAt) {
			return false
		}
		for i, waiver := range c.Waivers {
			if waiver.RuleID == "" || !plainText(waiver.Reason, MaxReviewReason) ||
				(waiver.CompensatingControl != "" && !plainText(waiver.CompensatingControl, MaxReviewReason)) ||
				!waiver.ExpiresAt.After(c.ConcludedAt) || waiver.ExpiresAt.After(c.ValidUntil) ||
				(i > 0 && c.Waivers[i-1].RuleID >= waiver.RuleID) {
				return false
			}
		}
		return true
	case ReviewRejected:
		return c.Reason != "" && c.ValidUntil.IsZero() && len(c.Waivers) == 0
	case ReviewWithdrawn:
		return c.ValidUntil.IsZero() && len(c.Waivers) == 0
	default:
		return false
	}
}
