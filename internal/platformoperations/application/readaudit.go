package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
)

var (
	// ErrProductNotFound reports that there is no such product, or that the
	// caller may not know whether there is. The two are answered alike, so
	// that an identifier cannot be probed (F05 §5.2).
	ErrProductNotFound = errors.New("no such product")

	// ErrForbidden reports a caller that may know the product exists and is
	// no auditor of it.
	ErrForbidden = errors.New("the caller may not read the audit records of the product")

	// ErrRefusalNotAudited accompanies a refusal whose audit record could
	// not be written. The refusal stands; this is for whoever operates the
	// service, and it never changes what the caller is told.
	ErrRefusalNotAudited = errors.New("the refusal to read the audit records could not be audited")
)

const (
	// ActionAuditSearch is what the access matrix of F05 calls the reading of
	// audit records (AC-29).
	ActionAuditSearch = "audit.search"
	// ResourceAuditRecord is its resource, as the matrix names it.
	ResourceAuditRecord = "AuditRecord"
	// ruleNoRole refuses whoever holds no role that permits the action
	// (DENY-03, missing_active_membership_or_grant), as every refusal for
	// lack of a role is recorded. AC-29 is the rule that permits it; a read
	// that is permitted is not recorded.
	ruleNoRole = "DENY-03"

	// DefaultPageSize is how many records a page holds when the caller names
	// no size, and MaxPageSize the most it gets. The contract of the API
	// states the same bounds, and a test holds the two together.
	DefaultPageSize = 50
	MaxPageSize     = 100
)

// Refusals records what was refused. RecordAudit is what it is wired with.
type Refusals interface {
	Record(ctx context.Context, entry AuditEntry) error
}

// ReadAuditDependencies are the ports ReadAudit is wired with. All are
// required.
type ReadAuditDependencies struct {
	Transactor ports.Transactor
	Scopes     ports.AuditScopes
	Reader     ports.AuditReader
	Refusals   Refusals
}

// ReadAudit reads the audit trail back to whoever may read it: the auditors
// of a product, as far as that product goes (AC-29).
type ReadAudit struct {
	deps ReadAuditDependencies
}

// NewReadAudit builds the use case.
func NewReadAudit(deps ReadAuditDependencies) (*ReadAudit, error) {
	missing := map[string]bool{
		"a transactor":                  deps.Transactor == nil,
		"the scopes of the callers":     deps.Scopes == nil,
		"a reader of the audit records": deps.Reader == nil,
		"a record of what is refused":   deps.Refusals == nil,
	}
	for what, absent := range missing {
		if absent {
			return nil, fmt.Errorf("reading the audit trail is wired without %s", what)
		}
	}
	return &ReadAudit{deps: deps}, nil
}

// ListAuditRecords asks for a page of the audit records of one product.
type ListAuditRecords struct {
	Caller        domain.Actor
	CorrelationID string
	ProductID     string
	Filter        domain.AuditFilter
	// PageSize is how many records the page holds at most. Zero asks for
	// DefaultPageSize.
	PageSize int
	// After is the position the page starts behind: Next of the page before.
	// Nil starts the list.
	After *ports.Position
}

// AuditPage is one page of the audit records of a product, newest first.
type AuditPage struct {
	Items []domain.AuditRecord
	// Next is the position the page after this one starts behind: that of
	// the last record here. It is nil when nothing follows.
	Next *ports.Position
}

