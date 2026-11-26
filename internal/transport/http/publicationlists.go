package http

import (
	"context"

	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

// defaultSlotPage is the size of a page of slots the contract declares as the
// default, for a request that names none past the validator.
const defaultSlotPage = 50

func (o operations) ListPublicationSlots(ctx context.Context, request ListPublicationSlotsRequestObject) (ListPublicationSlotsResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, request.PublicationTargetID, "")
	if err != nil {
		return nil, err
	}
	list := "publication-slots/" + input.ProductID + "/" + input.TargetID
	size, position, err := paging(list, request.Params.PageSize, request.Params.PageToken)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = defaultSlotPage
	}
	page, err := o.publication.ListSlots(ctx, input, slotPosition(position), size)
	if err != nil {
		return nil, err
	}
	return ListPublicationSlots200JSONResponse{Body: showSlots(list, page), Headers: ListPublicationSlots200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) ListAssetPublications(ctx context.Context, request ListAssetPublicationsRequestObject) (ListAssetPublicationsResponseObject, error) {
	input, err := publicationRequest(ctx, request.ProductID, "", request.APIAssetID)
	if err != nil {
		return nil, err
	}
	list := "asset-publications/" + input.ProductID + "/" + input.AssetID
	size, position, err := paging(list, request.Params.PageSize, request.Params.PageToken)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = defaultSlotPage
	}
	page, err := o.publication.ListAssetPublications(ctx, input, slotPosition(position), size)
	if err != nil {
		return nil, err
	}
	return ListAssetPublications200JSONResponse{Body: showSlots(list, page), Headers: ListAssetPublications200ResponseHeaders{CacheControl: noStore}}, nil
}

// slotPosition is the position a page token names, as the use case reads it.
func slotPosition(position *governanceports.Position) *ports.SlotPosition {
	if position == nil {
		return nil
	}
	return &ports.SlotPosition{At: position.At, ID: position.ID}
}

// showSlots is a page of slots as the contract shows it: each slot as the
// read of one shows it, and the token of the page that follows for as long as
// one does. A page without slots is an empty list, never a missing one.
func showSlots(list string, page publication.SlotPage) PublicationSlotList {
	items := make([]PublicationSlot, 0, len(page.Items))
	for _, slot := range page.Items {
		items = append(items, showSlot(slot))
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return PublicationSlotList{Items: items, NextPageToken: next}
}
