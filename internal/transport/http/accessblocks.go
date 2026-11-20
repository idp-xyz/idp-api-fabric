package http

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
)

func (o operations) BlockPublicationAccess(ctx context.Context, request BlockPublicationAccessRequestObject) (BlockPublicationAccessResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, valueOf(request.Body.APIAssetID))
	if err != nil {
		return nil, err
	}
	result, err := o.publication.BlockAccess(ctx, input, valueOf(request.Body.ReleaseID), request.Body.Reason)
	if err != nil {
		return nil, err
	}
	body := showBlock(result.Block)
	if result.Created {
		return BlockPublicationAccess201JSONResponse{Body: body, Headers: BlockPublicationAccess201ResponseHeaders{
			CacheControl: noStore, Location: targetLocation(input.ProductID, input.TargetID) + "/access-blocks/" + result.Block.ID,
		}}, nil
	}
	return BlockPublicationAccess200JSONResponse{Body: body, Headers: BlockPublicationAccess200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) GetAccessBlock(ctx context.Context, request GetAccessBlockRequestObject) (GetAccessBlockResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, "")
	if err != nil {
		return nil, err
	}
	block, err := o.publication.GetBlock(ctx, input, request.AccessBlockID)
	if err != nil {
		return nil, err
	}
	return GetAccessBlock200JSONResponse{Body: showBlock(block), Headers: GetAccessBlock200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) ClearPublicationAccessBlock(ctx context.Context, request ClearPublicationAccessBlockRequestObject) (ClearPublicationAccessBlockResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, "")
	if err != nil {
		return nil, err
	}
	block, err := o.publication.ClearBlock(ctx, input, request.AccessBlockID, request.Body.Reason)
	if err != nil {
		return nil, err
	}
	return ClearPublicationAccessBlock200JSONResponse{
		Body: showBlock(block), Headers: ClearPublicationAccessBlock200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) ListAccessBlocks(ctx context.Context, request ListAccessBlocksRequestObject) (ListAccessBlocksResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, "")
	if err != nil {
		return nil, err
	}
	after, size := publicationPage(request.Params.AfterGeneration, request.Params.PageSize)
	page, err := o.publication.ListBlocks(ctx, input, after, size)
	if err != nil {
		return nil, err
	}
	items := make([]AccessBlock, 0, len(page.Items))
	for _, block := range page.Items {
		items = append(items, showBlock(block))
	}
	return ListAccessBlocks200JSONResponse{
		Body: AccessBlockList{Items: items, NextAfterGeneration: page.Next}, Headers: ListAccessBlocks200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func showBlock(block domain.AccessBlock) AccessBlock {
	public := AccessBlock{
		AccessBlockID: block.ID, ProductID: block.ProductID, PublicationTargetID: block.TargetID,
		APIAssetID: optionalString(block.AssetID), ReleaseID: optionalString(block.ReleaseID), Generation: block.Generation,
		CreatedAt: block.CreatedAt, CreatedBy: Principal(block.CreatedBy), Reason: block.Reason, EdgeStatus: "UNKNOWN", Status: "BLOCK_ACTIVE",
	}
	if !block.Active() {
		actor := Principal(block.ClearedBy)
		public.Status, public.ClearedBy = "CLEARED", &actor
		public.ClearGeneration, public.ClearReason, public.ClearedAt = &block.ClearGeneration, &block.ClearReason, &block.ClearedAt
	}
	return public
}
