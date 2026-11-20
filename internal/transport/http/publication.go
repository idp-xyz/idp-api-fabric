package http

import (
	"bytes"
	"context"

	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

type Publications interface {
	RegisterTarget(context.Context, domain.Request, string, string) (publication.TargetRegistration, error)
	GetTarget(context.Context, domain.Request) (domain.Target, error)
	ListTargets(context.Context, domain.Request, *ports.TargetPosition, int) (publication.TargetPage, error)
	GetSlot(context.Context, domain.Request) (domain.SlotView, error)
	ChangeSlot(context.Context, domain.Request, domain.Change) (domain.Operation, error)
	ListOperations(context.Context, domain.Request, int64, int) (publication.OperationPage, error)
	ActiveArtifact(context.Context, domain.Request) (ports.Artifact, error)
	BlockAccess(context.Context, domain.Request, string, string) (publication.BlockResult, error)
	GetBlock(context.Context, domain.Request, string) (domain.AccessBlock, error)
	ClearBlock(context.Context, domain.Request, string, string) (domain.AccessBlock, error)
	ListBlocks(context.Context, domain.Request, int64, int) (publication.BlockPage, error)
}

func publicationRequest(ctx context.Context, product, target, asset string) (domain.Request, error) {
	principal, correlationID, err := caller(ctx)
	return domain.Request{Caller: domain.Principal(principal), ProductID: product, TargetID: target, AssetID: asset, CorrelationID: correlationID}, err
}

func targetLocation(product, target string) string {
	return "/v1/products/" + product + "/publication-targets/" + target
}

func (o operations) RegisterPublicationTarget(ctx context.Context, request RegisterPublicationTargetRequestObject) (RegisterPublicationTargetResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, "", "")
	if err != nil {
		return nil, err
	}
	result, err := o.publication.RegisterTarget(ctx, input, request.Body.AudienceConfigID, request.Body.PortalKey)
	if err != nil {
		return nil, err
	}
	body := showTarget(result.Target)
	if result.Created {
		return RegisterPublicationTarget201JSONResponse{Body: body, Headers: RegisterPublicationTarget201ResponseHeaders{
			CacheControl: noStore, Location: targetLocation(input.ProductID, result.Target.ID),
		}}, nil
	}
	return RegisterPublicationTarget200JSONResponse{Body: body, Headers: RegisterPublicationTarget200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) GetPublicationTarget(ctx context.Context, request GetPublicationTargetRequestObject) (GetPublicationTargetResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, "")
	if err != nil {
		return nil, err
	}
	target, err := o.publication.GetTarget(ctx, input)
	if err != nil {
		return nil, err
	}
	return GetPublicationTarget200JSONResponse{Body: showTarget(target), Headers: GetPublicationTarget200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) ListPublicationTargets(ctx context.Context, request ListPublicationTargetsRequestObject) (ListPublicationTargetsResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, "", "")
	if err != nil {
		return nil, err
	}
	list := "publication-targets/" + input.ProductID
	size, position, err := paging(list, request.Params.PageSize, request.Params.PageToken)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = 50
	}
	var after *ports.TargetPosition
	if position != nil {
		after = &ports.TargetPosition{At: position.At, ID: position.ID}
	}
	page, err := o.publication.ListTargets(ctx, input, after, size)
	if err != nil {
		return nil, err
	}
	items := make([]PublicationTarget, 0, len(page.Items))
	for _, target := range page.Items {
		items = append(items, showTarget(target))
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return ListPublicationTargets200JSONResponse{
		Body: PublicationTargetList{Items: items, NextPageToken: next}, Headers: ListPublicationTargets200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) GetPublicationSlot(ctx context.Context, request GetPublicationSlotRequestObject) (GetPublicationSlotResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	slot, err := o.publication.GetSlot(ctx, input)
	if err != nil {
		return nil, err
	}
	body := PublicationSlot{
		SlotID: optionalString(slot.ID), PublicationTargetID: slot.TargetID, APIAssetID: slot.AssetID,
		ActiveReleaseID: optionalString(slot.ActiveReleaseID), Generation: slot.Generation,
		Visibility: PublicationSlotVisibility(slot.Visibility()), Blocked: slot.Blocked, DeliveryStatus: "UNKNOWN",
	}
	return GetPublicationSlot200JSONResponse{Body: body, Headers: GetPublicationSlot200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) ChangePublicationSlot(ctx context.Context, request ChangePublicationSlotRequestObject) (ChangePublicationSlotResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	result, err := o.publication.ChangeSlot(ctx, input, domain.Change{
		Action: domain.Action(request.Body.Action), ExpectedGeneration: request.Body.ExpectedGeneration,
		ReleaseID: valueOf(request.Body.ReleaseID), Reason: valueOf(request.Body.Reason), IdempotencyKey: request.Params.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	return ChangePublicationSlot200JSONResponse{
		Body: showOperation(result), Headers: ChangePublicationSlot200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) ListPublicationOperations(ctx context.Context, request ListPublicationOperationsRequestObject) (ListPublicationOperationsResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	after, size := publicationPage(request.Params.AfterGeneration, request.Params.PageSize)
	page, err := o.publication.ListOperations(ctx, input, after, size)
	if err != nil {
		return nil, err
	}
	items := make([]PublicationOperation, 0, len(page.Items))
	for _, op := range page.Items {
		items = append(items, showOperation(op))
	}
	return ListPublicationOperations200JSONResponse{
		Body: PublicationOperationList{Items: items, NextAfterGeneration: page.Next}, Headers: ListPublicationOperations200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) GetPublishedArtifact(ctx context.Context, request GetPublishedArtifactRequestObject) (GetPublishedArtifactResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	artifact, err := o.publication.ActiveArtifact(ctx, input)
	if err != nil {
		return nil, err
	}
	return GetPublishedArtifact200ApplicationOctetStreamResponse{
		Body: bytes.NewReader(artifact.Payload), ContentLength: int64(len(artifact.Payload)),
		Headers: GetPublishedArtifact200ResponseHeaders{CacheControl: noStore, XArtifactDigest: artifact.Digest},
	}, nil
}

func showTarget(target domain.Target) PublicationTarget {
	return PublicationTarget{
		PublicationTargetID: target.ID, ProductID: target.ProductID, AudienceConfigID: target.AudienceConfigID,
		PortalKey: target.PortalKey, Audience: Audience(target.Audience), Environment: Environment(target.Environment),
		BlockGeneration: target.BlockGeneration, CreatedAt: target.CreatedAt,
	}
}

func showOperation(op domain.Operation) PublicationOperation {
	return PublicationOperation{
		OperationID: op.ID, SlotID: op.SlotID, Generation: op.Generation, Action: PublicationAction(op.Action),
		PreviousReleaseID: optionalString(op.PreviousReleaseID), ReleaseID: optionalString(op.ReleaseID), Reason: optionalString(op.Reason),
		PerformedBy: Principal(op.By), OccurredAt: op.At,
	}
}

func publicationPage(cursor *AfterGeneration, size *PageSize) (after int64, limit int) {
	limit = 50
	if cursor != nil {
		after = *cursor
	}
	if size != nil {
		limit = *size
	}
	return after, limit
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
