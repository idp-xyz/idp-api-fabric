package domain

import (
	"errors"
	"fmt"
	"time"
)

// Outcome is how an attempt to act ended.
type Outcome string

const (
	// OutcomeSucceeded is an action that was allowed and took effect.
	OutcomeSucceeded Outcome = "SUCCEEDED"
	// OutcomeDenied is an action that was refused. Nothing changed.
	OutcomeDenied Outcome = "DENIED"
)

// Actor is the principal that acted: a subject as one issuer names it. It is
// a reference into an identity provider and carries no credential.
type Actor struct {
	Issuer  string
	Subject string
}

// AuditRecord is the minimal fact of one attempt to act (INV-13, F05 §14):
// who acted, what was attempted on which resource, when, how it ended and
// under which rule. A record is never changed or removed once it is written.
//
// It holds references only. A token, a request body or the name of a private
// object has no place in it.
type AuditRecord struct {
	ID         string
	RecordedAt time.Time
	Actor      Actor
	// Action is the smallest unit of what was attempted, such as
	// product.register.
	Action  string
	Outcome Outcome
	// Rule identifies the rule that allowed or refused the action, as the
	// access matrix of F05 names it: AC-08, DENY-03.
	Rule string
	// ProductID is the product the action is scoped to. It is empty for an
	// action in the platform scope, which concerns no single product.
	ProductID string
	// ResourceType and ResourceID name what was acted on. The identifier is
	// empty when the attempt left no resource behind.
	ResourceType string
	ResourceID   string
	// CorrelationID ties the record to the request, its log lines and its
	// events.
	CorrelationID string
}

// ErrAuditRecordIncomplete reports a record that does not say enough to be
// evidence of anything.
var ErrAuditRecordIncomplete = errors.New("an audit record is incomplete")

// Validate reports what a record lacks to be a fact worth keeping.
func (r AuditRecord) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("%w: it has no identifier", ErrAuditRecordIncomplete)
	case r.RecordedAt.IsZero():
		return fmt.Errorf("%w: it has no time", ErrAuditRecordIncomplete)
	case r.Actor.Issuer == "" || r.Actor.Subject == "":
		return fmt.Errorf("%w: it names no actor", ErrAuditRecordIncomplete)
	case r.Action == "":
		return fmt.Errorf("%w: it names no action", ErrAuditRecordIncomplete)
	case r.Outcome != OutcomeSucceeded && r.Outcome != OutcomeDenied:
		return fmt.Errorf("%w: its outcome is not known", ErrAuditRecordIncomplete)
	case r.Rule == "":
		return fmt.Errorf("%w: it names no rule", ErrAuditRecordIncomplete)
	case r.ResourceType == "":
		return fmt.Errorf("%w: it names no kind of resource", ErrAuditRecordIncomplete)
	case r.Outcome == OutcomeSucceeded && r.ResourceID == "":
		return fmt.Errorf("%w: a successful action names the resource it acted on", ErrAuditRecordIncomplete)
	case r.CorrelationID == "":
		return fmt.Errorf("%w: it has no correlation identifier", ErrAuditRecordIncomplete)
	}
	return nil
}