// List returns a page of the audit records of a product to an auditor of it:
// to a principal that holds the role of an auditor in the product now, and to
// nobody else. An owner, a member and a platform administrator do not read
// them by being one.
//
// Whoever may know the product exists and is no auditor is told it may not;
// to anyone else, and for a product that does not exist, there is no such
// product. The refusal is recorded, unless there is no product to record it
// under: it is the audit trail of the product that holds who tried to read
// it. A read that is answered leaves no record, as no other read does.
//
// What the caller holds is read with every page, under a lock that keeps its
// role from being revoked until the records are read: a page token grants
// nothing, and a role that was revoked reads no page after it. What the
// request says of the records is looked at once the caller is known to read
// them: whoever is refused is told nothing else.
func (r *ReadAudit) List(ctx context.Context, query ListAuditRecords) (AuditPage, error) {
	if query.Caller.Issuer == "" || query.Caller.Subject == "" {
		return AuditPage{}, fmt.Errorf("%w: the request carries no principal", ErrForbidden)
	}
	if !domain.ValidIdentifier(query.ProductID) {
		return AuditPage{}, ErrProductNotFound
	}

	var page AuditPage
	var refusal error
	err := r.deps.Transactor.WithinTransaction(ctx, func(tx context.Context) error {
		scope, err := r.deps.Scopes.AuditScope(tx, query.Caller, query.ProductID)
		if err != nil {
			return fmt.Errorf("read what the caller is to the product: %w", err)
		}
		switch {
		case !scope.Found:
			return ErrProductNotFound
		case !scope.Known:
			refusal = ErrProductNotFound
		case !scope.Auditor:
			refusal = ErrForbidden
		}
		if refusal != nil {
			return refusal
		}

		filter := query.Filter.Normalized()
		if err := filter.Validate(); err != nil {
			return err
		}
		store, size, err := paging(query.PageSize, query.After)
		if err != nil {
			return err
		}
		records, err := r.deps.Reader.List(tx, query.ProductID, filter, store)
		if err != nil {
			return fmt.Errorf("list the audit records of a product: %w", err)
		}
		page = pageOf(records, size)
		return nil
	})
	if refusal != nil {
		return AuditPage{}, r.refuse(ctx, query, refusal)
	}
	if err != nil {
		return AuditPage{}, err
	}
	return page, nil
}

// refuse records a refusal and returns its error. It is called outside any
// transaction: a refusal changes nothing, so its record commits on its own.
//
// The caller is refused whether or not the record could be written. A record
// that could not is reported with the refusal, as ErrRefusalNotAudited.
func (r *ReadAudit) refuse(ctx context.Context, query ListAuditRecords, cause error) error {
	if err := r.deps.Refusals.Record(ctx, AuditEntry{
		Actor: query.Caller, Action: ActionAuditSearch, Outcome: domain.OutcomeDenied, Rule: ruleNoRole,
		ProductID: query.ProductID, ResourceType: ResourceAuditRecord, CorrelationID: query.CorrelationID,
	}); err != nil {
		return fmt.Errorf("%w; %w: %v", cause, ErrRefusalNotAudited, err)
	}
	return cause
}

// paging checks the part of a list a query asks for, and says what the store
// is asked for: one record more than the page holds, to know whether a page
// follows without counting what the caller may read.
func paging(pageSize int, after *ports.Position) (page ports.Page, size int, err error) {
	switch {
	case pageSize == 0:
		size = DefaultPageSize
	case pageSize < 0 || pageSize > MaxPageSize:
		return ports.Page{}, 0, fmt.Errorf("%w: a page holds 1 to %d records", domain.ErrInvalidAuditQuery, MaxPageSize)
	default:
		size = pageSize
	}
	if after != nil {
		// A position is one a page of this list ended at: the identifier of
		// a record, and the instant it was recorded.
		if after.At.IsZero() || !domain.ValidIdentifier(after.ID) {
			return ports.Page{}, 0, fmt.Errorf("%w: a page starts behind a position of the list", domain.ErrInvalidAuditQuery)
		}
		behind := *after
		page.After = &behind
	}
	page.Limit = size + 1
	return page, size, nil
}

// pageOf is the page of at most size records in what a store returned when it
// was asked for one more.
func pageOf(records []domain.AuditRecord, size int) AuditPage {
	if len(records) <= size {
		return AuditPage{Items: records}
	}
	last := records[size-1]
	return AuditPage{Items: records[:size], Next: &ports.Position{At: last.RecordedAt, ID: last.ID}}
}
