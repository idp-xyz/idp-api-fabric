package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	auditing "go.idp.xyz/idp-api-fabric/internal/platformoperations/application"
	auditdomain "go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	auditports "go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// AuditReading is the use case of reading the audit records of a product, as
// the transport calls it. It decides for itself whether the caller may read
// them: the transport says who the caller is and nothing more.
type AuditReading interface {
	List(ctx context.Context, query auditing.ListAuditRecords) (auditing.AuditPage, error)
}

// listOfAuditRecords is the list a page token of the audit records of one
// product is of. The token names a position in the trail of that product and
// in no other: the one of another product, or of another list, is none.
func listOfAuditRecords(productID string) string { return "audit-records/" + productID }

func (o operations) ListAuditRecords(ctx context.Context, request ListAuditRecordsRequestObject) (ListAuditRecordsResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	list := listOfAuditRecords(request.ProductID)
	params := request.Params
	size, position, err := paging(list, params.PageSize, params.PageToken)
	if err != nil {
		return nil, err
	}
	var after *auditports.Position
	if position != nil {
		after = &auditports.Position{At: position.At, ID: position.ID}
	}
	page, err := o.audit.List(ctx, auditing.ListAuditRecords{
		Caller:        auditdomain.Actor{Issuer: principal.Issuer, Subject: principal.Subject},
		CorrelationID: correlationID, ProductID: request.ProductID,
		Filter: auditdomain.AuditFilter{
			RecordedSince: params.RecordedSince, RecordedBefore: params.RecordedBefore,
			Action: valueOf(params.Action), Outcome: auditdomain.Outcome(valueOf(params.Outcome)),
			ActorIssuer: valueOf(params.ActorIssuer), ActorSubject: valueOf(params.ActorSubject),
			ResourceType: valueOf(params.ResourceType), ResourceID: valueOf(params.ResourceID),
			CorrelationID: valueOf(params.CorrelationID),
		},
		PageSize: size, After: after,
	})
	if err != nil {
		return nil, err
	}
	items, err := listed(page.Items, auditRecordOf)
	if err != nil {
		return nil, err
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return ListAuditRecords200JSONResponse{
		Body:    AuditRecordList{Items: items, NextPageToken: next},
		Headers: ListAuditRecords200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// auditRecordOf is the audit record as the contract shows it: every fact the
// trail holds of it. The actor is the principal as it was recorded. What the
// record does not have, a resource it left behind, is left out and not sent
// empty.
//
// An outcome the contract does not declare is not sent: a caller would branch
// on a value it was never told of.
func auditRecordOf(record auditdomain.AuditRecord) (AuditRecord, error) {
	outcome := AuditOutcome(record.Outcome)
	if !outcome.Valid() {
		return AuditRecord{}, fmt.Errorf("the audit record has the outcome %q, which the contract does not declare", record.Outcome)
	}
	shown := AuditRecord{
		AuditID: record.ID, RecordedAt: record.RecordedAt,
		Actor:  Principal{Issuer: record.Actor.Issuer, Subject: record.Actor.Subject},
		Action: record.Action, Outcome: outcome, RuleID: record.Rule, ProductID: record.ProductID,
		ResourceType: record.ResourceType, CorrelationID: record.CorrelationID,
	}
	if record.ResourceID != "" {
		resourceID := record.ResourceID
		shown.ResourceID = &resourceID
	}
	return shown, nil
}

// auditProblem is the problem a failed reading of the audit records is
// answered with, and false for a failure it does not name.
func auditProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, auditing.ErrProductNotFound):
		return problem(http.StatusNotFound, CodeProductNotFound, ""), true
	case errors.Is(err, auditing.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller may not perform this action."), true
	case errors.Is(err, auditdomain.ErrInvalidAuditQuery):
		// The contract states the rules of the model, apart from the one
		// that a range of time does not end where it begins, which only the
		// model can say.
		return problem(http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""), true
	default:
		return Problem{}, false
	}
}
