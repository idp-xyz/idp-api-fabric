package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Principal struct {
	Issuer  string
	Subject string
}

type Request struct {
	Caller        Principal
	ProductID     string
	TargetID      string
	AssetID       string
	CorrelationID string
}

type Target struct {
	ID               string
	ProductID        string
	AudienceConfigID string
	PortalKey        string
	Audience         string
	Environment      string
	BlockGeneration  int64
	CreatedAt        time.Time
}

type Slot struct {
	ID              string
	ProductID       string
	TargetID        string
	AssetID         string
	ActiveReleaseID string
	Generation      int64
}

// SlotView is a slot as it is read: with what stands between the release it
// points at and whoever asks for the artifact.
type SlotView struct {
	Slot
	Blocked bool
	// Unapproved is why the approval the active release rests on does not
	// hold for the target now: the refusal an activation of that release
	// would meet. It is nil when the approval holds, and for a slot that
	// points at no release. The artifact of a release whose approval does
	// not hold is not served, and the read that is refused says nothing of
	// why; the slot says it, to whoever may read the slot.
	Unapproved error
}

func (s Slot) Visibility() string {
	switch {
	case s.ActiveReleaseID != "":
		return "ACTIVE"
	case s.Generation > 0:
		return "WITHDRAWN"
	default:
		return "EMPTY"
	}
}

type Action string

const (
	Activate Action = "ACTIVATE"
	Withdraw Action = "WITHDRAW"
	Restore  Action = "RESTORE"
)

type Operation struct {
	ID                string
	SlotID            string
	Generation        int64
	Action            Action
	PreviousReleaseID string
	ReleaseID         string
	Reason            string
	By                Principal
	At                time.Time
}

type Change struct {
	Action             Action
	ExpectedGeneration int64
	ReleaseID          string
	Reason             string
	IdempotencyKey     string
}

// The forms a request is held to. The contract of the API states the same
// patterns and the same bound, and a test holds the two together.
const (
	PortalKeyPattern      = `^[a-z0-9][a-z0-9-]{0,62}$`
	IdempotencyKeyPattern = `^[A-Za-z0-9._:-]{1,128}$`
	// ReasonMaxLength bounds a reason in characters.
	ReasonMaxLength = 500
)

var (
	ErrInvalid         = errors.New("the publication request is invalid")
	ErrGeneration      = errors.New("the slot has another generation")
	ErrIdempotency     = errors.New("the key is already bound to another publication request")
	ErrAlreadyActive   = errors.New("the requested release is active already")
	ErrNoActiveRelease = errors.New("the slot has no active release to withdraw")
	ErrNotInHistory    = errors.New("the release has not been active on this slot")
	portalKey          = regexp.MustCompile(PortalKeyPattern)
	idempotencyKey     = regexp.MustCompile(IdempotencyKeyPattern)
)

func ValidatePortalKey(key string) error {
	if !portalKey.MatchString(key) {
		return ErrInvalid
	}
	return nil
}

func ValidateReason(reason string) error {
	if reason == "" || reason != strings.TrimSpace(reason) || utf8.RuneCountInString(reason) > ReasonMaxLength ||
		strings.ContainsFunc(reason, unicode.IsControl) {
		return ErrInvalid
	}
	return nil
}

func (c Change) Validate() error {
	if c.ExpectedGeneration < 0 || c.ExpectedGeneration == math.MaxInt64 || !idempotencyKey.MatchString(c.IdempotencyKey) {
		return ErrInvalid
	}
	switch c.Action {
	case Activate:
		if c.ReleaseID == "" {
			return ErrInvalid
		}
	case Withdraw:
		if c.ReleaseID != "" || c.Reason == "" {
			return ErrInvalid
		}
	case Restore:
		if c.ReleaseID == "" || c.Reason == "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if c.Reason != "" {
		return ValidateReason(c.Reason)
	}
	return nil
}

func (c Change) KeyDigest() string { return fmt.Sprintf("%x", sha256.Sum256([]byte(c.IdempotencyKey))) }

func (op Operation) Matches(c Change, actor Principal) bool {
	return op.By == actor && op.Generation-1 == c.ExpectedGeneration &&
		op.Action == c.Action && op.ReleaseID == c.ReleaseID && op.Reason == c.Reason
}

func (s Slot) Operation(c Change, id string, by Principal, at time.Time) (Operation, error) {
	switch {
	case s.Generation != c.ExpectedGeneration:
		return Operation{}, ErrGeneration
	case c.Action == Withdraw && s.ActiveReleaseID == "":
		return Operation{}, ErrNoActiveRelease
	case c.Action != Withdraw && c.ReleaseID == s.ActiveReleaseID:
		return Operation{}, ErrAlreadyActive
	}
	return Operation{
		ID: id, SlotID: s.ID, Generation: s.Generation + 1, Action: c.Action, PreviousReleaseID: s.ActiveReleaseID,
		ReleaseID: c.ReleaseID, Reason: c.Reason, By: by, At: at,
	}, nil
}

// AccessBlock is independent of the current release pointer. Empty AssetID
// covers the target, and an empty ReleaseID covers all releases of its asset.
type AccessBlock struct {
	ID              string
	ProductID       string
	TargetID        string
	AssetID         string
	ReleaseID       string
	Generation      int64
	Reason          string
	CreatedBy       Principal
	CreatedAt       time.Time
	ClearGeneration int64
	ClearReason     string
	ClearedBy       Principal
	ClearedAt       time.Time
}

func (b AccessBlock) Active() bool { return b.ClearedAt.IsZero() }
